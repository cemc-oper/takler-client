// The HTTP Transport of the Go client (M3 task 10).
//
// HttpTransport speaks the server's HTTP wire: it posts the JSON command
// envelope to POST /v1/commands/{command} and decodes the response envelope.
// It is implemented with the standard library's net/http only -- no new
// dependency, which is the point of taking HTTP on this client at all (the
// job-script client must stay trivially installable).
//
// Everything a call shares with the gRPC transport comes from the same one
// place, so the two transports cannot drift:
//
//   - the credentials travel as the HTTP headers takler-pass / takler-secret /
//     takler-user -- the header names are the gRPC metadata keys. The
//     Call_Wrapper attaches them to the call's context as metadata.MD exactly
//     as it does for gRPC (metadata.MD is a plain map type used here as the
//     transport-neutral credential bag); this transport is the one that turns
//     the bag into request headers (requirement 13.7);
//
//   - the retry loop is the shared RunWithRetry of retry.go: same per-attempt
//     deadline (the call's context deadline, requirement 14.2), same backoff
//     and Retry_Window, same exit codes, and byte-identical message shapes.
//     What is local to this file is only the classification of HTTP failures
//     into a FailureVerdict (classifyHttpError), mirroring the Python client's
//     classify_http_error (requirements 14.3, 14.7):
//
//   - the server never uses an HTTP status to report a business outcome, so a
//     non-200 status is always a transport-level event: 400 / 401 / 403 / 422
//     mean "the request itself is wrong" and exit as a request error, the
//     transient statuses of retryableHTTPStatuses are retried, and any other
//     status is not retried and exits as unreachable;
//
//   - a net/http failure (refused connection, reset stream, the per-attempt
//     deadline expiring) is the HTTP form of UNAVAILABLE / DEADLINE_EXCEEDED
//     and is retried.
//
// What deliberately does not cross over is client-side validation: the
// payload is built from the request and posted verbatim (meter_value "abc"
// included), because the server's end of the wire owns validation and both
// clients must be answered with the same flag for the same malformed request.
// A business failure is not retried and not turned into an error: a response
// envelope carrying a non-zero flag is handed back unchanged (requirement
// 14.8).
//
// TLS mirrors the gRPC channel rules: a configured CA certificate switches the
// base URL to https with that certificate as the root of trust, a resolved
// server name override becomes tls.Config.ServerName -- which crypto/tls
// honors natively, unlike the Python client's httpx -- and without a CA
// certificate the transport is plaintext, the recommended deployment shape
// behind a TLS-terminating reverse proxy.
package common

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
	"google.golang.org/grpc/metadata"
)

// CommandURLPrefix is the URL prefix of the command endpoint, mirroring
// API_PREFIX + "/commands/" on the server side. The full URL of a command is
// this prefix plus the command's CLI word.
const CommandURLPrefix = "/v1/commands/"

// ProtocolVersion is the envelope format version, mirroring PROTOCOL_VERSION
// in the takler repo's takler/protocol/envelope.py.
const ProtocolVersion = "1"

// retryableHTTPStatuses are the HTTP status codes that mean "transport level
// failure, worth retrying" (requirement 14.3): the request timed out or was
// throttled, or a server or proxy in between reported a transient condition.
// Business outcomes never arrive as a status code -- they are a 200 with the
// Error_Code in the envelope's flag -- so every status in this table is
// genuinely about the path, not about the command. Mirrors
// RETRYABLE_HTTP_STATUSES in the Python client's http_transport.py.
var retryableHTTPStatuses = map[int]bool{
	408: true,
	429: true,
	500: true,
	502: true,
	503: true,
	504: true,
}

// nonRetryableExitCodeByHTTPStatus are the HTTP status codes that mean "the
// request itself is wrong, retrying cannot help", mapped to the process exit
// code (requirement 14.7). The mapping mirrors the gRPC one: 401 / 403 are the
// HTTP form of UNAUTHENTICATED / PERMISSION_DENIED (both exit as a request
// error there), and 400 / 422 are the server's "the envelope is malformed or
// does not match the URL" answers, the HTTP form of INVALID_ARGUMENT. Mirrors
// NON_RETRYABLE_EXCEPTION_BY_HTTP_STATUS in the Python client.
var nonRetryableExitCodeByHTTPStatus = map[int]int{
	400: ExitRequestError,
	401: ExitRequestError,
	403: ExitRequestError,
	422: ExitRequestError,
}

