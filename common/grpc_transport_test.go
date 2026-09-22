// Tests of the gRPC transport's own wire behaviour (requirements 13.1, 13.2,
// 13.3): the target spelling, the credential failure path of Open, and the
// classification of a failed attempt into a FailureVerdict.
//
// None of the connection tests dials a server: grpc.NewClient does not
// connect eagerly, so everything they assert is observable without a
// listener.
package common

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The target must keep the passthrough scheme the replaced grpc.Dial defaulted
// to, so that the address is resolved by the dialer instead of by gRPC's dns
// resolver, which rejects host names such as an HPC login node's login_a06.
func TestGrpcTargetKeepsPassthroughScheme(t *testing.T) {
	transport := NewGrpcTransport("login_a06", "33083", SecurityLevels{})

	if got, want := transport.getServerAddress(), "login_a06:33083"; got != want {
		t.Errorf("server address = %q, want %q", got, want)
	}
	if got, want := transport.getTarget(), "passthrough:///login_a06:33083"; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
}

// Without a CA certificate the connection is unencrypted and is created without
// error, which is what keeps an M1 deployment working (requirement 13.3). The
// connection is created lazily, so this establishes nothing and needs no
// server.
func TestGrpcOpenWithoutCaCertificateSucceeds(t *testing.T) {
	t.Setenv(TaklerTlsCaFile, "")

	transport := NewGrpcTransport("localhost", "33083", SecurityLevels{})

	if err := transport.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer transport.Close()

	if transport.conn == nil {
		t.Error("Open left no connection on the transport")
	}
	if transport.client == nil {
		t.Error("Open left no generated client on the transport")
	}
}

// An unreadable CA certificate file is a configuration error of the request, so
// Open returns BuildTransportCredentials' *ExitError rather than dying, and no
// connection is left behind (requirements 13.12, 15.9).
func TestGrpcOpenWithUnreadableCaCertificateReturnsExitError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent-ca.crt")

	transport := NewGrpcTransport("localhost", "33083", SecurityLevels{
		TLSFlags: TLSSettings{CaFile: missing},
	})

	err := transport.Open()
	if err == nil {
		transport.Close()
		t.Fatal("Open succeeded, want a CA certificate failure")
	}
	if transport.conn != nil {
		t.Error("Open left a connection behind after failing")
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
}

// Close without Open is a no-op, which is what makes the Call_Wrapper's defer
// unconditional.
func TestGrpcCloseWithoutOpenIsHarmless(t *testing.T) {
	transport := NewGrpcTransport("localhost", "33083", SecurityLevels{})
	transport.Close()

	if transport.conn != nil {
		t.Error("Close without Open left a connection behind")
	}
}

// The classifier is where the gRPC wire failure meets the transport-neutral
// retry loop: the retryable set and the exit codes come from retry.go and
// exitcode.go, and the Name and LogField reproduce the exact message fragments
// the Call_Wrapper has always produced, so the retry diagnostics stay
// byte-identical across the extraction.
func TestClassifyGrpcError(t *testing.T) {
	err := status.Error(codes.Unavailable, "connection refused")

	verdict := classifyGrpcError(err)

	if !verdict.Retryable {
		t.Error("Unavailable is not retryable, want it retryable")
	}
	if got, want := verdict.Name, fmt.Sprintf("gRPC status %v", codes.Unavailable); got != want {
		t.Errorf("name = %q, want %q", got, want)
	}
	if got, want := verdict.LogField, fmt.Sprintf("status=%v", codes.Unavailable); got != want {
		t.Errorf("log field = %q, want %q", got, want)
	}
	if got, want := verdict.Details, "connection refused"; got != want {
		t.Errorf("details = %q, want %q", got, want)
	}

	verdict = classifyGrpcError(status.Error(codes.PermissionDenied, "bad secret"))
	if verdict.Retryable {
		t.Error("PermissionDenied is retryable, want it not retryable")
	}
	if got, want := verdict.ExitCode, ExitRequestError; got != want {
		t.Errorf("exit code = %d, want %d", got, want)
	}
}
