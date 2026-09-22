// In-process gRPC test server for the Go client tests (requirement 16.12).
//
// Every behaviour the client layer adds around an RPC -- backoff retry, single
// call timeouts, credential metadata injection, error to exit code mapping --
// is only observable from the far end of a real gRPC connection. Testing it
// against a real Takler server would tie the Go tests to a Python process, a
// listening port and a port allocation race in CI. Instead the harness here
// runs a gRPC server over google.golang.org/grpc/test/bufconn, an in-memory
// net.Listener: no port is bound, nothing escapes the test process, and the
// connection is torn down deterministically when the test ends.
//
// The fake servicer is programmable along the three axes the client tests care
// about:
//
//   - fakeWithFlag / fakeWithMessage: what a successful ServiceResponse says,
//     which drives the Error_Code to exit code mapping tests,
//   - fakeAlwaysFail: force one gRPC status code on every call, which drives
//     the retryable / non-retryable classification tests,
//   - fakeFailFirst: fail the leading N attempts then succeed, which is the
//     only way to observe that the retry loop actually retried.
//
// Independently of those, every received call is recorded together with the
// metadata taken from metadata.FromIncomingContext, so a test can assert on
// what credentials the client put on the wire (requirement 16.16) and on how
// many attempts it made.
//
// This file contains test infrastructure plus one smoke test proving the
// harness itself works. The assertions about client behaviour live in the
// per-feature test files.
package common

import (
	"context"
	"net"
	"sync"
	"testing"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

// fakeBufferSize is the bufconn buffer size. 1 MiB dwarfs every message this
// service exchanges, so a slow reader can never turn into a deadlock inside a
// test.
const fakeBufferSize = 1024 * 1024

// fakeAlways is the fakeServicer.failCount value meaning "fail every attempt",
// as opposed to a non-negative count meaning "fail the leading N attempts".
const fakeAlways = -1

// fakeCall is one call the fake servicer received.
type fakeCall struct {
	// Method is the bare RPC name, e.g. "RunCommandInit".
	Method string

	// Metadata is a copy of the incoming metadata, lowercase keys as gRPC
	// delivers them. Never nil, empty when the client sent none.
	Metadata metadata.MD

	// Request is the request message as received, e.g. *pb.ForceCommand. It is
	// what a test asserts a command method's request construction against.
	Request proto.Message
}

// value returns the first value of key, or "" when the key is absent. key is
// matched as gRPC stores it, i.e. lowercase.
func (c fakeCall) value(key string) string {
	values := c.Metadata.Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// fakeServicer is a programmable TaklerServer implementation.
//
// It embeds UnimplementedTaklerServerServer so only the handful of RPCs the
// client tests exercise need a body; anything else answers Unimplemented, which
// is a clear failure signal rather than a nil dereference.
//
// All state is guarded by mu: gRPC serves each call in its own goroutine, so
// even a single-threaded test can have the recording race with the assertion
// if the client cancels a call and moves on while the handler is still running.
type fakeServicer struct {
	pb.UnimplementedTaklerServerServer

	mu sync.Mutex

	// flag and message are the ServiceResponse fields returned on success.
	flag    int32
	message string

	// failCode is the status code returned by a failing attempt.
	failCode codes.Code

	// failCount is how many leading attempts fail: 0 means none, N means the
	// first N, fakeAlways means all of them.
	failCount int

	// coroutines is what QueryCoroutine answers.
	coroutines []*pb.Coroutine

	// calls is every call received, in arrival order.
	calls []fakeCall
}

// fakeOption configures a fakeServicer.
type fakeOption func(*fakeServicer)

// fakeWithFlag sets the flag of the returned ServiceResponse (requirement
// 16.12). The flag carries the Error_Code, so this is how a test picks which
// server outcome the client has to map.
func fakeWithFlag(flag int32) fakeOption {
	return func(s *fakeServicer) { s.flag = flag }
}

// fakeWithMessage sets the message of the returned ServiceResponse.
func fakeWithMessage(message string) fakeOption {
	return func(s *fakeServicer) { s.message = message }
}

// fakeAlwaysFail makes every call fail with code.
func fakeAlwaysFail(code codes.Code) fakeOption {
	return func(s *fakeServicer) {
		s.failCode = code
		s.failCount = fakeAlways
	}
}

// fakeFailFirst makes the first n calls fail with code and every later call
// succeed. n <= 0 means no call fails.
func fakeFailFirst(n int, code codes.Code) fakeOption {
	return func(s *fakeServicer) {
		s.failCode = code
		if n < 0 {
			n = 0
		}
		s.failCount = n
	}
}

// fakeWithCoroutines sets the coroutines QueryCoroutine answers with.
func fakeWithCoroutines(coroutines ...*pb.Coroutine) fakeOption {
	return func(s *fakeServicer) { s.coroutines = coroutines }
}

// newFakeServicer builds a servicer that succeeds with flag 0 unless the
// options say otherwise.
func newFakeServicer(opts ...fakeOption) *fakeServicer {
	s := &fakeServicer{failCode: codes.Unavailable}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// admit records the call together with its request and returns the error this
// attempt should fail with, or nil when it should succeed.
func (s *fakeServicer) admit(ctx context.Context, method string, request proto.Message) error {
	s.mu.Lock()
	incoming, _ := metadata.FromIncomingContext(ctx)
	s.calls = append(s.calls, fakeCall{Method: method, Metadata: incoming.Copy(), Request: request})
	attempt := len(s.calls)
	failCount := s.failCount
	failCode := s.failCode
	s.mu.Unlock()

	if failCount == fakeAlways || attempt <= failCount {
		return status.Errorf(failCode, "fake server: attempt %d fails with %v", attempt, failCode)
	}
	return nil
}

// serviceCall is the body shared by every RPC returning a ServiceResponse.
func (s *fakeServicer) serviceCall(ctx context.Context, method string, request proto.Message) (*pb.ServiceResponse, error) {
	if err := s.admit(ctx, method, request); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.ServiceResponse{Flag: s.flag, Message: s.message}, nil
}

// callCount returns how many calls the servicer received.
func (s *fakeServicer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// receivedCalls returns a snapshot of every received call, in arrival order.
func (s *fakeServicer) receivedCalls() []fakeCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]fakeCall, len(s.calls))
	copy(out, s.calls)
	return out
}

// lastCall returns the most recent call, failing the test when none arrived.
func (s *fakeServicer) lastCall(t *testing.T) fakeCall {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		t.Fatal("fake server received no call")
	}
	return s.calls[len(s.calls)-1]
}