// errorBodySnippetLength is how much of an unreadable error body goes into the
// failure details: enough to identify what answered (a proxy's error page,
// say), short enough for a single terminal line. Mirrors the Python client's
// _ERROR_BODY_SNIPPET_LENGTH.
const errorBodySnippetLength = 200

// errorBodyReadLimit bounds how much of an error response body is read at all,
// so a misbehaving proxy cannot make the client buffer an unbounded page for
// the sake of a snippet.
const errorBodyReadLimit = 64 * 1024

// httpStatusError is one attempt answered with a non-200 HTTP status. It is an
// internal carrier between the attempt and classifyHttpError: the retry loop
// only sees errors, so a status answer is returned as one, carrying the status
// code and the detail text the server (or a proxy) sent along.
type httpStatusError struct {
	statusCode int
	details    string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("HTTP status %d: %s", e.statusCode, e.details)
}

// httpResponseError is a 200 answer that is not a well-formed response
// envelope -- a proxy's error page with a 200, a truncated body. Like a
// malformed pb2 message on the gRPC wire it is not a retry decision at all:
// the server answered something the client cannot use, which is a server
// error (requirement 15.3), not a reason to spend Retry_Window time.
type httpResponseError struct {
	err error
}

func (e *httpResponseError) Error() string {
	return fmt.Sprintf("invalid response envelope: %v", e.err)
}

// HttpTransport is the Transport that speaks HTTP.
//
// Nothing is connected or read from disk at construction: the CA certificate
// is read and the client is built in Open, so a transport value can be built
// before it is known whether a call happens at all.
type HttpTransport struct {
	host string
	port string

	// security is the two configuration levels the TLS settings are resolved
	// from.
	security SecurityLevels

	client  *http.Client
	baseURL string
}

// NewHttpTransport returns the HTTP transport of the server at host:port whose
// TLS settings are built from security.
func NewHttpTransport(host string, port string, security SecurityLevels) *HttpTransport {
	return &HttpTransport{host: host, port: port, security: security}
}

// Open builds the HTTP client, encrypted when a CA certificate is configured.
//
// The base URL is fixed here from the current host / port / CA file: https
// when a CA is configured (requirement 13.2), plaintext http otherwise
// (requirement 13.3).
//
// An unreadable or unparseable CA certificate file is reported here, before
// any request, so the failure names the file instead of showing up as an
// unreachable server after the Retry_Window (requirement 13.12).
//
// The transport honors the environment proxy variables, as the Python
// client's httpx does by default; a redirect is not followed (httpx's default
// either), so a captive portal's 302 surfaces as the unmapped status it is
// instead of being mistaken for an answer.
func (t *HttpTransport) Open() error {
	if t.client != nil {
		return nil
	}

	tlsConfig, err := BuildHTTPTLSConfig(t.security.TLSFlags, t.security.TLSConfig)
	if err != nil {
		return err
	}

	scheme := "http"
	if tlsConfig != nil {
		scheme = "https"
	}
	t.baseURL = fmt.Sprintf("%s://%s:%s", scheme, t.host, t.port)
	t.client = &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: tlsConfig,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return nil
}

// Close releases the client's idle connections. Calling it without an open
// client is a no-op, which is what makes the Call_Wrapper's defer
// unconditional.
func (t *HttpTransport) Close() {
	if t.client == nil {
		return
	}
	t.client.CloseIdleConnections()
	t.client = nil
}

// getServerAddress returns the "host:port" the transport talks to, as it
// appears in diagnostics.
func (t *HttpTransport) getServerAddress() string {
	return fmt.Sprintf("%s:%s", t.host, t.port)
}

// Classify maps a failed HTTP attempt to its transport-neutral verdict; it is
// classifyHttpError, exposed through the Transport interface.
func (t *HttpTransport) Classify(err error) FailureVerdict {
	return classifyHttpError(err)
}

