// The client-side transport interface and the transport selection.
//
// A *transport* is the one way this client reaches the server over a given
// wire protocol: gRPC (GrpcTransport, the default, in grpc_transport.go) or
// HTTP (HttpTransport, in http_transport.go, M3 task 10). TaklerServiceClient depends only on the Transport
// interface, so a command method never knows which protocol carries its call
// -- the same role ClientTransport plays for the Python client's
// takler/client/transport.py, which is the reference implementation.
//
// Everything the wire imposes lives behind the interface:
//
//   - the connection lifecycle (Open / Close),
//   - the encoding of the request and the decoding of the response,
//   - and the classification of a wire failure, which each transport maps to
//     the transport-neutral FailureVerdict of retry.go so the retry loop, the
//     backoff and the exit codes cannot drift between transports.
//
// What never crosses the interface is a *business* failure: a response that
// arrived carries its Error_Code in flag and is returned to the caller
// unchanged, whatever its value (requirement 14.8).
//
// The request and response types of the interface are the generated pb types:
// unlike the Python side the Go client has no separate DTO layer, so the
// generated message *is* the client's model of a command (task 2 of M3 kept it
// that way; the HTTP transport marshals it into the envelope itself).
//
// Which transport a client uses is decided by ResolveTransport -- the connect
// config's server.transport field > the TAKLER_TRANSPORT environment variable
// > gRPC. This is the address resolution chain's own ordering (the connect
// config outranks the environment there too), deliberately not the env first
// ordering of the TLS and credential resolvers: the transport belongs to the
// address the client dials.
package common

import (
	"context"
	"fmt"
	"io"
	"strings"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
)

// Transport names and the environment variable of the selection chain.
const (
	// TransportGrpc is the gRPC transport: always available, and the default.
	TransportGrpc = "grpc"

	// TransportHttp is the HTTP transport (M3 task 10): envelope JSON over
	// POST /v1/commands/{command}, implemented with the standard library's
	// net/http only.
	TransportHttp = "http"

	// DefaultTransport applies when no source selects a transport.
	DefaultTransport = TransportGrpc

	// EnvTransport is the environment variable selecting the client transport,
	// the second precedence level of ResolveTransport.
	EnvTransport = "TAKLER_TRANSPORT"
)

// Transport is one way for the client to reach the server.
//
// The method set mirrors the generated pb.TaklerServerClient interface one
// command method for one command method, minus the gRPC call options: the
// whole point of the interface is that the sixteen call sites name their
// command through it (a method expression such as Transport.RunCommandInit)
// without the generated interface appearing at the call site at all.
//
// Unlike the server side the interface is synchronous: the callers -- the
// commands of the cmd layer -- are synchronous, so the async-ness of a wire
// stack is the transport's own business.
type Transport interface {
	// Open establishes the connection, if the transport keeps one. It is
	// called before the first command of a session. May return an *ExitError
	// for a misconfiguration that must name itself before any call is
	// attempted (an unreadable TLS CA certificate).
	Open() error

	// Close releases the connection. Always safe to call, even unopened.
	Close()

	// Classify maps one failed attempt of this transport's wire to the
	// transport-neutral FailureVerdict the shared retry loop acts on: a gRPC
	// status code on one transport, an HTTP status code or a connection error
	// on the other. The classification tables are each transport's own; the
	// retry window, the backoff, the exit codes and the message shapes the
	// verdict feeds are shared, so they cannot drift (M3 task 9).
	Classify(err error) FailureVerdict

	// Child commands.

	RunCommandInit(context.Context, *pb.InitCommand) (*pb.ServiceResponse, error)
	RunCommandComplete(context.Context, *pb.CompleteCommand) (*pb.ServiceResponse, error)
	RunCommandAbort(context.Context, *pb.AbortCommand) (*pb.ServiceResponse, error)
	RunCommandEvent(context.Context, *pb.EventCommand) (*pb.ServiceResponse, error)
	RunCommandMeter(context.Context, *pb.MeterCommand) (*pb.ServiceResponse, error)

	// Control commands.

	RunCommandRequeue(context.Context, *pb.RequeueCommand) (*pb.ServiceResponse, error)
	RunCommandSuspend(context.Context, *pb.SuspendCommand) (*pb.ServiceResponse, error)
	RunCommandResume(context.Context, *pb.ResumeCommand) (*pb.ServiceResponse, error)
	RunCommandRun(context.Context, *pb.RunCommand) (*pb.ServiceResponse, error)
	RunCommandForce(context.Context, *pb.ForceCommand) (*pb.ServiceResponse, error)
	RunCommandFreeDep(context.Context, *pb.FreeDepCommand) (*pb.ServiceResponse, error)
	RunCommandLoad(context.Context, *pb.LoadCommand) (*pb.ServiceResponse, error)
	RunCommandBegin(context.Context, *pb.BeginCommand) (*pb.ServiceResponse, error)

	// Query commands.

	RunRequestShow(context.Context, *pb.ShowRequest) (*pb.ShowResponse, error)
	RunRequestPing(context.Context, *pb.PingRequest) (*pb.PingResponse, error)
	QueryCoroutine(context.Context, *pb.CoroutineRequest) (*pb.CoroutineResponse, error)
}

