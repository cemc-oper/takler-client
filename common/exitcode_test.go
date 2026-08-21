package common

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
)

// exitCodeErrorCodeCase is one row of the fixed Error_Code -> exit code table
// this test asserts against. The table is written out by hand instead of being
// derived from ExitCodeByErrorCode, so a wrong edit to the map is caught here
// rather than mirrored by the test.
type exitCodeErrorCodeCase struct {
	name     string
	code     int32
	expected int
}

// exitCodeErrorCodeTable restates the last column of the design's Error_Code
// table, i.e. EXIT_CODE_BY_ERROR_CODE in takler/client/exit_code.py. Every key
// of ExitCodeByErrorCode must appear here exactly once.
var exitCodeErrorCodeTable = []exitCodeErrorCodeCase{
	{name: "success", code: 0, expected: ExitOK},
	{name: "takler_error", code: 1, expected: ExitRequestError},
	{name: "node_not_found", code: 10, expected: ExitRequestError},
	{name: "invalid_node_path", code: 11, expected: ExitRequestError},
	{name: "node_type", code: 12, expected: ExitRequestError},
	{name: "unsupported_value", code: 13, expected: ExitRequestError},
	{name: "flow_state", code: 14, expected: ExitRequestError},
	{name: "invalid_request", code: 15, expected: ExitRequestError},
	{name: "expression_syntax", code: 20, expected: ExitRequestError},
	{name: "job_submission", code: 30, expected: ExitServerError},
	{name: "zombie", code: 31, expected: ExitServerError},
	{name: "transport", code: 40, expected: ExitUnreachable},
	{name: "client_connection", code: 41, expected: ExitUnreachable},
	{name: "server_response", code: 42, expected: ExitServerError},
	{name: "permission_denied", code: 43, expected: ExitRequestError},
	{name: "internal_error", code: 99, expected: ExitServerError},
}

// TestExitCodeConstants pins the four exit code values themselves: a job script
// reads these numbers, so they are part of the contract and cannot change
// (requirements 15.1 ~ 15.4).
func TestExitCodeConstants(t *testing.T) {
	cases := []struct {
		name     string
		actual   int
		expected int
	}{
		{name: "ok", actual: ExitOK, expected: 0},
		{name: "request error", actual: ExitRequestError, expected: 1},
		{name: "server error", actual: ExitServerError, expected: 3},
		{name: "unreachable", actual: ExitUnreachable, expected: 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.actual != c.expected {
				t.Errorf("exit code %s = %d, want %d", c.name, c.actual, c.expected)
			}
		})
	}
}

// TestExitCodeForErrorCodeRegistered walks the whole fixed table and asserts the
// exit code of each registered Error_Code, covering the success, request error,
// server error and unreachable outcomes (requirements 15.1 ~ 15.3, 16.15).
func TestExitCodeForErrorCodeRegistered(t *testing.T) {
	for _, c := range exitCodeErrorCodeTable {
		t.Run(c.name, func(t *testing.T) {
			if got := ExitCodeForErrorCode(c.code); got != c.expected {
				t.Errorf("ExitCodeForErrorCode(%d) = %d, want %d", c.code, got, c.expected)
			}
			mapped, ok := ExitCodeByErrorCode[c.code]
			if !ok {
				t.Fatalf("ExitCodeByErrorCode has no entry for code %d", c.code)
			}
			if mapped != c.expected {
				t.Errorf("ExitCodeByErrorCode[%d] = %d, want %d", c.code, mapped, c.expected)
			}
		})
	}
}

// TestExitCodeByErrorCodeKeySet asserts the map carries no entry beyond the
// fixed table, so an extra or renamed key cannot slip past the test above
// (requirement 16.15).
func TestExitCodeByErrorCodeKeySet(t *testing.T) {
	expected := make(map[int32]bool, len(exitCodeErrorCodeTable))
	for _, c := range exitCodeErrorCodeTable {
		if expected[c.code] {
			t.Fatalf("code %d listed twice in the test table", c.code)
		}
		expected[c.code] = true
	}
	if len(ExitCodeByErrorCode) != len(expected) {
		t.Errorf("ExitCodeByErrorCode has %d entries, want %d", len(ExitCodeByErrorCode), len(expected))
	}
	for code := range ExitCodeByErrorCode {
		if !expected[code] {
			t.Errorf("ExitCodeByErrorCode has unexpected entry for code %d", code)
		}
	}
}

// TestExitCodeForErrorCodeZombie states the zombie case on its own: a
// Child_Command rejected by the Zombie_Detector under the fail policy must end
// the job script with exit code 3 (requirements 15.3, 16.15).
func TestExitCodeForErrorCodeZombie(t *testing.T) {
	const zombieErrorCode int32 = 31
	if got := ExitCodeForErrorCode(zombieErrorCode); got != ExitServerError {
		t.Errorf("ExitCodeForErrorCode(%d) = %d, want %d for zombie", zombieErrorCode, got, ExitServerError)
	}
}

