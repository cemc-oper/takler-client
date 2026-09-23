// Tests of the HTTP transport (M3 task 10), against an in-process HTTP server
// from net/http/httptest -- the HTTP counterpart of the bufconn harness of
// testing_test.go, and reused the same way: no port is prechosen, nothing
// escapes the test process, and every received call is recorded for
// assertions on the envelope, the credential headers and the attempt count.
//
// The wire contract asserted here is the one the takler server's HTTP
// transport (M3 task 7) and the Python client's (task 8) already implement:
// envelope JSON posted to /v1/commands/{command}, credentials as the
// takler-* headers, business failures as a 200 with the Error_Code in the
// envelope's flag, and HTTP statuses reserved for transport-level events.
package common

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
)

// pemEncodeCert returns cert in PEM form, for writing a CA file in a test.
func pemEncodeCert(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}

// fakeHTTPCall is one request the fake HTTP server received, with the envelope
// already decoded.
type fakeHTTPCall struct {
	Path    string
	Header  http.Header
	Version string
	TraceID string
	Command string
	Payload map[string]any
}

// fakeHTTPServer is a programmable HTTP command endpoint.
//
// It mirrors fakeServicer of the gRPC harness along the same axes:
// httpFailFirst forces a status code on the leading calls (the only way to
// observe that the retry loop actually retried), and every received request
// is recorded with its headers, so a test can assert which credentials went
// onto the wire.
type fakeHTTPServer struct {
	*httptest.Server

	mu        sync.Mutex
	failCode  int
	failCount int // 0 = never fail, N = fail the leading N calls, -1 = always
	payloads  map[string]any
	calls     []fakeHTTPCall
}

// fakeHTTPOption configures a fakeHTTPServer.
type fakeHTTPOption func(*fakeHTTPServer)

// httpFailFirst makes the first n calls answer code and every later call
// succeed; n < 0 fails every call.
func httpFailFirst(n int, code int) fakeHTTPOption {
	return func(s *fakeHTTPServer) {
		s.failCode = code
		s.failCount = n
	}
}

// httpWithPayloads sets the response payload per command name.
func httpWithPayloads(payloads map[string]any) fakeHTTPOption {
	return func(s *fakeHTTPServer) { s.payloads = payloads }
}

// newFakeHTTPServer starts an in-process HTTP server answering the command
// endpoint and registers its teardown with t.Cleanup. The response to a
// successful call is a well-formed envelope echoing the request's trace_id,
// as the takler server produces it.
func newFakeHTTPServer(t *testing.T, opts ...fakeHTTPOption) *fakeHTTPServer {
	t.Helper()

	fake := &fakeHTTPServer{}
	for _, opt := range opts {
		opt(fake)
	}

	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope struct {
			Version string         `json:"version"`
			TraceID string         `json:"trace_id"`
			Command string         `json:"command"`
			Payload map[string]any `json:"payload"`
		}
		_ = json.NewDecoder(r.Body).Decode(&envelope)

		fake.mu.Lock()
		fake.calls = append(fake.calls, fakeHTTPCall{
			Path:    r.URL.Path,
			Header:  r.Header.Clone(),
			Version: envelope.Version,
			TraceID: envelope.TraceID,
			Command: envelope.Command,
			Payload: envelope.Payload,
		})
		attempt := len(fake.calls)
		failCount := fake.failCount
		failCode := fake.failCode
		payload := fake.payloads[envelope.Command]
		fake.mu.Unlock()
		if payload == nil && isBatchCommand(envelope.Command) {
			targets, _ := envelope.Payload["node_paths"].([]any)
			if targets == nil {
				targets, _ = envelope.Payload["paths"].([]any)
			}
			if name, ok := envelope.Payload["flow_name"].(string); ok && name != "" {
				targets = []any{"/" + name}
			}
			results := []any{}
			for i, target := range targets {
				results = append(results, map[string]any{"index": i, "target": target, "flag": 0, "message": "success", "effect": "applied"})
			}
			payload = map[string]any{"flag": 0, "message": "success", "results": results}
		}

		if failCount < 0 || attempt <= failCount {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(failCode)
			_ = json.NewEncoder(w).Encode(map[string]any{"detail": "fake server refuses"})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version":  "1",
			"trace_id": envelope.TraceID,
			"command":  envelope.Command,
			"payload":  payload,
		})
	}))
	t.Cleanup(fake.Server.Close)
	return fake
}