// Child commands.

func (s *fakeServicer) RunCommandInit(ctx context.Context, req *pb.InitCommand) (*pb.ServiceResponse, error) {
	return s.serviceCall(ctx, "RunCommandInit", req)
}

func (s *fakeServicer) RunCommandComplete(ctx context.Context, req *pb.CompleteCommand) (*pb.ServiceResponse, error) {
	return s.serviceCall(ctx, "RunCommandComplete", req)
}

func (s *fakeServicer) RunCommandAbort(ctx context.Context, req *pb.AbortCommand) (*pb.ServiceResponse, error) {
	return s.serviceCall(ctx, "RunCommandAbort", req)
}

// Control commands.

func (s *fakeServicer) RunCommandRequeue(ctx context.Context, req *pb.RequeueCommand) (*pb.ServiceResponse, error) {
	return s.serviceCall(ctx, "RunCommandRequeue", req)
}

func (s *fakeServicer) RunCommandSuspend(ctx context.Context, req *pb.SuspendCommand) (*pb.ServiceResponse, error) {
	return s.serviceCall(ctx, "RunCommandSuspend", req)
}

func (s *fakeServicer) RunCommandResume(ctx context.Context, req *pb.ResumeCommand) (*pb.ServiceResponse, error) {
	return s.serviceCall(ctx, "RunCommandResume", req)
}

func (s *fakeServicer) RunCommandRun(ctx context.Context, req *pb.RunCommand) (*pb.ServiceResponse, error) {
	return s.serviceCall(ctx, "RunCommandRun", req)
}

func (s *fakeServicer) RunCommandForce(ctx context.Context, req *pb.ForceCommand) (*pb.ServiceResponse, error) {
	return s.serviceCall(ctx, "RunCommandForce", req)
}

func (s *fakeServicer) RunCommandFreeDep(ctx context.Context, req *pb.FreeDepCommand) (*pb.ServiceResponse, error) {
	return s.serviceCall(ctx, "RunCommandFreeDep", req)
}

func (s *fakeServicer) RunCommandLoad(ctx context.Context, req *pb.LoadCommand) (*pb.ServiceResponse, error) {
	return s.serviceCall(ctx, "RunCommandLoad", req)
}

func (s *fakeServicer) RunCommandBegin(ctx context.Context, req *pb.BeginCommand) (*pb.ServiceResponse, error) {
	return s.serviceCall(ctx, "RunCommandBegin", req)
}

