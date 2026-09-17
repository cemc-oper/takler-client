// Tests of the connection construction (requirements 13.1, 13.2, 13.3).
//
// None of these dials a server: grpc.NewClient does not connect eagerly, so
// everything this file asserts -- the target spelling, the credential failure
// path, the connect / close pairing -- is observable without a listener.
package common

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
)

// The target must keep the passthrough scheme the replaced grpc.Dial defaulted
// to, so that the address is resolved by the dialer instead of by gRPC's dns
// resolver, which rejects host names such as an HPC login node's login_a06.
func TestClientTargetKeepsPassthroughScheme(t *testing.T) {
	client := NewTaklerServiceClient("login_a06", "33083", SecurityLevels{})

	if got, want := client.getServerAddress(), "login_a06:33083"; got != want {
		t.Errorf("server address = %q, want %q", got, want)
	}
	if got, want := client.getTarget(), "passthrough:///login_a06:33083"; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
}

// Without a CA certificate the connection is unencrypted and is created without
// error, which is what keeps an M1 deployment working (requirement 13.3). The
// connection is created lazily, so this establishes nothing and needs no
// server.
func TestConnectWithoutCaCertificateSucceeds(t *testing.T) {
	t.Setenv(TaklerTlsCaFile, "")

	client := NewTaklerServiceClient("localhost", "33083", SecurityLevels{})

	generated, err := client.connect()
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.closeConnection()

	if generated == nil {
		t.Error("connect returned no client")
	}
	if client.conn == nil {
		t.Error("connect left no connection on the client")
	}
}

// An unreadable CA certificate file is a configuration error of the request, so
// connect returns BuildTransportCredentials' *ExitError rather than dying, and
// no connection is left behind (requirements 13.12, 15.9).
func TestConnectWithUnreadableCaCertificateReturnsExitError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent-ca.crt")

	client := NewTaklerServiceClient("localhost", "33083", SecurityLevels{
		TLSFlags: TLSSettings{CaFile: missing},
	})

	generated, err := client.connect()
	if err == nil {
		client.closeConnection()
		t.Fatal("connect succeeded, want a CA certificate failure")
	}
	if generated != nil {
		t.Error("connect returned a client together with the error")
	}
	if client.conn != nil {
		t.Error("connect left a connection behind after failing")
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

// withConnection runs the body with a usable client and releases the connection
// afterwards, which is the boilerplate every command method now shares.
func TestWithConnectionClosesAfterTheBody(t *testing.T) {
	client := NewTaklerServiceClient("localhost", "33083", SecurityLevels{})

	called := 0
	err := client.withConnection(func(generated pb.TaklerServerClient) error {
		called++
		if generated == nil {
			t.Error("withConnection passed no client to the body")
		}
		if client.conn == nil {
			t.Error("withConnection ran the body without a connection")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withConnection: %v", err)
	}

	if called != 1 {
		t.Errorf("body ran %d times, want 1", called)
	}
	if client.conn != nil {
		t.Error("withConnection left the connection open")
	}
}

// A failure to build the connection must skip the body and surface as is.
func TestWithConnectionSkipsTheBodyWhenConnectFails(t *testing.T) {
	client := NewTaklerServiceClient("localhost", "33083", SecurityLevels{
		TLSFlags: TLSSettings{CaFile: filepath.Join(t.TempDir(), "absent-ca.crt")},
	})

	err := client.withConnection(func(pb.TaklerServerClient) error {
		t.Error("withConnection ran the body although connect failed")
		return nil
	})
	if err == nil {
		t.Fatal("withConnection succeeded, want a CA certificate failure")
	}
}

// The body's error is the caller's error: nothing on the connection path
// swallows or rewraps it.
func TestWithConnectionReturnsTheBodyError(t *testing.T) {
	client := NewTaklerServiceClient("localhost", "33083", SecurityLevels{})
	want := NewExitError(ExitServerError, "body failed")

	err := client.withConnection(func(pb.TaklerServerClient) error { return want })

	if !errors.Is(err, error(want)) {
		t.Errorf("error = %v, want %v", err, want)
	}
}

// Every client carries Credentials, including one built without security levels,
// so the Call_Wrapper can ask for the Credential_Metadata unconditionally.
func TestClientAlwaysCarriesCredentials(t *testing.T) {
	if got := NewTaklerServiceClient("localhost", "33083", SecurityLevels{}).Credentials(); got == nil {
		t.Error("client built without security levels carries no credentials")
	}

	client := NewTaklerServiceClient("localhost", "33083", SecurityLevels{
		CredFlags: CredentialSettings{SecretFile: "/flag/secret"},
	})
	credentials := client.Credentials()
	if credentials == nil {
		t.Fatal("client carries no credentials")
	}
	if got, want := credentials.flags.SecretFile, "/flag/secret"; got != want {
		t.Errorf("credential flag secret file = %q, want %q", got, want)
	}
}