// hostPort returns the host and the port of the fake server.
func (f *fakeHTTPServer) hostPort(t *testing.T) (string, string) {
	t.Helper()
	host, port, err := net.SplitHostPort(strings.TrimPrefix(f.URL, "http://"))
	if err != nil {
		t.Fatalf("split the fake server address %q: %v", f.URL, err)
	}
	return host, port
}

// callCount returns how many requests the server received.
func (f *fakeHTTPServer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// lastCall returns the most recent request, failing the test when none
// arrived.
func (f *fakeHTTPServer) lastCall(t *testing.T) fakeHTTPCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("fake HTTP server received no call")
	}
	return f.calls[len(f.calls)-1]
}

// newHTTPClient returns a TaklerServiceClient speaking HTTP to the fake
// server, in an environment scrubbed of TLS and credential settings.
func newHTTPClient(t *testing.T, server *fakeHTTPServer) *TaklerServiceClient {
	t.Helper()

	credUnsetEnv(t, EnvJobPassword)
	credUnsetEnv(t, EnvSecretFile)
	credUnsetEnv(t, TaklerTlsCaFile)

	host, port := server.hostPort(t)
	client, err := NewTaklerServiceClient(host, port, TransportHttp, SecurityLevels{})
	if err != nil {
		t.Fatalf("build an HTTP client: %v", err)
	}
	return client
}