// Query commands.

func (s *fakeServicer) RunRequestShow(ctx context.Context, req *pb.ShowRequest) (*pb.ShowResponse, error) {
	if err := s.admit(ctx, "RunRequestShow", req); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.ShowResponse{Output: s.message}, nil
}

func (s *fakeServicer) RunRequestPing(ctx context.Context, req *pb.PingRequest) (*pb.PingResponse, error) {
	if err := s.admit(ctx, "RunRequestPing", req); err != nil {
		return nil, err
	}
	return &pb.PingResponse{}, nil
}

func (s *fakeServicer) QueryCoroutine(ctx context.Context, req *pb.CoroutineRequest) (*pb.CoroutineResponse, error) {
	if err := s.admit(ctx, "QueryCoroutine", req); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.CoroutineResponse{Coroutines: s.coroutines}, nil
}

// fakeServer is a running in-process server together with a client connection
// to it.
type fakeServer struct {
	// Servicer is the programmable servicer, for assertions on what arrived.
	Servicer *fakeServicer

	// Conn is a client connection to the in-process server.
	Conn *grpc.ClientConn

	// Client is a generated client bound to Conn.
	Client pb.TaklerServerClient

	listener   *bufconn.Listener
	grpcServer *grpc.Server
	stopOnce   sync.Once
}

// newFakeServer starts an in-process gRPC server carrying a fakeServicer
// configured by opts, dials it, and registers the teardown with t.Cleanup
// (requirement 16.12). Nothing binds a network port.
func newFakeServer(t *testing.T, opts ...fakeOption) *fakeServer {
	t.Helper()
	return newFakeServerWithServicer(t, newFakeServicer(opts...))
}

// newFakeServerWithServicer is newFakeServer with the servicer supplied by the
// caller, for a test that needs to reconfigure or inspect it before dialing.
func newFakeServerWithServicer(t *testing.T, servicer *fakeServicer) *fakeServer {
	t.Helper()

	listener := bufconn.Listen(fakeBufferSize)
	grpcServer := grpc.NewServer()
	pb.RegisterTaklerServerServer(grpcServer, servicer)

	served := make(chan struct{})
	go func() {
		defer close(served)
		// Serve returns ErrServerStopped or a closed listener error at
		// teardown; neither is worth reporting, and reporting it would race
		// with the test finishing.
		_ = grpcServer.Serve(listener)
	}()

	conn, cleanupConn := newFakeConn(t, listener)

	f := &fakeServer{
		Servicer:   servicer,
		Conn:       conn,
		Client:     pb.NewTaklerServerClient(conn),
		listener:   listener,
		grpcServer: grpcServer,
	}

	t.Cleanup(func() {
		cleanupConn()
		f.Stop()
		<-served
	})
	return f
}

// newFakeConn dials listener and returns the connection plus its cleanup func.
//
// grpc.NewClient replaces the deprecated grpc.Dial. Two consequences shape the
// call below: NewClient resolves its target with the dns resolver by default,
// so the target is spelled with the passthrough scheme to keep the bufconn
// address opaque; and the transport is reached only through the context dialer,
// which hands back an in-memory pipe instead of a socket.
func newFakeConn(t *testing.T, listener *bufconn.Listener) (*grpc.ClientConn, func()) {
	t.Helper()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial in-process server: %v", err)
	}

	var once sync.Once
	return conn, func() { once.Do(func() { _ = conn.Close() }) }
}

// Stop shuts the server down. It is idempotent and is called automatically at
// the end of the test that created the server.
func (f *fakeServer) Stop() {
	f.stopOnce.Do(func() {
		f.grpcServer.Stop()
		_ = f.listener.Close()
	})
}

// newTCPFakeServer starts a fakeServicer on a loopback TCP listener and
// returns the port it is listening on together with the servicer.
//
// The bufconn server above is only reachable through the connection handed out
// here, which the production TaklerServiceClient cannot use: its withTransport
// always dials its own target. Exercising a command method end to end --
// request construction included -- therefore needs a real socket, and a
// loopback listener on an ephemeral port gives one without a port allocation
// race or anything escaping the test machine.
func newTCPFakeServer(t *testing.T, opts ...fakeOption) (string, *fakeServicer) {
	t.Helper()

	servicer := newFakeServicer(opts...)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on a loopback ephemeral port: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterTaklerServerServer(grpcServer, servicer)

	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = grpcServer.Serve(listener)
	}()

	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
		<-served
	})

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("read the listener's address: %v", err)
	}
	return port, servicer
}