// classifyHttpError maps a failed HTTP attempt to its transport-neutral
// verdict (requirements 14.3, 14.7), mirroring the Python client's
// classify_http_error:
//
//   - a status of nonRetryableExitCodeByHTTPStatus is not retried and exits
//     with the mapped code;
//   - a status of retryableHTTPStatuses and any net/http failure of the
//     attempt itself (refused connection, reset stream, expired deadline) are
//     retried;
//   - any other status is not retried and exits as unreachable: the client
//     never reached a usable takler answer, which is the same reading the
//     gRPC side gives an unmapped status code;
//   - a malformed 200 answer (httpResponseError) is not retried and exits as
//     a server error.
func classifyHttpError(err error) FailureVerdict {
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		name := fmt.Sprintf("HTTP status %d", statusErr.statusCode)
		logField := fmt.Sprintf("status=%d", statusErr.statusCode)
		if exitCode, ok := nonRetryableExitCodeByHTTPStatus[statusErr.statusCode]; ok {
			return FailureVerdict{
				Retryable: false,
				ExitCode:  exitCode,
				Name:      name,
				LogField:  logField,
				Details:   statusErr.details,
			}
		}
		if retryableHTTPStatuses[statusErr.statusCode] {
			return FailureVerdict{
				Retryable: true,
				Name:      name,
				LogField:  logField,
				Details:   statusErr.details,
			}
		}
		return FailureVerdict{
			Retryable: false,
			ExitCode:  ExitUnreachable,
			Name:      name,
			LogField:  logField,
			Details:   statusErr.details,
		}
	}

	var responseErr *httpResponseError
	if errors.As(err, &responseErr) {
		return FailureVerdict{
			Retryable: false,
			ExitCode:  ExitServerError,
			Name:      "an invalid response envelope",
			Details:   responseErr.err.Error(),
		}
	}

	// Anything else is a connection-level failure of net/http: unreachable
	// host, refused connection, reset stream, the per-attempt deadline
	// expiring -- the HTTP form of UNAVAILABLE / DEADLINE_EXCEEDED, worth
	// spending the Retry_Window on.
	name := httpErrorTypeName(err)
	return FailureVerdict{
		Retryable: true,
		Name:      fmt.Sprintf("connection error (%s)", name),
		LogField:  fmt.Sprintf("error=%s", name),
		Details:   err.Error(),
	}
}

// httpErrorTypeName names the failure's type the way the Python client names
// the httpx exception class. A *url.Error only wraps the real cause, so the
// inner type names it: "net.OpError" rather than "url.Error".
func httpErrorTypeName(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		err = urlErr.Err
	}
	return strings.TrimPrefix(fmt.Sprintf("%T", err), "*")
}

// httpEnvelope is the JSON wire form of one request or response: the command
// envelope of takler/protocol/envelope.py. The target and auth fields are
// reserved for a future proxy shape and are never set by this client; the
// response decoding tolerates them but rejects any other extra field, which
// is the DTO layer's extra="forbid" mirrored -- both ends of the wire ship in
// lockstep, so an unknown field is a malformed answer, not an extension.
type httpEnvelope struct {
	Version string          `json:"version"`
	TraceID string          `json:"trace_id"`
	Target  json.RawMessage `json:"target"`
	Auth    json.RawMessage `json:"auth"`
	Command string          `json:"command"`
	Payload json.RawMessage `json:"payload"`
}

// newTraceID returns a fresh Trace_Id: 32 lowercase hex characters, no
// dashes, as the Python envelope's default factory produces.
func newTraceID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failing means the system has no randomness at all; a
		// trace id is decorative, so fall back to a zero id rather than
		// failing the call over it.
		return strings.Repeat("0", 32)
	}
	return hex.EncodeToString(buf[:])
}

// post sends one attempt of command with payload and returns the response
// envelope's payload.
//
// The credentials the Call_Wrapper attached to the context as metadata become
// the request's headers -- the header names are the metadata keys, so the
// wire form of the Credential_Metadata is identical on both transports
// (requirement 13.7). The per-attempt deadline is the context's, set by the
// Call_Wrapper (requirement 14.2).
//
// A non-200 answer is returned as *httpStatusError, a 200 that is not a
// well-formed envelope as *httpResponseError, and a failure of the attempt
// itself as net/http's own error; classifyHttpError sorts the three.
func (t *HttpTransport) post(
	ctx context.Context,
	command string,
	payload map[string]any,
) (json.RawMessage, error) {
	if t.client == nil {
		return nil, NewExitError(
			ExitRequestError,
			fmt.Sprintf(
				"cannot run %s on server %s: the HTTP transport is not open",
				command, t.getServerAddress(),
			),
		)
	}

	body, err := json.Marshal(map[string]any{
		"version":  ProtocolVersion,
		"trace_id": newTraceID(),
		"command":  command,
		"payload":  payload,
	})
	if err != nil {
		// The payload comes from this file's own builders, so a marshal
		// failure is a programming error, not a wire event.
		return nil, fmt.Errorf("marshal the %s request envelope: %w", command, err)
	}

	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, t.baseURL+CommandURLPrefix+command, bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("build the %s request: %w", command, err)
	}
	request.Header.Set("Content-Type", "application/json")
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		for key, values := range md {
			for _, value := range values {
				request.Header.Add(key, value)
			}
		}
	}

	response, err := t.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, &httpStatusError{
			statusCode: response.StatusCode,
			details:    httpResponseDetails(response),
		}
	}

	var answer httpEnvelope
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&answer); err != nil {
		return nil, &httpResponseError{err: err}
	}
	if answer.Payload == nil {
		return nil, &httpResponseError{err: errors.New("the envelope carries no payload")}
	}
	return answer.Payload, nil
}