// httpCommandPayloads is the literal request payload contract of every
// command: the DTO field names and wire shapes the takler server's HTTP
// transport validates, written out as the JSON decoding of what goes onto
// the wire. It is a table of literals on purpose -- deriving it from the
// payload builders themselves could not detect a wrong field name.
var httpCommandPayloads = []struct {
	name    string
	payload map[string]any
	call    func(t Transport) error
}{
	{"init", map[string]any{"node_path": "/flow1/task1", "task_id": "job-1"},
		func(tr Transport) error {
			_, err := tr.RunCommandInit(context.Background(), &pb.InitCommand{
				ChildOptions: &pb.ChildCommandOptions{NodePath: "/flow1/task1"},
				TaskId:       "job-1",
			})
			return err
		}},
	{"complete", map[string]any{"node_path": "/flow1/task1"},
		func(tr Transport) error {
			_, err := tr.RunCommandComplete(context.Background(), &pb.CompleteCommand{
				ChildOptions: &pb.ChildCommandOptions{NodePath: "/flow1/task1"},
			})
			return err
		}},
	{"abort", map[string]any{"node_path": "/flow1/task1", "reason": "boom"},
		func(tr Transport) error {
			_, err := tr.RunCommandAbort(context.Background(), &pb.AbortCommand{
				ChildOptions: &pb.ChildCommandOptions{NodePath: "/flow1/task1"},
				Reason:       "boom",
			})
			return err
		}},
	{"event", map[string]any{"node_path": "/flow1/task1", "event_name": "data_ready"},
		func(tr Transport) error {
			_, err := tr.RunCommandEvent(context.Background(), &pb.EventCommand{
				ChildOptions: &pb.ChildCommandOptions{NodePath: "/flow1/task1"},
				EventName:    "data_ready",
			})
			return err
		}},
	// meter_value is the string the caller passed, verbatim: "abc" must cross
	// the wire unvalidated, so the server answers both clients with the same
	// flag for the same malformed request.
	{"meter", map[string]any{"node_path": "/flow1/task1", "meter_name": "progress", "meter_value": "abc"},
		func(tr Transport) error {
			_, err := tr.RunCommandMeter(context.Background(), &pb.MeterCommand{
				ChildOptions: &pb.ChildCommandOptions{NodePath: "/flow1/task1"},
				MeterName:    "progress",
				MeterValue:   "abc",
			})
			return err
		}},
	{"requeue", map[string]any{"node_paths": []any{"/flow1", "/flow2"}},
		func(tr Transport) error {
			_, err := tr.RunCommandRequeue(context.Background(), &pb.RequeueCommand{
				NodePath: []string{"/flow1", "/flow2"},
			})
			return err
		}},
	{"suspend", map[string]any{"node_paths": []any{"/flow1", "/flow2"}},
		func(tr Transport) error {
			_, err := tr.RunCommandSuspend(context.Background(), &pb.SuspendCommand{
				NodePath: []string{"/flow1", "/flow2"},
			})
			return err
		}},
	{"resume", map[string]any{"node_paths": []any{"/flow1", "/flow2"}},
		func(tr Transport) error {
			_, err := tr.RunCommandResume(context.Background(), &pb.ResumeCommand{
				NodePath: []string{"/flow1", "/flow2"},
			})
			return err
		}},
	{"run", map[string]any{"node_paths": []any{"/flow1/task1"}, "force": true},
		func(tr Transport) error {
			_, err := tr.RunCommandRun(context.Background(), &pb.RunCommand{
				NodePath: []string{"/flow1/task1"},
				Force:    true,
			})
			return err
		}},
	{"force", map[string]any{"paths": []any{"/flow1/task1"}, "state": "complete", "recursive": true},
		func(tr Transport) error {
			_, err := tr.RunCommandForce(context.Background(), &pb.ForceCommand{
				Path:      []string{"/flow1/task1"},
				State:     pb.ForceCommand_ForceState(pb.ForceCommand_ForceState_value["complete"]),
				Recursive: true,
			})
			return err
		}},
	{"free-dep", map[string]any{"paths": []any{"/flow1/task1"}, "dep_type": "trigger"},
		func(tr Transport) error {
			_, err := tr.RunCommandFreeDep(context.Background(), &pb.FreeDepCommand{
				Path:    []string{"/flow1/task1"},
				DepType: pb.FreeDepCommand_DepType(pb.FreeDepCommand_DepType_value["trigger"]),
			})
			return err
		}},
	// flow_bytes is base64 on the JSON wire, as the DTO's JSON-mode
	// serialization defines; the literal below is base64 of `{"a": 1}`.
	{"load", map[string]any{"flow_type": "json", "flow_bytes": "eyJhIjogMX0="},
		func(tr Transport) error {
			_, err := tr.RunCommandLoad(context.Background(), &pb.LoadCommand{
				FlowType: "json",
				Flow:     []byte(`{"a": 1}`),
			})
			return err
		}},
	{"begin", map[string]any{"flow_name": "flow1", "force": false},
		func(tr Transport) error {
			_, err := tr.RunCommandBegin(context.Background(), &pb.BeginCommand{
				FlowName: "flow1",
			})
			return err
		}},
	{"show", map[string]any{
		"show_trigger":   true,
		"show_parameter": false,
		"show_limit":     true,
		"show_event":     false,
		"show_meter":     true,
	},
		func(tr Transport) error {
			_, err := tr.RunRequestShow(context.Background(), &pb.ShowRequest{
				ShowTrigger: true,
				ShowLimit:   true,
				ShowMeter:   true,
			})
			return err
		}},
	{"ping", map[string]any{},
		func(tr Transport) error {
			_, err := tr.RunRequestPing(context.Background(), &pb.PingRequest{})
			return err
		}},
	{"coroutine", map[string]any{},
		func(tr Transport) error {
			_, err := tr.QueryCoroutine(context.Background(), &pb.CoroutineRequest{})
			return err
		}},
}

