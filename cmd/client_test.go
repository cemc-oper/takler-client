package cmd

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/perillaroc/takler-client/common"
	pb "github.com/perillaroc/takler-client/takler_protocol"
)

// captureStdout runs body with standard output redirected, and returns what it
// wrote there. The commands print with fmt.Printf, so this is the only way to
// read back what an operator would see.
func captureStdout(t *testing.T, body func()) string {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	saved := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = saved }()

	done := make(chan string, 1)
	go func() {
		output, _ := io.ReadAll(reader)
		done <- string(output)
	}()

	body()

	if err := writer.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	return <-done
}

// A successful command must report the Error_Code classification name, not the
// raw flag integer (requirement 15.8).
func TestReportCommandResponsePrintsClassificationName(t *testing.T) {
	var err error
	output := captureStdout(t, func() {
		err = reportCommandResponse(&pb.ServiceResponse{Flag: 0})
	})

	if err != nil {
		t.Fatalf("report response: %v", err)
	}
	if got, want := strings.TrimSpace(output), "received: success"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// A non zero flag is a business failure: it must become an *ExitError carrying
// the contract's exit code, with the classification name in the message
// (requirements 15.2, 15.3, 15.8, 15.9).
func TestReportCommandResponseMapsNonZeroFlagToExitError(t *testing.T) {
	cases := []struct {
		name     string
		flag     int32
		message  string
		wantCode int
		wantName string
	}{
		{"request error", 10, "no such node", common.ExitRequestError, "node_not_found"},
		{"permission denied", 43, "denied", common.ExitRequestError, "permission_denied"},
		{"zombie", 31, "zombie", common.ExitServerError, "zombie"},
		{"server error", 99, "boom", common.ExitServerError, "internal_error"},
		{"unregistered code", 77, "who knows", common.ExitServerError, common.UnknownErrorName},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var err error
			output := captureStdout(t, func() {
				err = reportCommandResponse(&pb.ServiceResponse{Flag: c.flag, Message: c.message})
			})

			if output != "" {
				t.Errorf("stdout = %q, want nothing on failure", output)
			}

			var exitErr *common.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("error = %v, want *ExitError", err)
			}
			if exitErr.Code != c.wantCode {
				t.Errorf("exit code = %d, want %d", exitErr.Code, c.wantCode)
			}
			if !strings.Contains(exitErr.Message, c.wantName) {
				t.Errorf("message = %q, want it to name %q", exitErr.Message, c.wantName)
			}
			if !strings.Contains(exitErr.Message, c.message) {
				t.Errorf("message = %q, want it to carry %q", exitErr.Message, c.message)
			}
		})
	}
}

// A missing response must not be read as success.
func TestReportCommandResponseWithoutResponse(t *testing.T) {
	err := reportCommandResponse(nil)

	var exitErr *common.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %v, want *ExitError", err)
	}
	if exitErr.Code != common.ExitServerError {
		t.Errorf("exit code = %d, want %d", exitErr.Code, common.ExitServerError)
	}
}

// NO_TAKLER short circuits every child command before anything else happens: the
// command succeeds, and the connect config is not even read, which is what
// pointing TAKLER_CONNECT_FILE at a missing file proves -- reaching the address
// resolution would fail on it (requirement 15.10).
func TestChildCommandsShortCircuitOnNoTakler(t *testing.T) {
	commands := map[string]func() error{
		"init":     func() error { return newInitCommand().runCommand(nil, nil) },
		"complete": func() error { return newCompleteCommand().runCommand(nil, nil) },
		"abort":    func() error { return newAbortCommand().runCommand(nil, nil) },
		"event":    func() error { return newEventCommand().runCommand(nil, nil) },
		"meter":    func() error { return newMeterCommand().runCommand(nil, nil) },
	}

	for name, run := range commands {
		t.Run(name, func(t *testing.T) {
			t.Setenv(NoTakler, "1")
			t.Setenv(TaklerConnectFile, filepath.Join(t.TempDir(), "absent.yaml"))

			var err error
			output := captureStdout(t, func() { err = run() })

			if err != nil {
				t.Fatalf("%s with NO_TAKLER set: %v", name, err)
			}
			if !strings.Contains(output, NoTakler) {
				t.Errorf("output = %q, want it to mention %s", output, NoTakler)
			}
		})
	}
}