// normalizeTransportName returns the canonical transport name in value.
//
// Matching is case-insensitive and ignores surrounding whitespace. An
// unrecognized name is not an error: one line naming the offending value and
// its source goes to warn and ok is false, letting the next precedence source
// apply -- the same degrade-and-warn shape resolveRetryWindowFrom uses, so a
// typo can never strand a job script without a client.
func normalizeTransportName(value string, source string, warn io.Writer) (name string, ok bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case TransportGrpc, TransportHttp:
		return normalized, true
	default:
		fmt.Fprintf(
			warn,
			"invalid transport name %q from %s; expected one of: %s, %s; ignoring it.\n",
			value, source, TransportGrpc, TransportHttp,
		)
		return "", false
	}
}

// ResolveTransport resolves which transport the client uses.
//
// Applies per-source precedence -- the connect config's server.transport field
// > the TAKLER_TRANSPORT environment variable > DefaultTransport. An absent
// value at any level (empty or whitespace-only) lets the next source take
// effect, and an unrecognized name degrades to the next source with one line
// of diagnostics rather than failing.
//
// lookupEnv and warn are injected so a test exercises the chain without
// touching the process environment or the real standard error, the same
// pattern resolveRetryWindowFrom uses.
func ResolveTransport(
	configValue string,
	lookupEnv func(string) (string, bool),
	warn io.Writer,
) string {
	if strings.TrimSpace(configValue) != "" {
		if name, ok := normalizeTransportName(configValue, "the connect config server section", warn); ok {
			return name
		}
	}

	if envValue, found := lookupEnv(EnvTransport); found && strings.TrimSpace(envValue) != "" {
		if name, ok := normalizeTransportName(envValue, "the "+EnvTransport+" environment variable", warn); ok {
			return name
		}
	}

	return DefaultTransport
}

// newTransport builds the transport named name for the server at host:port.
//
// name is expected to come from ResolveTransport, so it is canonical already;
// an empty string means "not provided" and defaults to gRPC. Any other
// unknown name is a programming error of the caller, reported as an
// *ExitError rather than a panic so the cmd layer's single exit point stays
// the only place that ends the process (requirement 15.9).
func newTransport(name string, host string, port string, security SecurityLevels) (Transport, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", TransportGrpc:
		return NewGrpcTransport(host, port, security), nil
	case TransportHttp:
		return NewHttpTransport(host, port, security), nil
	default:
		return nil, NewExitError(
			ExitRequestError,
			fmt.Sprintf("unknown transport name %q, want one of: %s, %s", name, TransportGrpc, TransportHttp),
		)
	}
}