// TestHttpTransportEnvelopeShape pins the wire shape of every one of the
// sixteen commands: the URL is the command endpoint, the envelope carries the
// version, a fresh trace_id and the command name, and the payload is exactly
// the literal contract table above.
func TestHttpTransportEnvelopeShape(t *testing.T) {
	successPayloads := map[string]any{
		"show":      map[string]any{"output": "tree text"},
		"ping":      map[string]any{},
		"coroutine": map[string]any{"coroutines": []any{}},
	}
	server := newFakeHTTPServer(t, httpWithPayloads(successPayloads))
	host, port := server.hostPort(t)

	for _, command := range httpCommandPayloads {
		t.Run(command.name, func(t *testing.T) {
			transport := NewHttpTransport(host, port, SecurityLevels{})
			if err := transport.Open(); err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer transport.Close()

			if err := command.call(transport); err != nil {
				t.Fatalf("%s: %v", command.name, err)
			}

			call := server.lastCall(t)
			if got, want := call.Path, CommandURLPrefix+command.name; got != want {
				t.Errorf("path = %q, want %q", got, want)
			}
			if got := call.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			if got, want := call.Version, ProtocolVersion; got != want {
				t.Errorf("envelope version = %q, want %q", got, want)
			}
			if len(call.TraceID) != 32 || strings.ToLower(call.TraceID) != call.TraceID {
				t.Errorf("trace_id = %q, want 32 lowercase hex characters", call.TraceID)
			}
			if got, want := call.Command, command.name; got != want {
				t.Errorf("envelope command = %q, want %q", got, want)
			}

			wantPayload, _ := json.Marshal(command.payload)
			gotPayload, _ := json.Marshal(call.Payload)
			if string(gotPayload) != string(wantPayload) {
				t.Errorf("payload = %s, want %s", gotPayload, wantPayload)
			}
		})
	}
}

// TestHttpTransportCredentialHeaders asserts that the credentials the
// Call_Wrapper attaches to the call's context reach the wire as the takler-*
// headers of the cross-language contract -- the header names are the gRPC
// metadata keys (requirement 13.7).
func TestHttpTransportCredentialHeaders(t *testing.T) {
	t.Run("a child command carries takler-pass", func(t *testing.T) {
		server := newFakeHTTPServer(t)
		client := newHTTPClient(t, server)
		t.Setenv(EnvJobPassword, credPasswordValue)

		if _, err := client.RunCommandInit("/flow1/task1", "job-1"); err != nil {
			t.Fatalf("init: %v", err)
		}

		call := server.lastCall(t)
		if got := call.Header.Get("Takler-Pass"); got != credPasswordValue {
			t.Errorf("takler-pass header = %q, want the job password", got)
		}
		if got := call.Header.Get("Takler-Secret"); got != "" {
			t.Errorf("takler-secret header = %q, want it absent on a child command", got)
		}
	})

	t.Run("an operator command carries takler-secret and takler-user", func(t *testing.T) {
		server := newFakeHTTPServer(t)
		client := newHTTPClient(t, server)
		t.Setenv(EnvSecretFile, credSecretFile(t, credSecretValue+"\n"))
		username := credRequireUsername(t)

		if _, err := client.RunCommandSuspend([]string{"/flow1"}); err != nil {
			t.Fatalf("suspend: %v", err)
		}

		call := server.lastCall(t)
		if got := call.Header.Get("Takler-Secret"); got != credSecretValue {
			t.Errorf("takler-secret header = %q, want the operator secret", got)
		}
		if got := call.Header.Get("Takler-User"); got != username {
			t.Errorf("takler-user header = %q, want %q", got, username)
		}
		if got := call.Header.Get("Takler-Pass"); got != "" {
			t.Errorf("takler-pass header = %q, want it absent on an operator command", got)
		}
	})
}