// httpResponseDetails extracts the detail text of a non-200 response.
//
// The takler server answers a refusal as FastAPI's {"detail": "<text>"};
// anything else answering (a reverse proxy's error page, a truncated body) is
// quoted as a snippet. No content ever lands here that is not already on its
// way into an error message, and the server's refusal texts are sanitized by
// construction -- they name the reason, never a credential value.
func httpResponseDetails(response *http.Response) string {
	body, err := io.ReadAll(io.LimitReader(response.Body, errorBodyReadLimit))
	if err == nil {
		var parsed struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal(body, &parsed) == nil && parsed.Detail != "" {
			return parsed.Detail
		}
		snippet := string(body)
		if len(snippet) > errorBodySnippetLength {
			snippet = snippet[:errorBodySnippetLength]
		}
		return snippet
	}
	return "the error body could not be read"
}

// serviceCall posts a command answering a ServiceResponse -- every child and
// control command -- and decodes the response payload.
//
// The payload decode rejects unknown fields, mirroring the DTO layer's
// extra="forbid": a response this client cannot fully account for is a server
// error, not a partial success.
func (t *HttpTransport) serviceCall(
	ctx context.Context,
	command string,
	payload map[string]any,
) (*pb.ServiceResponse, error) {
	rawPayload, err := t.post(ctx, command, payload)
	if err != nil {
		return nil, err
	}

	var body struct {
		Flag    int32  `json:"flag"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rawPayload, &body); err != nil {
		return nil, &httpResponseError{err: err}
	}
	return &pb.ServiceResponse{Flag: body.Flag, Message: body.Message}, nil
}

// The sixteen command methods. Each builds the request DTO's JSON payload
// from the generated request message -- the field names are the DTO's, not
// the proto's (the proto's ChildCommandOptions wrapper is flattened, the
// enum-valued fields travel as their names, and LoadCommand's flow bytes are
// base64, all exactly as the DTOs' JSON-mode serialization defines) -- and
// reads the response. The connection, the timeout, the retry and the
// credentials are the Call_Wrapper's (requirements 14.1, 13.7).

func (t *HttpTransport) RunCommandInit(ctx context.Context, req *pb.InitCommand) (*pb.ServiceResponse, error) {
	return t.serviceCall(ctx, "init", map[string]any{
		"node_path": req.GetChildOptions().GetNodePath(),
		"task_id":   req.GetTaskId(),
	})
}

func (t *HttpTransport) RunCommandComplete(ctx context.Context, req *pb.CompleteCommand) (*pb.ServiceResponse, error) {
	return t.serviceCall(ctx, "complete", map[string]any{
		"node_path": req.GetChildOptions().GetNodePath(),
	})
}

func (t *HttpTransport) RunCommandAbort(ctx context.Context, req *pb.AbortCommand) (*pb.ServiceResponse, error) {
	return t.serviceCall(ctx, "abort", map[string]any{
		"node_path": req.GetChildOptions().GetNodePath(),
		"reason":    req.GetReason(),
	})
}

func (t *HttpTransport) RunCommandEvent(ctx context.Context, req *pb.EventCommand) (*pb.ServiceResponse, error) {
	return t.serviceCall(ctx, "event", map[string]any{
		"node_path":  req.GetChildOptions().GetNodePath(),
		"event_name": req.GetEventName(),
	})
}

func (t *HttpTransport) RunCommandMeter(ctx context.Context, req *pb.MeterCommand) (*pb.ServiceResponse, error) {
	// meter_value travels as the string the caller passed: the server coerces
	// it, and validating here would diverge the two clients (see the module
	// docstring).
	return t.serviceCall(ctx, "meter", map[string]any{
		"node_path":   req.GetChildOptions().GetNodePath(),
		"meter_name":  req.GetMeterName(),
		"meter_value": req.GetMeterValue(),
	})
}

func (t *HttpTransport) RunCommandRequeue(ctx context.Context, req *pb.RequeueCommand) (*pb.ServiceResponse, error) {
	return t.serviceCall(ctx, "requeue", map[string]any{
		"node_paths": req.GetNodePath(),
	})
}

func (t *HttpTransport) RunCommandSuspend(ctx context.Context, req *pb.SuspendCommand) (*pb.ServiceResponse, error) {
	return t.serviceCall(ctx, "suspend", map[string]any{
		"node_paths": req.GetNodePath(),
	})
}

func (t *HttpTransport) RunCommandResume(ctx context.Context, req *pb.ResumeCommand) (*pb.ServiceResponse, error) {
	return t.serviceCall(ctx, "resume", map[string]any{
		"node_paths": req.GetNodePath(),
	})
}

func (t *HttpTransport) RunCommandRun(ctx context.Context, req *pb.RunCommand) (*pb.ServiceResponse, error) {
	return t.serviceCall(ctx, "run", map[string]any{
		"node_paths": req.GetNodePath(),
		"force":      req.GetForce(),
	})
}

func (t *HttpTransport) RunCommandForce(ctx context.Context, req *pb.ForceCommand) (*pb.ServiceResponse, error) {
	// The state travels as its name; an unknown number cannot come from the
	// cmd layer (which validates before calling) and is spelled as the number
	// so the server's rejection names what arrived.
	state, ok := pb.ForceCommand_ForceState_name[int32(req.GetState())]
	if !ok {
		state = strconv.Itoa(int(req.GetState()))
	}
	return t.serviceCall(ctx, "force", map[string]any{
		"paths":     req.GetPath(),
		"state":     state,
		"recursive": req.GetRecursive(),
	})
}

func (t *HttpTransport) RunCommandFreeDep(ctx context.Context, req *pb.FreeDepCommand) (*pb.ServiceResponse, error) {
	depType, ok := pb.FreeDepCommand_DepType_name[int32(req.GetDepType())]
	if !ok {
		depType = strconv.Itoa(int(req.GetDepType()))
	}
	return t.serviceCall(ctx, "free-dep", map[string]any{
		"paths":    req.GetPath(),
		"dep_type": depType,
	})
}

func (t *HttpTransport) RunCommandLoad(ctx context.Context, req *pb.LoadCommand) (*pb.ServiceResponse, error) {
	return t.serviceCall(ctx, "load", map[string]any{
		"flow_type":  req.GetFlowType(),
		"flow_bytes": base64.StdEncoding.EncodeToString(req.GetFlow()),
	})
}

func (t *HttpTransport) RunCommandBegin(ctx context.Context, req *pb.BeginCommand) (*pb.ServiceResponse, error) {
	return t.serviceCall(ctx, "begin", map[string]any{
		"flow_name": req.GetFlowName(),
		"force":     req.GetForce(),
	})
}

func (t *HttpTransport) RunRequestShow(ctx context.Context, req *pb.ShowRequest) (*pb.ShowResponse, error) {
	rawPayload, err := t.post(ctx, "show", map[string]any{
		"show_trigger":   req.GetShowTrigger(),
		"show_parameter": req.GetShowParameter(),
		"show_limit":     req.GetShowLimit(),
		"show_event":     req.GetShowEvent(),
		"show_meter":     req.GetShowMeter(),
	})
	if err != nil {
		return nil, err
	}

	var body struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(rawPayload, &body); err != nil {
		return nil, &httpResponseError{err: err}
	}
	return &pb.ShowResponse{Output: body.Output}, nil
}

func (t *HttpTransport) RunRequestPing(ctx context.Context, req *pb.PingRequest) (*pb.PingResponse, error) {
	if _, err := t.post(ctx, "ping", map[string]any{}); err != nil {
		return nil, err
	}
	return &pb.PingResponse{}, nil
}

func (t *HttpTransport) QueryCoroutine(ctx context.Context, req *pb.CoroutineRequest) (*pb.CoroutineResponse, error) {
	rawPayload, err := t.post(ctx, "coroutine", map[string]any{})
	if err != nil {
		return nil, err
	}

	var body struct {
		Coroutines []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"coroutines"`
	}
	if err := json.Unmarshal(rawPayload, &body); err != nil {
		return nil, &httpResponseError{err: err}
	}

	response := &pb.CoroutineResponse{}
	for _, coroutine := range body.Coroutines {
		response.Coroutines = append(response.Coroutines, &pb.Coroutine{
			Name:        coroutine.Name,
			Description: coroutine.Description,
		})
	}
	return response, nil
}
