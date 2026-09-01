package cmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cemc-oper/takler-client/common"
)

// The four exit codes a command can end with must survive the trip through
// Execute's error handling unchanged (requirements 15.1 ~ 15.5).
func TestExitStatusForExitError(t *testing.T) {
	for _, testCase := range []struct {
		name string
		code int
	}{
		{"success", common.ExitOK},
		{"request error", common.ExitRequestError},
		{"server error", common.ExitServerError},
		{"unreachable", common.ExitUnreachable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := common.NewExitError(testCase.code, "boom")

			code, message := exitStatusForError(err)

			if code != testCase.code {
				t.Errorf("exit code: got %d, want %d", code, testCase.code)
			}
			if message != "boom" {
				t.Errorf("message: got %q, want %q", message, "boom")
			}
		})
	}
}

// An *ExitError wrapped by another error must still decide the exit code:
// Execute uses errors.As, not a type assertion (requirement 15.9).
func TestExitStatusForWrappedExitError(t *testing.T) {
	err := fmt.Errorf("while running command: %w", common.NewExitError(common.ExitUnreachable, "no server"))

	code, message := exitStatusForError(err)

	if code != common.ExitUnreachable {
		t.Errorf("exit code: got %d, want %d", code, common.ExitUnreachable)
	}
	if message != "no server" {
		t.Errorf("message: got %q, want %q", message, "no server")
	}
}

// An error that is not an *ExitError, such as cobra's own argument or flag
// error, is the most conservative failure (requirement 15.3).
func TestExitStatusForPlainError(t *testing.T) {
	code, message := exitStatusForError(errors.New("unknown flag: --nope"))

	if code != common.ExitServerError {
		t.Errorf("exit code: got %d, want %d", code, common.ExitServerError)
	}
	if message != "unknown flag: --nope" {
		t.Errorf("message: got %q", message)
	}
}

// Execute silences cobra's own error and usage output so that a failing command
// writes exactly one line to standard error.
func TestRootCommandSilencesCobraOutput(t *testing.T) {
	rootCmd := newCommandsBuilder().addAll().build().getCommand()

	if !rootCmd.SilenceErrors || !rootCmd.SilenceUsage {
		t.Fatal("root command must silence both cobra error and usage output")
	}
}