// TestHttpTransportBusinessFailureIsNotAnError pins requirement 14.8 on the
// HTTP wire: a 200 envelope carrying a non-zero flag comes back as the
// response, on the first attempt, with no error.
func TestHttpTransportBusinessFailureIsNotAnError(t *testing.T) {
	server := newFakeHTTPServer(t, httpWithPayloads(map[string]any{
		"complete": map[string]any{"flag": 43, "message": "refused"},
	}))
	client := newHTTPClient(t, server)

	response, err := client.RunCommandComplete("/flow1/task1")
	if err != nil {
		t.Fatalf("complete: %v, want the response carrying flag 43", err)
	}
	if got := response.GetFlag(); got != 43 {
		t.Errorf("flag = %d, want 43", got)
	}
	if got := response.GetMessage(); got != "refused" {
		t.Errorf("message = %q, want %q", got, "refused")
	}
	if got := server.callCount(); got != 1 {
		t.Errorf("the server received %d calls, want exactly 1: a non-zero flag is not retried", got)
	}
}

// TestHttpTransportStatusClassification drives the status table end to end
// through the Call_Wrapper with a zero Retry_Window, so a retryable status is
// observable as "tried once, then unreachable" and a non-retryable one as
// "tried once, then the mapped exit code".
func TestHttpTransportStatusClassification(t *testing.T) {
	cases := []struct {
		code     int
		exitCode int
	}{
		// "The request itself is wrong" -- the HTTP form of the four
		// non-retryable gRPC codes.
		{code: 400, exitCode: ExitRequestError},
		{code: 401, exitCode: ExitRequestError},
		{code: 403, exitCode: ExitRequestError},
		{code: 422, exitCode: ExitRequestError},
		// Any other status is neither retryable nor a request error: the
		// client never reached a usable takler answer, which is unreachable.
		{code: 404, exitCode: ExitUnreachable},
		{code: 501, exitCode: ExitUnreachable},
	}

	for _, c := range cases {
		t.Run(fmt.Sprintf("status %d", c.code), func(t *testing.T) {
			t.Setenv(EnvRetryWindow, "0")
			server := newFakeHTTPServer(t, httpFailFirst(-1, c.code))
			client := newHTTPClient(t, server)

			_, err := client.RunCommandSuspend([]string{"/flow1"})

			exitErr := retryRequireExitError(t, err, c.exitCode)
			retryAssertMessageContains(
				t, "the failure message", exitErr.Message,
				"suspend", fmt.Sprintf("HTTP status %d", c.code), "fake server refuses",
			)
			if got := server.callCount(); got != 1 {
				t.Errorf("the server received %d calls, want exactly 1: %d is not retried", got, c.code)
			}
		})
	}
}