// The address resolution has four levels, the command's own options winning.
func TestResolveServerTargetPrecedence(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		target, err := resolveServerTarget("", "")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if target.host != DefaultHost || target.port != DefaultPort {
			t.Errorf("target = %s:%s, want %s:%s", target.host, target.port, DefaultHost, DefaultPort)
		}
		if target.config != nil {
			t.Errorf("config = %+v, want nil", target.config)
		}
	})

	t.Run("environment", func(t *testing.T) {
		t.Setenv(TaklerHost, "env_host")
		t.Setenv(TaklerPort, "1234")

		target, err := resolveServerTarget("", "")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if target.host != "env_host" || target.port != "1234" {
			t.Errorf("target = %s:%s, want env_host:1234", target.host, target.port)
		}
	})

	t.Run("connect config over environment", func(t *testing.T) {
		t.Setenv(TaklerHost, "env_host")
		t.Setenv(TaklerConnectFile, writeConnectConfig(t, `server:
  address:
    hostname: config_host
    port: "5678"
`))

		target, err := resolveServerTarget("", "")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if target.host != "config_host" || target.port != "5678" {
			t.Errorf("target = %s:%s, want config_host:5678", target.host, target.port)
		}
		if target.config == nil {
			t.Fatal("config = nil, want the parsed connect config")
		}
	})

	t.Run("options over everything", func(t *testing.T) {
		t.Setenv(TaklerHost, "env_host")
		t.Setenv(TaklerConnectFile, writeConnectConfig(t, `server:
  address:
    hostname: config_host
    port: "5678"
`))

		target, err := resolveServerTarget("flag_host", "9999")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if target.host != "flag_host" || target.port != "9999" {
			t.Errorf("target = %s:%s, want flag_host:9999", target.host, target.port)
		}
	})
}

// An unusable connect config is a configuration error of the request, reported
// as an *ExitError rather than by ending the process (requirement 15.9).
func TestResolveServerTargetWithUnusableConnectConfig(t *testing.T) {
	cases := map[string]string{
		"missing file": filepath.Join(t.TempDir(), "absent.yaml"),
		"unparseable":  writeConnectConfig(t, "server: [this is not a mapping\n"),
	}

	for name, filePath := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(TaklerConnectFile, filePath)

			_, err := resolveServerTarget("", "")

			var exitErr *common.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("error = %v, want *ExitError", err)
			}
			if exitErr.Code != common.ExitRequestError {
				t.Errorf("exit code = %d, want %d", exitErr.Code, common.ExitRequestError)
			}
			if !strings.Contains(exitErr.Message, filePath) {
				t.Errorf("message = %q, want it to name %q", exitErr.Message, filePath)
			}
		})
	}
}

// The client a command talks through carries the resolved address and is ready
// to build the Credential_Metadata of a call; building it connects to nothing,
// so an unreachable address is fine here.
func TestNewClientCarriesAddressAndCredentials(t *testing.T) {
	t.Setenv(TaklerConnectFile, writeConnectConfig(t, `server:
  address:
    hostname: config_host
    port: "5678"

security:
  ca_file: /config/ca.crt
  server_name: config_name
  operator_secret_file: /config/secret
`))

	client, err := newClient("", "")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if client.Host != "config_host" || client.Port != "5678" {
		t.Errorf("client address = %s:%s, want config_host:5678", client.Host, client.Port)
	}
	if client.Credentials() == nil {
		t.Error("credentials = nil, want the credentials built from the security levels")
	}
}