// TestExitCodeForErrorCodePermissionDenied states the auth refusal case on its
// own: a request the server's auth interceptor turned down is a wrong request,
// not a server failure (requirement 15.5).
func TestExitCodeForErrorCodePermissionDenied(t *testing.T) {
	const permissionDeniedErrorCode int32 = 43
	if got := ExitCodeForErrorCode(permissionDeniedErrorCode); got != ExitRequestError {
		t.Errorf(
			"ExitCodeForErrorCode(%d) = %d, want %d for permission_denied",
			permissionDeniedErrorCode, got, ExitRequestError,
		)
	}
}

// TestExitCodeForErrorCodeUnregistered asserts an unknown non zero code falls
// back to the conservative reading "the server side went wrong" instead of
// being reported as a bad request or, worse, as success (requirement 15.3).
func TestExitCodeForErrorCodeUnregistered(t *testing.T) {
	cases := []struct {
		name string
		code int32
	}{
		{name: "small gap", code: 7},
		{name: "between families", code: 55},
		{name: "far above the table", code: 1000},
		{name: "negative", code: -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, registered := ExitCodeByErrorCode[c.code]; registered {
				t.Fatalf("code %d is registered, pick an unregistered one for this case", c.code)
			}
			if got := ExitCodeForErrorCode(c.code); got != ExitServerError {
				t.Errorf("ExitCodeForErrorCode(%d) = %d, want %d", c.code, got, ExitServerError)
			}
		})
	}
}

// TestExitCodeForStatus covers the two outcomes of a failed call, i.e. of a call
// that never produced a ServiceResponse: the four non retryable status codes are
// request errors (requirement 15.5) and every other status code is retried until
// the Retry_Window is exhausted, which ends as unreachable (requirement 15.4).
// codes.OK is not a failure and so is not covered here.
func TestExitCodeForStatus(t *testing.T) {
	cases := []struct {
		name     string
		code     codes.Code
		expected int
	}{
		{name: "invalid argument", code: codes.InvalidArgument, expected: ExitRequestError},
		{name: "not found", code: codes.NotFound, expected: ExitRequestError},
		{name: "permission denied", code: codes.PermissionDenied, expected: ExitRequestError},
		{name: "unauthenticated", code: codes.Unauthenticated, expected: ExitRequestError},
		{name: "unavailable", code: codes.Unavailable, expected: ExitUnreachable},
		{name: "deadline exceeded", code: codes.DeadlineExceeded, expected: ExitUnreachable},
		{name: "resource exhausted", code: codes.ResourceExhausted, expected: ExitUnreachable},
		{name: "unknown", code: codes.Unknown, expected: ExitUnreachable},
		{name: "internal", code: codes.Internal, expected: ExitUnreachable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExitCodeForStatus(c.code); got != c.expected {
				t.Errorf("ExitCodeForStatus(%s) = %d, want %d", c.code, got, c.expected)
			}
		})
	}
}

// TestExitCodeForStatusOKIsUndefinedInput records what the function does today
// for codes.OK. codes.OK violates the function's precondition, which is that only
// a failed call reaches it, so the value below is pinned for one reason only: the
// conservative fallback must not change silently. It is not part of the
// cross-language contract and has no Python counterpart. Turning codes.OK into a
// panic, or into ExitServerError, would be a legitimate choice, so this
// assertion must not be read as a contract guard.
func TestExitCodeForStatusOKIsUndefinedInput(t *testing.T) {
	if got := ExitCodeForStatus(codes.OK); got != ExitUnreachable {
		t.Errorf("ExitCodeForStatus(%s) = %d, want %d", codes.OK, got, ExitUnreachable)
	}
}

// TestExitCodeErrorIsError asserts *ExitError travels the call stack as a plain
// error and that its text is the single line meant for standard error, which is
// what lets commands return it instead of calling log.Fatalf (requirement 15.9).
func TestExitCodeErrorIsError(t *testing.T) {
	const message = "zombie: /flow1/task1 complete refused"
	exitErr := NewExitError(ExitServerError, message)

	if exitErr.Code != ExitServerError {
		t.Errorf("NewExitError code = %d, want %d", exitErr.Code, ExitServerError)
	}
	if exitErr.Message != message {
		t.Errorf("NewExitError message = %q, want %q", exitErr.Message, message)
	}

	var err error = exitErr
	if err.Error() != message {
		t.Errorf("Error() = %q, want %q", err.Error(), message)
	}

	var target *ExitError
	if !errors.As(err, &target) {
		t.Fatal("errors.As did not recover the *ExitError")
	}
	if target.Code != ExitServerError {
		t.Errorf("recovered code = %d, want %d", target.Code, ExitServerError)
	}
}