// TestHttpTransportRetriesRetryableStatus is the happy path of the retry loop
// on the HTTP wire: the server answers 503 twice and succeeds on the third
// attempt, with the backoff and the diagnostics line of the shared loop.
func TestHttpTransportRetriesRetryableStatus(t *testing.T) {
	credCleanEnv(t)
	server := newFakeHTTPServer(t, httpFailFirst(2, 503), httpWithPayloads(map[string]any{
		"complete": map[string]any{"flag": 0, "message": ""},
	}))
	host, port := server.hostPort(t)

	transport := NewHttpTransport(host, port, SecurityLevels{})
	if err := transport.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer transport.Close()

	// The client is never opened: it supplies the credentials, the address for
	// diagnostics and the HTTP classifier, while the attempts run through the
	// transport opened above.
	client, err := NewTaklerServiceClient(host, port, TransportHttp, SecurityLevels{})
	if err != nil {
		t.Fatalf("build the client: %v", err)
	}

	clock := newRetryFakeClock()
	var warn strings.Builder

	response, err := callWith(
		client,
		context.Background(),
		"complete",
		KindChild,
		&pb.CompleteCommand{ChildOptions: &pb.ChildCommandOptions{NodePath: "/flow1/task1"}},
		transport.RunCommandComplete,
		callSettings{policy: retryPolicyWithClock(86400*time.Second, clock), warn: &warn},
	)
	if err != nil {
		t.Fatalf("callWith: %v, want the third attempt to succeed", err)
	}
	if response == nil {
		t.Fatal("response is nil, want the server's response")
	}

	if got := server.callCount(); got != 3 {
		t.Errorf("the server received %d calls, want 3", got)
	}
	wantSleeps := []time.Duration{1 * time.Second, 2 * time.Second}
	if len(clock.sleeps) != len(wantSleeps) {
		t.Fatalf("slept %v, want %v", clock.sleeps, wantSleeps)
	}
	for i, d := range wantSleeps {
		if clock.sleeps[i] != d {
			t.Errorf("sleep %d = %v, want %v", i+1, clock.sleeps[i], d)
		}
	}

	warnings := strings.Split(strings.TrimRight(warn.String(), "\n"), "\n")
	if len(warnings) != 2 {
		t.Fatalf("wrote %d diagnostics lines for 2 retries, want one each:\n%s", len(warnings), warn.String())
	}
	address := host + ":" + port
	want := []string{
		fmt.Sprintf("retry complete to %s: elapsed=0.0s, status=503", address),
		fmt.Sprintf("retry complete to %s: elapsed=1.0s, status=503", address),
	}
	for i, w := range want {
		if warnings[i] != w {
			t.Errorf("diagnostics line %d = %q, want %q", i+1, warnings[i], w)
		}
	}
}

// TestHttpTransportConnectionErrorIsRetryable drives a call against a closed
// port: the refused connection is the HTTP form of UNAVAILABLE, so with a
// zero window the call ends unreachable after exactly one attempt, and the
// message names the failure as a connection error.
func TestHttpTransportConnectionErrorIsRetryable(t *testing.T) {
	t.Setenv(EnvRetryWindow, "0")
	credCleanEnv(t)
	credUnsetEnv(t, TaklerTlsCaFile)

	client, err := NewTaklerServiceClient("127.0.0.1", "1", TransportHttp, SecurityLevels{})
	if err != nil {
		t.Fatalf("build the client: %v", err)
	}

	_, err = client.RunCommandComplete("/flow1/task1")

	exitErr := retryRequireExitError(t, err, ExitUnreachable)
	retryAssertMessageContains(
		t, "the unreachable message", exitErr.Message,
		"127.0.0.1:1", "1 attempts", "last connection error (",
	)
}

// TestHttpTransportMalformedResponseIsAServerError pins the counterpart of a
// malformed pb2 message on the gRPC wire: a 200 that is not a well-formed
// envelope is a server error (requirement 15.3), never a retry decision --
// even with the window wide open exactly one attempt happens.
func TestHttpTransportMalformedResponseIsAServerError(t *testing.T) {
	credCleanEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("this is not an envelope"))
	}))
	t.Cleanup(server.Close)

	host, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("split the fake server address: %v", err)
	}
	transport := NewHttpTransport(host, port, SecurityLevels{})
	if err := transport.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer transport.Close()

	client, err := NewTaklerServiceClient(host, port, TransportHttp, SecurityLevels{})
	if err != nil {
		t.Fatalf("build the client: %v", err)
	}

	clock := newRetryFakeClock()
	var warn strings.Builder

	_, err = callWith(
		client,
		context.Background(),
		"show",
		KindQuery,
		&pb.ShowRequest{},
		transport.RunRequestShow,
		callSettings{policy: retryPolicyWithClock(86400*time.Second, clock), warn: &warn},
	)

	exitErr := retryRequireExitError(t, err, ExitServerError)
	retryAssertMessageContains(t, "the failure message", exitErr.Message, "invalid response envelope")
	if len(clock.sleeps) != 0 {
		t.Errorf("slept %v, want no wait at all: a malformed answer is not retried", clock.sleeps)
	}
}

