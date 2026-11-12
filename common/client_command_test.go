// Tests that the command methods really go through the Call_Wrapper
// (requirements 14.1, 15.9, 13.7).
//
// A local fake server returns Unavailable immediately. With a zero retry
// window, each command makes one attempt and returns the wrapper's exit error.
// An unreadable Operator_Secret_File exercises credential validation before RPC.
package common

import (
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
)

// The commands return the wrapper's error. Load gets a readable file so its
// request reaches the transport.
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
			_, err := c.RunCommandInit("/flow1/task1", "job-1", testAttemptID)
			return err
		}},
		{"complete", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandComplete("/flow1/task1", testAttemptID)
			return err
		}},
		{"abort", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandAbort("/flow1/task1", "because", testAttemptID)
			return err
		}},
		{"event", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandEvent("/flow1/task1", "ready", testAttemptID, "/flow1/task1")
			return err
		}},
		{"meter", func(c *TaklerServiceClient) error {
			_, err := c.RunCommandMeter("/flow1/task1", "step", "10", testAttemptID, "/flow1/task1")
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
		{"ping", func(c *TaklerServiceClient) error {
			_, err := c.RunQueryPing()
			return err
		}},
		{"coroutine", func(c *TaklerServiceClient) error {
			_, err := c.RunQueryCoroutine()
			return err
		}},
	}

	t.Setenv(EnvRetryWindow, "0")
	client, servicer := newTCPClient(t, fakeAlwaysFail(codes.Unavailable))
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			before := servicer.callCount()
			exitErr := retryRequireExitError(t, testCase.call(client), ExitUnreachable)
			retryAssertMessageContains(t, "the unreachable message", exitErr.Message, client.getServerAddress())
			if attempts := servicer.callCount() - before; attempts != 1 {
				t.Errorf("RPC attempts = %d, want 1", attempts)
			}
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
	client, servicer := newTCPClient(t)
	t.Setenv(EnvSecretFile, missing)

	_, err := client.RunCommandSuspend([]string{"/flow1"})
	exitErr := retryRequireExitError(t, err, ExitRequestError)
	retryAssertMessageContains(t, "the credential error", exitErr.Message, missing)
	if attempts := servicer.callCount(); attempts != 0 {
		t.Errorf("RPC attempts = %d, want 0", attempts)
	}
}