// newTCPClient returns a TaklerServiceClient pointed at a TCP fake server, in
// an environment scrubbed of TLS and credential settings so the call goes out
// unencrypted and unauthenticated regardless of the developer's shell.
func newTCPClient(t *testing.T, opts ...fakeOption) (*TaklerServiceClient, *fakeServicer) {
	t.Helper()

	credUnsetEnv(t, EnvJobPassword)
	credUnsetEnv(t, EnvSecretFile)
	credUnsetEnv(t, TaklerTlsCaFile)

	port, servicer := newTCPFakeServer(t, opts...)
	return newTestClient(t, "127.0.0.1", port, SecurityLevels{}), servicer
}

// grpcRPC adapts a generated stub method value, whose signature carries the
// variadic gRPC call options, to the invoke shape Call takes, so a Call test
// can drive the real retry loop through a stub bound to the in-process server.
func grpcRPC[Req any, Resp any](
	method func(context.Context, Req, ...grpc.CallOption) (Resp, error),
) func(context.Context, Req) (Resp, error) {
	return func(ctx context.Context, req Req) (Resp, error) {
		return method(ctx, req)
	}
}

// TestFakeServerHarness is the smoke test of the harness above: it proves a
// call reaches the in-process server, that the response is the configured one,
// that a forced status code surfaces on the client, that the leading N attempts
// can be made to fail, and that outgoing metadata is captured for assertions.
func TestFakeServerHarness(t *testing.T) {
	t.Run("configured flag and captured metadata", func(t *testing.T) {
		server := newFakeServer(t, fakeWithFlag(7), fakeWithMessage("hello"))

		ctx := metadata.AppendToOutgoingContext(
			context.Background(), "takler-user", "operator",
		)
		response, err := server.Client.RunCommandInit(ctx, &pb.InitCommand{
			ChildOptions: &pb.ChildCommandOptions{NodePath: "/flow1/task1"},
			TaskId:       "job-1",
		})
		if err != nil {
			t.Fatalf("RunCommandInit: %v", err)
		}
		if got := response.GetFlag(); got != 7 {
			t.Errorf("flag = %d, want 7", got)
		}
		if got := response.GetMessage(); got != "hello" {
			t.Errorf("message = %q, want %q", got, "hello")
		}

		call := server.Servicer.lastCall(t)
		if call.Method != "RunCommandInit" {
			t.Errorf("method = %q, want %q", call.Method, "RunCommandInit")
		}
		if got := call.value("takler-user"); got != "operator" {
			t.Errorf("takler-user = %q, want %q", got, "operator")
		}
		if got := call.value("takler-secret"); got != "" {
			t.Errorf("takler-secret = %q, want it absent", got)
		}
	})

	t.Run("forced status code", func(t *testing.T) {
		server := newFakeServer(t, fakeAlwaysFail(codes.PermissionDenied))

		_, err := server.Client.RunCommandRequeue(context.Background(), &pb.RequeueCommand{})
		if err == nil {
			t.Fatal("RunCommandRequeue succeeded, want PermissionDenied")
		}
		if got := status.Code(err); got != codes.PermissionDenied {
			t.Errorf("status code = %v, want %v", got, codes.PermissionDenied)
		}
	})

	t.Run("first attempts fail then succeed", func(t *testing.T) {
		server := newFakeServer(t, fakeFailFirst(2, codes.Unavailable))

		for attempt := 1; attempt <= 2; attempt++ {
			if _, err := server.Client.RunRequestPing(context.Background(), &pb.PingRequest{}); err == nil {
				t.Fatalf("attempt %d succeeded, want %v", attempt, codes.Unavailable)
			} else if got := status.Code(err); got != codes.Unavailable {
				t.Fatalf("attempt %d status code = %v, want %v", attempt, got, codes.Unavailable)
			}
		}
		if _, err := server.Client.RunRequestPing(context.Background(), &pb.PingRequest{}); err != nil {
			t.Fatalf("attempt 3: %v", err)
		}

		if got := server.Servicer.callCount(); got != 3 {
			t.Errorf("call count = %d, want 3", got)
		}
		for i, call := range server.Servicer.receivedCalls() {
			if call.Method != "RunRequestPing" {
				t.Errorf("call %d method = %q, want %q", i, call.Method, "RunRequestPing")
			}
		}
	})
}