// TestClassifyHttpError pins the classification table itself, the HTTP
// counterpart of TestClassifyGrpcError.
func TestClassifyHttpError(t *testing.T) {
	t.Run("a non-retryable status", func(t *testing.T) {
		verdict := classifyHttpError(&httpStatusError{statusCode: 401, details: "credential required"})

		if verdict.Retryable {
			t.Error("401 is retryable, want it not retryable")
		}
		if got, want := verdict.ExitCode, ExitRequestError; got != want {
			t.Errorf("exit code = %d, want %d", got, want)
		}
		if got, want := verdict.Name, "HTTP status 401"; got != want {
			t.Errorf("name = %q, want %q", got, want)
		}
		if got, want := verdict.LogField, "status=401"; got != want {
			t.Errorf("log field = %q, want %q", got, want)
		}
		if got, want := verdict.Details, "credential required"; got != want {
			t.Errorf("details = %q, want %q", got, want)
		}
	})

	t.Run("a retryable status", func(t *testing.T) {
		verdict := classifyHttpError(&httpStatusError{statusCode: 503, details: "unavailable"})

		if !verdict.Retryable {
			t.Error("503 is not retryable, want it retryable")
		}
		if got, want := verdict.LogField, "status=503"; got != want {
			t.Errorf("log field = %q, want %q", got, want)
		}
	})

	t.Run("an unmapped status", func(t *testing.T) {
		verdict := classifyHttpError(&httpStatusError{statusCode: 418, details: "teapot"})

		if verdict.Retryable {
			t.Error("418 is retryable, want it not retryable")
		}
		if got, want := verdict.ExitCode, ExitUnreachable; got != want {
			t.Errorf("exit code = %d, want %d", got, want)
		}
	})

	t.Run("a connection error names the inner cause", func(t *testing.T) {
		err := &url.Error{Op: "Post", URL: "http://x", Err: &net.OpError{Op: "dial", Err: errors.New("refused")}}

		verdict := classifyHttpError(err)

		if !verdict.Retryable {
			t.Error("a connection error is not retryable, want it retryable")
		}
		if got, want := verdict.Name, "connection error (net.OpError)"; got != want {
			t.Errorf("name = %q, want %q", got, want)
		}
		if got, want := verdict.LogField, "error=net.OpError"; got != want {
			t.Errorf("log field = %q, want %q", got, want)
		}
	})

	t.Run("a malformed response is a server error", func(t *testing.T) {
		verdict := classifyHttpError(&httpResponseError{err: errors.New("no payload")})

		if verdict.Retryable {
			t.Error("a malformed response is retryable, want it not retryable")
		}
		if got, want := verdict.ExitCode, ExitServerError; got != want {
			t.Errorf("exit code = %d, want %d", got, want)
		}
	})
}

// TestHttpTransportLifecycle covers Open and Close: a call before Open is a
// clear error, Open is idempotent, and Close without Open is harmless.
func TestHttpTransportLifecycle(t *testing.T) {
	server := newFakeHTTPServer(t)
	host, port := server.hostPort(t)

	transport := NewHttpTransport(host, port, SecurityLevels{})

	// Close before Open must be harmless.
	transport.Close()

	_, err := transport.RunRequestPing(context.Background(), &pb.PingRequest{})
	exitErr := retryRequireExitError(t, err, ExitRequestError)
	retryAssertMessageContains(t, "the not-open message", exitErr.Message, "not open")

	if err := transport.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got, want := transport.baseURL, "http://"+host+":"+port; got != want {
		t.Errorf("base URL = %q, want %q", got, want)
	}
	// A second Open keeps the same client.
	first := transport.client
	if err := transport.Open(); err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if transport.client != first {
		t.Error("a second Open replaced the client")
	}

	if _, err := transport.RunRequestPing(context.Background(), &pb.PingRequest{}); err != nil {
		t.Fatalf("ping: %v", err)
	}

	transport.Close()
	if transport.client != nil {
		t.Error("Close left the client behind")
	}
	transport.Close()
}

