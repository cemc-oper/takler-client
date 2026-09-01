// Tests of the eleven command implementations' own RunE bodies, i.e. of what
// each command does around the one common call it makes.
//
// No test here talks to a server: the two observable outcomes of a command that
// never reaches the network are enough to exercise the whole body. A connect
// config that cannot be loaded fails while the client is being built, and an
// unusable CA certificate file fails inside the first call, before any
// connection is created (common.BuildTransportCredentials runs ahead of
// grpc.NewClient). Both must come back as the *ExitError the cmd layer promises
// (requirements 15.9, 13.12).
package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/perillaroc/takler-client/common"
)

// commandRunners returns every command's RunE, keyed by the subcommand name.
//
// The child commands carry a node path so that their diagnostics line is the
// one an operator sees, and the ones with required flags carry a value for
// them: the flags are bound to the command struct, so a direct RunE call has to
// fill them itself.
func commandRunners() map[string]func() error {
	return map[string]func() error{
		"init": func() error {
			c := newInitCommand()
			c.childOptions.nodePath = "/flow1/task1"
			c.taskId = "12345"
			return c.runCommand(nil, nil)
		},
		"complete": func() error {
			c := newCompleteCommand()
			c.childOptions.nodePath = "/flow1/task1"
			return c.runCommand(nil, nil)
		},
		"abort": func() error {
			c := newAbortCommand()
			c.childOptions.nodePath = "/flow1/task1"
			c.reason = "job failed"
			return c.runCommand(nil, nil)
		},
		"event": func() error {
			c := newEventCommand()
			c.childOptions.nodePath = "/flow1/task1"
			c.eventName = "ready"
			return c.runCommand(nil, nil)
		},
		"meter": func() error {
			c := newMeterCommand()
			c.childOptions.nodePath = "/flow1/task1"
			c.meterName = "progress"
			c.meterValue = "50"
			return c.runCommand(nil, nil)
		},
		"requeue": func() error {
			return newRequeueCommand().runCommand(nil, []string{"/flow1/task1"})
		},
		"suspend": func() error {
			return newSuspendCommand().runCommand(nil, []string{"/flow1"})
		},
		"resume": func() error {
			return newResumeCommand().runCommand(nil, []string{"/flow1"})
		},
		"run": func() error {
			c := newRunCommand()
			c.force = true
			return c.runCommand(nil, []string{"/flow1/task1"})
		},
		"show": func() error {
			return newShowCommand().runCommand(nil, nil)
		},
		"ping": func() error {
			return newPingCommand().runCommand(nil, nil)
		},
	}
}

// clearNoTakler makes sure the NO_TAKLER short circuit is not in effect, so a
// child command runs its whole body. t.Setenv records the variable's original
// state, including its absence, and the cleanup restores it.
func clearNoTakler(t *testing.T) {
	t.Helper()

	t.Setenv(NoTakler, "")
	if err := os.Unsetenv(NoTakler); err != nil {
		t.Fatalf("unset %s: %v", NoTakler, err)
	}
}

// An unusable CA certificate file is a configuration error of the request: every
// command must report it as an *ExitError with ExitRequestError, and must have
// printed its own diagnostics line first, which is what says the failure came
// from the call rather than from building the client (requirements 13.12, 15.9).
func TestEveryCommandReportsUnusableCaCertificate(t *testing.T) {
	for name, run := range commandRunners() {
		t.Run(name, func(t *testing.T) {
			clearNoTakler(t)
			caFile := filepath.Join(t.TempDir(), "absent-ca.crt")
			t.Setenv(common.TaklerTlsCaFile, caFile)
			t.Setenv(TaklerHost, "test_host")
			t.Setenv(TaklerPort, "4321")

			var err error
			output := captureStdout(t, func() { err = run() })

			var exitErr *common.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("error = %v, want *ExitError", err)
			}
			if exitErr.Code != common.ExitRequestError {
				t.Errorf("exit code = %d, want %d", exitErr.Code, common.ExitRequestError)
			}
			if !strings.Contains(exitErr.Message, caFile) {
				t.Errorf("message = %q, want it to name %q", exitErr.Message, caFile)
			}
			if !strings.Contains(output, "test_host:4321") {
				t.Errorf("output = %q, want it to name the server the command talked to", output)
			}
		})
	}
}

