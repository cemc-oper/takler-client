// Tests of the client construction and the transport lifetime.
//
// The client is the command surface: it carries the address for diagnostics,
// the Credentials of every call, and the Transport the calls travel over. The
// wire specifics of the gRPC transport -- the target spelling, the credential
// failure path of Open -- have their own tests in grpc_transport_test.go.
package common

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// newTestClient is the test front of NewTaklerServiceClient: gRPC, and a
// construction error fails the test.
func newTestClient(t *testing.T, host string, port string, security SecurityLevels) *TaklerServiceClient {
	t.Helper()

	client, err := NewTaklerServiceClient(host, port, TransportGrpc, security)
	if err != nil {
		t.Fatalf("build a test client: %v", err)
	}
	return client
}

// The constructor carries the resolved transport name into the transport it
// builds: empty and "grpc" build the gRPC transport, "http" builds the HTTP
// transport, and a garbage name -- which ResolveTransport would have degraded
// before it got here -- is rejected as the programming error it is.
func TestNewClientSelectsTheTransport(t *testing.T) {
	t.Run("empty name and grpc build the gRPC transport", func(t *testing.T) {
		for _, name := range []string{"", TransportGrpc} {
			client, err := NewTaklerServiceClient("localhost", "33083", name, SecurityLevels{})
			if err != nil {
				t.Fatalf("transport %q: %v", name, err)
			}
			if _, ok := client.transport.(*GrpcTransport); !ok {
				t.Errorf("transport %q built %T, want *GrpcTransport", name, client.transport)
			}
		}
	})

	t.Run("http builds the HTTP transport", func(t *testing.T) {
		client, err := NewTaklerServiceClient("localhost", "8083", TransportHttp, SecurityLevels{})
		if err != nil {
			t.Fatalf("transport %q: %v", TransportHttp, err)
		}
		if _, ok := client.transport.(*HttpTransport); !ok {
			t.Errorf("transport %q built %T, want *HttpTransport", TransportHttp, client.transport)
		}
	})

	t.Run("an unknown name is rejected", func(t *testing.T) {
		_, err := NewTaklerServiceClient("localhost", "33083", "carrier-pigeon", SecurityLevels{})

		var exitErr *ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("error is %v (%T), want *ExitError", err, err)
		}
		if exitErr.Code != ExitRequestError {
			t.Errorf("exit code = %d, want %d", exitErr.Code, ExitRequestError)
		}
		if !strings.Contains(exitErr.Message, "carrier-pigeon") {
			t.Errorf("message %q does not name the offending value", exitErr.Message)
		}
	})
}

// withTransport opens the transport, runs the body with it, and closes the
// transport afterwards, which is the boilerplate every command method now
// shares.
func TestWithTransportClosesAfterTheBody(t *testing.T) {
	client := newTestClient(t, "localhost", "33083", SecurityLevels{})
	grpcTransport := client.transport.(*GrpcTransport)

	called := 0
	err := client.withTransport(func(transport Transport) error {
		called++
		if transport == nil {
			t.Error("withTransport passed no transport to the body")
		}
		if grpcTransport.conn == nil {
			t.Error("withTransport ran the body without a connection")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withTransport: %v", err)
	}

	if called != 1 {
		t.Errorf("body ran %d times, want 1", called)
	}
	if grpcTransport.conn != nil {
		t.Error("withTransport left the connection open")
	}
}

// A failure to open the transport must skip the body and surface as is.
func TestWithTransportSkipsTheBodyWhenOpenFails(t *testing.T) {
	client := newTestClient(t, "localhost", "33083", SecurityLevels{
		TLSFlags: TLSSettings{CaFile: filepath.Join(t.TempDir(), "absent-ca.crt")},
	})

	err := client.withTransport(func(Transport) error {
		t.Error("withTransport ran the body although Open failed")
		return nil
	})
	if err == nil {
		t.Fatal("withTransport succeeded, want a CA certificate failure")
	}
}

// The body's error is the caller's error: nothing on the transport path
// swallows or rewraps it.
func TestWithTransportReturnsTheBodyError(t *testing.T) {
	client := newTestClient(t, "localhost", "33083", SecurityLevels{})
	want := NewExitError(ExitServerError, "body failed")

	err := client.withTransport(func(Transport) error { return want })

	if !errors.Is(err, error(want)) {
		t.Errorf("error = %v, want %v", err, want)
	}
}

// Every client carries Credentials, including one built without security levels,
// so the Call_Wrapper can ask for the Credential_Metadata of a call
// unconditionally.
func TestClientAlwaysCarriesCredentials(t *testing.T) {
	if got := newTestClient(t, "localhost", "33083", SecurityLevels{}).Credentials(); got == nil {
		t.Error("client built without security levels carries no credentials")
	}

	client := newTestClient(t, "localhost", "33083", SecurityLevels{
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