// TestHttpTransportTLS exercises the HTTPS path against an httptest TLS
// server: the server's own certificate PEM as the CA file switches the
// transport to https and verifies the chain (requirement 13.2); an unreadable
// CA file fails at Open, naming the path (requirement 13.12).
func TestHttpTransportTLS(t *testing.T) {
	t.Run("https with the server's CA succeeds", func(t *testing.T) {
		credCleanEnv(t)

		tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"version": "1", "trace_id": "t", "command": "ping", "payload": map[string]any{},
			})
		}))
		t.Cleanup(tlsServer.Close)

		host, port, err := net.SplitHostPort(strings.TrimPrefix(tlsServer.URL, "https://"))
		if err != nil {
			t.Fatalf("split the TLS server address: %v", err)
		}

		caFile := filepath.Join(t.TempDir(), "ca.crt")
		if err := os.WriteFile(caFile, pemEncodeCert(tlsServer.Certificate()), 0600); err != nil {
			t.Fatalf("write the CA file: %v", err)
		}

		transport := NewHttpTransport(host, port, SecurityLevels{TLSFlags: TLSSettings{CaFile: caFile}})
		if err := transport.Open(); err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer transport.Close()

		if got, want := transport.baseURL, "https://"+host+":"+port; got != want {
			t.Errorf("base URL = %q, want %q", got, want)
		}
		if _, err := transport.RunRequestPing(context.Background(), &pb.PingRequest{}); err != nil {
			t.Fatalf("ping over TLS: %v", err)
		}
	})

	t.Run("a server name override is honored", func(t *testing.T) {
		credCleanEnv(t)
		tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{}"))
		}))
		t.Cleanup(tlsServer.Close)

		host, port, err := net.SplitHostPort(strings.TrimPrefix(tlsServer.URL, "https://"))
		if err != nil {
			t.Fatalf("split the TLS server address: %v", err)
		}

		caFile := filepath.Join(t.TempDir(), "ca.crt")
		if err := os.WriteFile(caFile, pemEncodeCert(tlsServer.Certificate()), 0600); err != nil {
			t.Fatalf("write the CA file: %v", err)
		}

		// The httptest certificate covers 127.0.0.1, not this name: if the
		// override reaches tls.Config.ServerName the handshake must fail,
		// which is what proves the override is honored (crypto/tls supports
		// it natively, unlike the Python client's httpx).
		transport := NewHttpTransport(host, port, SecurityLevels{TLSFlags: TLSSettings{
			CaFile:     caFile,
			ServerName: "not-the-servers-name.example",
		}})
		if err := transport.Open(); err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer transport.Close()

		if _, err := transport.RunRequestPing(context.Background(), &pb.PingRequest{}); err == nil {
			t.Error("ping with a wrong server name override succeeded, want a TLS verification failure")
		}
	})

	t.Run("an unreadable CA file fails at Open", func(t *testing.T) {
		credCleanEnv(t)
		missing := filepath.Join(t.TempDir(), "absent-ca.crt")

		transport := NewHttpTransport("localhost", "8083", SecurityLevels{TLSFlags: TLSSettings{CaFile: missing}})

		err := transport.Open()
		if err == nil {
			t.Fatal("Open succeeded, want a CA certificate failure")
		}
		var exitErr *ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("error is %T, want *ExitError", err)
		}
		if exitErr.Code != ExitRequestError {
			t.Errorf("exit code = %d, want %d", exitErr.Code, ExitRequestError)
		}
		if !strings.Contains(exitErr.Message, missing) {
			t.Errorf("message %q does not name the file %q", exitErr.Message, missing)
		}
	})
}
