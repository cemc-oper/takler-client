// The gRPC Transport: the historical, and default, wire of the Go client.
//
// Everything gRPC the client knows lives here: the target string, the
// transport credentials, the connect / close pair, the generated stub the
// sixteen command methods delegate to, and the classification of a failed
// attempt into the transport-neutral FailureVerdict of retry.go. The rest of
// the client -- TaklerServiceClient, the Call_Wrapper, the command methods --
// only sees the Transport interface of transport.go.
//
// The classification is the one thing that must never drift from the Python
// client's classify_grpc_error in takler/client/grpc_transport.py, the
// reference implementation of the cross-language contract: same retryable set,
// same exit codes, same message shapes. Any change here must be applied there
// as well.
//
// Requirements: 13.1, 13.2, 13.3.
package common

import (
	"context"
	"fmt"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// GrpcTransport is the Transport that speaks gRPC.
//
// Nothing is connected or read from disk at construction: the CA certificate
// is read and the connection is created in Open, so a transport value can be
// built before it is known whether a call happens at all (the NO_TAKLER short
// circuit is decided in cmd).
type GrpcTransport struct {
	host string
	port string

	// security is the two configuration levels the transport credentials are
	// resolved from.
	security SecurityLevels

	conn   *grpc.ClientConn
	client pb.TaklerServerClient
}

// NewGrpcTransport returns the gRPC transport of the server at host:port whose
// connection is built from security.
func NewGrpcTransport(host string, port string, security SecurityLevels) *GrpcTransport {
	return &GrpcTransport{host: host, port: port, security: security}
}

// Open creates the connection and the generated client bound to it.
//
// The transport credentials come from BuildTransportCredentials, so a
// configured CA certificate makes this a TLS connection (requirement 13.2) and
// an absent one leaves it unencrypted (requirement 13.3). An unreadable CA
// certificate file is that function's *ExitError, returned as is: it is a
// configuration error of the request, not a failure to reach the server.
//
// grpc.NewClient replaces the deprecated grpc.Dial (requirement 13.1). It does
// not connect eagerly, so returning here means only that the target and the
// options are usable; the first RPC is what actually establishes the connection
// and what reports an unreachable server, which is where the Call_Wrapper's
// retry loop belongs.
func (t *GrpcTransport) Open() error {
	creds, err := BuildTransportCredentials(t.security.TLSFlags, t.security.TLSConfig)
	if err != nil {
		return err
	}

	conn, err := grpc.NewClient(t.getTarget(), grpc.WithTransportCredentials(creds))
	if err != nil {
		return NewExitError(
			ExitUnreachable,
			fmt.Sprintf("cannot create a connection to %s: %v", t.getServerAddress(), err),
		)
	}

	t.conn = conn
	t.client = pb.NewTaklerServerClient(conn)
	return nil
}

// Close releases the connection Open made.
//
// Close only fails on an already closed connection, which cannot happen here:
// the Call_Wrapper closes exactly the connection it opened. Calling Close
// without Open is a no-op.
func (t *GrpcTransport) Close() {
	if t.conn == nil {
		return
	}
	_ = t.conn.Close()
	t.conn = nil
	t.client = nil
}

// getServerAddress returns the "host:port" the transport talks to, as it
// appears in diagnostics.
func (t *GrpcTransport) getServerAddress() string {
	return fmt.Sprintf("%s:%s", t.host, t.port)
}

// getTarget returns the gRPC target of the connection.
//
// The address is spelled with the passthrough scheme because grpc.NewClient
// resolves a scheme-less target with the dns resolver, whereas the grpc.Dial it
// replaces defaulted to passthrough. Under dns the name is parsed and resolved
// by gRPC itself, which rejects host names the operating system accepts -- an
// HPC login node called login_a06 is the common case, as an underscore is not
// valid in a DNS name -- and would turn a working M1 deployment into a client
// that cannot connect after an upgrade. Passthrough keeps the address opaque to
// gRPC and hands it to the dialer, which resolves it exactly as before, through
// /etc/hosts included.
//
// TLS is unaffected: the credentials take the host name to verify from the
// target's authority, i.e. from this same address, unless a ServerName override
// was resolved.
func (t *GrpcTransport) getTarget() string {
	return fmt.Sprintf("passthrough:///%s", t.getServerAddress())
}

// Classify maps a failed gRPC attempt to its transport-neutral verdict; it is
// classifyGrpcError, exposed through the Transport interface.
func (t *GrpcTransport) Classify(err error) FailureVerdict {
	return classifyGrpcError(err)
}

// classifyGrpcError maps a failed gRPC attempt to its transport-neutral
// verdict (requirement 14.3, 14.7).
//
// Every failure of a gRPC call is a status error -- status.Code maps anything
// else to Unknown, which is retryable -- so unlike the Python classifier this
// one has no "not mine, re-raise" path: a programming error inside an attempt
// panics before it can be mistaken for a wire failure.
//
// The Name and LogField reproduce the exact message fragments the Call_Wrapper
// has always produced ("gRPC status UNAVAILABLE" in the error message,
// "status=UNAVAILABLE" in the retry line), so the messages pinned by the retry
// tests are byte-identical after the extraction.
func classifyGrpcError(err error) FailureVerdict {
	code := status.Code(err)
	return FailureVerdict{
		Retryable: IsRetryableStatus(code),
		ExitCode:  ExitCodeForStatus(code),
		Name:      fmt.Sprintf("gRPC status %v", code),
		LogField:  fmt.Sprintf("status=%v", code),
		Details:   status.Convert(err).Message(),
	}
}

// The sixteen command methods delegate to the generated stub bound to the
// connection. Each is one line: the retry loop, the credentials and the error
// mapping are the Call_Wrapper's, the request construction is the caller's.

func (t *GrpcTransport) RunCommandInit(ctx context.Context, req *pb.InitCommand) (*pb.ServiceResponse, error) {
	return t.client.RunCommandInit(ctx, req)
}

func (t *GrpcTransport) RunCommandComplete(ctx context.Context, req *pb.CompleteCommand) (*pb.ServiceResponse, error) {
	return t.client.RunCommandComplete(ctx, req)
}

func (t *GrpcTransport) RunCommandAbort(ctx context.Context, req *pb.AbortCommand) (*pb.ServiceResponse, error) {
	return t.client.RunCommandAbort(ctx, req)
}

func (t *GrpcTransport) RunCommandEvent(ctx context.Context, req *pb.EventCommand) (*pb.ServiceResponse, error) {
	return t.client.RunCommandEvent(ctx, req)
}

func (t *GrpcTransport) RunCommandMeter(ctx context.Context, req *pb.MeterCommand) (*pb.ServiceResponse, error) {
	return t.client.RunCommandMeter(ctx, req)
}

func (t *GrpcTransport) RunCommandRequeue(ctx context.Context, req *pb.RequeueCommand) (*pb.BatchResponse, error) {
	return t.client.RunCommandRequeue(ctx, req)
}

func (t *GrpcTransport) RunCommandSuspend(ctx context.Context, req *pb.SuspendCommand) (*pb.BatchResponse, error) {
	return t.client.RunCommandSuspend(ctx, req)
}

func (t *GrpcTransport) RunCommandResume(ctx context.Context, req *pb.ResumeCommand) (*pb.BatchResponse, error) {
	return t.client.RunCommandResume(ctx, req)
}

func (t *GrpcTransport) RunCommandRun(ctx context.Context, req *pb.RunCommand) (*pb.BatchResponse, error) {
	return t.client.RunCommandRun(ctx, req)
}

func (t *GrpcTransport) RunCommandForce(ctx context.Context, req *pb.ForceCommand) (*pb.BatchResponse, error) {
	return t.client.RunCommandForce(ctx, req)
}

func (t *GrpcTransport) RunCommandFreeDep(ctx context.Context, req *pb.FreeDepCommand) (*pb.BatchResponse, error) {
	return t.client.RunCommandFreeDep(ctx, req)
}

func (t *GrpcTransport) RunCommandLoad(ctx context.Context, req *pb.LoadCommand) (*pb.ServiceResponse, error) {
	return t.client.RunCommandLoad(ctx, req)
}

func (t *GrpcTransport) RunCommandBegin(ctx context.Context, req *pb.BeginCommand) (*pb.BatchResponse, error) {
	return t.client.RunCommandBegin(ctx, req)
}

func (t *GrpcTransport) RunRequestShow(ctx context.Context, req *pb.ShowRequest) (*pb.ShowResponse, error) {
	return t.client.RunRequestShow(ctx, req)
}

func (t *GrpcTransport) RunRequestPing(ctx context.Context, req *pb.PingRequest) (*pb.PingResponse, error) {
	return t.client.RunRequestPing(ctx, req)
}

func (t *GrpcTransport) QueryCoroutine(ctx context.Context, req *pb.CoroutineRequest) (*pb.CoroutineResponse, error) {
	return t.client.QueryCoroutine(ctx, req)
}
