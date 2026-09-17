// Tests that the command methods really go through the Call_Wrapper
// (requirements 14.1, 15.9, 13.7).
//
// The point of task 14.3 is negative: no method has a hardcoded timeout, none
// ends the process on failure, and none carries credential code. The first two
// are observable from any method without a server at all -- a closed port plus
// TAKLER_TIMEOUT=0 makes a call fail once and return -- and the third is
// observable through the one credential failure that is a hard error, an
// unreadable Operator_Secret_File, which Call surfaces before it touches the
// network.
//
// Behaviour of the loop itself (backoff, classification, flag pass through) is
// asserted against the in-process server by the Call_Wrapper's own tests; here
// the subject is only that the methods are wired to it.
package common

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unreachableAddress is a closed port on the loopback interface. Port 1 is
// privileged, so nothing a test environment runs listens there, and connecting
// to it fails immediately rather than after a network timeout.
const unreachableHost, unreachablePort = "127.0.0.1", "1"

// newUnreachableClient returns a client pointed at a closed port, with a zero
// Retry_Window so that a failing call gives up after its first attempt.
func newUnreachableClient(t *testing.T) *TaklerServiceClient {
	t.Helper()
	t.Setenv(EnvRetryWindow, "0")
	t.Setenv(TaklerTlsCaFile, "")
	t.Setenv(EnvSecretFile, "")
	return NewTaklerServiceClient(unreachableHost, unreachablePort, SecurityLevels{})
}

// assertUnreachable checks that a call against the closed port came back as the
// Retry_Window-exhausted error instead of ending the process (requirement 15.9)
// or blocking on a hardcoded timeout.
func assertUnreachable(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("call against a closed port succeeded")
	}

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error is %T, want *ExitError", err)
	}
	if exitErr.Code != ExitUnreachable {
		t.Errorf("exit code = %d, want %d", exitErr.Code, ExitUnreachable)
	}
	address := unreachableHost + ":" + unreachablePort
	if !strings.Contains(exitErr.Message, address) {
		t.Errorf("message %q does not name the server address %q", exitErr.Message, address)
	}
}

// Every one of the sixteen methods returns the Call_Wrapper's error rather
// than dying, and does so without waiting on a per-method timeout of its own.
//
// load is the exception that proves the setup: it reads its flow file before
// dialling, so the table hands it a real file -- with a missing one the
// failure would be the file's, not the unreachable server's.
func TestCommandMethodsReturnTheCallWrapperError(t *testing.T) {
	flowFile := filepath.Join(t.TempDir(), "flow1.json")
	if err := os.WriteFile(flowFile, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write flow file: %v", err)
	}

	cases := []struct {
		name string
		call func(*TaklerServiceClient) error
	}{
		{"init", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandInit("/flow1/task1", "job-1")
			return err
		}},
		{"complete", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandComplete("/flow1/task1")
			return err
		}},
		{"abort", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandAbort("/flow1/task1", "because")
			return err
		}},
		{"event", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandEvent("/flow1/task1", "ready")
			return err
		}},
		{"meter", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandMeter("/flow1/task1", "step", "10")
			return err
		}},
		{"requeue", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandRequeue([]string{"/flow1"})
			return err
		}},
		{"suspend", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandSuspend([]string{"/flow1"})
			return err
		}},
		{"resume", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandResume([]string{"/flow1"})
			return err
		}},
		{"run", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandRun([]string{"/flow1"}, true)
			return err
		}},
		{"force", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandForce([]string{"/flow1"}, "complete", true)
			return err
		}},
		{"free-dep", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandFreeDep([]string{"/flow1"}, "all")
			return err
		}},
		{"load", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandLoad("json", flowFile)
			return err
		}},
		{"begin", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandBegin("flow1", false)
			return err
		}},
		{"show", func(c *TaklerServiceClient) error {
			_, err := c.RunQueryShow(true, true, true, true, true)
			return err
		}},
		{"ping", func(c *TaklerServiceClient) error {
			_, err := c.RunQueryPing()
			return err
		}},
		{"coroutine", func(c *TaklerServiceClient) error {
			_, err := c.RunQueryCoroutine()
			return err
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := newUnreachableClient(t)
			assertUnreachable(t, testCase.call(client))
		})
	}
}

// An unreadable Operator_Secret_File is a credential failure of the request, and
// it surfaces from a control method although that method contains no credential
// code at all: Call assembles the metadata before the first attempt
// (requirements 13.7, 13.12).
func TestControlMethodSurfacesCredentialFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent-secret")
	t.Setenv(EnvRetryWindow, "0")
	t.Setenv(TaklerTlsCaFile, "")
	t.Setenv(EnvSecretFile, missing)

	client := NewTaklerServiceClient(unreachableHost, unreachablePort, SecurityLevels{})

	_, err := client.RunCommandSuspend([]string{"/flow1"})
	if err == nil {
		t.Fatal("suspend succeeded, want a secret file failure")
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