// A connect config that cannot be loaded fails while the client is being built,
// which every command must report rather than continue past (requirement 15.9).
func TestEveryCommandReportsUnusableConnectConfig(t *testing.T) {
	for name, run := range commandRunners() {
		t.Run(name, func(t *testing.T) {
			clearNoTakler(t)
			connectFile := filepath.Join(t.TempDir(), "absent.yaml")
			t.Setenv(TaklerConnectFile, connectFile)

			var err error
			output := captureStdout(t, func() { err = run() })

			var exitErr *common.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("error = %v, want *ExitError", err)
			}
			if exitErr.Code != common.ExitRequestError {
				t.Errorf("exit code = %d, want %d", exitErr.Code, common.ExitRequestError)
			}
			if !strings.Contains(exitErr.Message, connectFile) {
				t.Errorf("message = %q, want it to name %q", exitErr.Message, connectFile)
			}
			if output != "" {
				t.Errorf("stdout = %q, want nothing before the client exists", output)
			}
		})
	}
}

// A child command takes its node path from TAKLER_NAME when --node-path was not
// given, which is the M1 behaviour a job script relies on: the printed line, and
// with it the request, must carry the environment's node path.
func TestChildCommandUsesNodePathFromEnvironment(t *testing.T) {
	clearNoTakler(t)
	t.Setenv(TaklerName, "/flow1/family1/task1")
	t.Setenv(common.TaklerTlsCaFile, filepath.Join(t.TempDir(), "absent-ca.crt"))

	c := newCompleteCommand()

	var err error
	output := captureStdout(t, func() { err = c.runCommand(nil, nil) })

	if err == nil {
		t.Fatal("error = nil, want the unusable CA certificate error")
	}
	if !strings.Contains(output, "/flow1/family1/task1") {
		t.Errorf("output = %q, want it to name the node path from %s", output, TaklerName)
	}
}

// --show-all turns on every show option, overriding the individual ones
// (including the two that default to false).
func TestShowAllTurnsOnEveryShowOption(t *testing.T) {
	t.Setenv(common.TaklerTlsCaFile, filepath.Join(t.TempDir(), "absent-ca.crt"))

	c := newShowCommand()
	c.showAll = true

	var err error
	captureStdout(t, func() { err = c.runCommand(nil, nil) })

	if err == nil {
		t.Fatal("error = nil, want the unusable CA certificate error")
	}
	if !c.showTrigger || !c.showParameter || !c.showLimit || !c.showEvent || !c.showMeter {
		t.Errorf(
			"show options = trigger:%t parameter:%t limit:%t event:%t meter:%t, want all true",
			c.showTrigger, c.showParameter, c.showLimit, c.showEvent, c.showMeter,
		)
	}
}

// getNodePath falls back from the --node-path option to TAKLER_NAME and then to
// an empty string, which lets the server reject the request rather than the
// client guessing a node.
func TestGetNodePath(t *testing.T) {
	t.Run("option wins", func(t *testing.T) {
		t.Setenv(TaklerName, "/env/task")

		if got, want := getNodePath("/option/task"), "/option/task"; got != want {
			t.Errorf("node path = %q, want %q", got, want)
		}
	})

	t.Run("environment", func(t *testing.T) {
		t.Setenv(TaklerName, "/env/task")

		if got, want := getNodePath(""), "/env/task"; got != want {
			t.Errorf("node path = %q, want %q", got, want)
		}
	})

	t.Run("neither", func(t *testing.T) {
		t.Setenv(TaklerName, "")

		if got := getNodePath(""); got != "" {
			t.Errorf("node path = %q, want empty", got)
		}
	})
}

// getHost and getPort are the deprecated single value resolutions, kept until
// their callers are gone. They must agree with resolveServerTarget on the two
// levels they do know about, or a caller that still uses them would reach a
// different server than the rest of the client.
func TestDeprecatedGetHostAndGetPort(t *testing.T) {
	t.Run("option wins", func(t *testing.T) {
		t.Setenv(TaklerHost, "env_host")
		t.Setenv(TaklerPort, "1234")

		if got, want := getHost("option_host"), "option_host"; got != want {
			t.Errorf("host = %q, want %q", got, want)
		}
		if got, want := getPort("9999"), "9999"; got != want {
			t.Errorf("port = %q, want %q", got, want)
		}
	})

	t.Run("environment", func(t *testing.T) {
		t.Setenv(TaklerHost, "env_host")
		t.Setenv(TaklerPort, "1234")

		if got, want := getHost(""), "env_host"; got != want {
			t.Errorf("host = %q, want %q", got, want)
		}
		if got, want := getPort(""), "1234"; got != want {
			t.Errorf("port = %q, want %q", got, want)
		}
	})

	t.Run("defaults", func(t *testing.T) {
		t.Setenv(TaklerHost, "")
		t.Setenv(TaklerPort, "")

		if got, want := getHost(""), DefaultHost; got != want {
			t.Errorf("host = %q, want %q", got, want)
		}
		if got, want := getPort(""), DefaultPort; got != want {
			t.Errorf("port = %q, want %q", got, want)
		}
	})
}
