package common

import "google.golang.org/grpc/codes"

// Process exit codes of the Go client (requirements 15.1 ~ 15.5). A client
// command runs inside a job script that uses "set -e", so the exit code is the
// only failure signal the script can act on. The four values, their meaning and
// their mapping are the same as the Python client's takler/client/exit_code.py,
// so one job script can call either client and read the result the same way.
const (
	// ExitOK means the command succeeded (requirement 15.1).
	ExitOK = 0

	// ExitRequestError means the request itself was not acceptable: unknown
	// node, malformed node path, unsupported value, unparseable expression, or
	// a refusal by the server's auth interceptor (requirements 15.2, 15.5).
	ExitRequestError = 1

	// ExitServerError means the server failed while executing the command, or
	// answered something the client cannot use (requirement 15.3).
	ExitServerError = 3

	// ExitUnreachable means the Retry_Window was exhausted without reaching the
	// server (requirement 15.4).
	ExitUnreachable = 4
)

// ExitError carries the exit code a failed command must end the process with,
// together with the single line to write to standard error. Commands return it
// up the call stack and Execute turns it into os.Exit, so no code on the
// command path calls log.Fatalf (requirement 15.9).
type ExitError struct {
	Code    int
	Message string
}

// Error implements the error interface.
func (e *ExitError) Error() string { return e.Message }

// NewExitError returns an *ExitError with the given exit code and message.
func NewExitError(code int, message string) *ExitError {
	return &ExitError{Code: code, Message: message}
}

// ExitCodeByErrorCode maps an Error_Code, i.e. the value of
// ServiceResponse.flag, to the process exit code. The keys and values mirror
// EXIT_CODE_BY_ERROR_CODE in the Python client's takler/client/exit_code.py
// exactly; the comment on each entry is the Error_Code's classification name
// (requirement 15.6). The key type is int32 because that is the type of
// ServiceResponse.flag in the protocol.
var ExitCodeByErrorCode = map[int32]int{
	0:  ExitOK,           // success
	1:  ExitRequestError, // takler_error
	10: ExitRequestError, // node_not_found
	11: ExitRequestError, // invalid_node_path
	12: ExitRequestError, // node_type
	13: ExitRequestError, // unsupported_value
	14: ExitRequestError, // flow_state
	15: ExitRequestError, // invalid_request
	20: ExitRequestError, // expression_syntax
	30: ExitServerError,  // job_submission
	31: ExitServerError,  // zombie
	40: ExitUnreachable,  // transport
	41: ExitUnreachable,  // client_connection
	42: ExitServerError,  // server_response
	43: ExitRequestError, // permission_denied
	99: ExitServerError,  // internal_error
}

// ExitCodeForErrorCode returns the process exit code for the Error_Code code.
//
// An unregistered non zero code is treated as the most conservative failure,
// ExitServerError: the server reported some failure this client build does not
// know about, so the safe reading is "the server side went wrong", not "your
// request was wrong" and not "success".
func ExitCodeForErrorCode(code int32) int {
	if exitCode, ok := ExitCodeByErrorCode[code]; ok {
		return exitCode
	}
	return ExitServerError
}

// ExitCodeForStatus returns the process exit code for a gRPC status code, i.e.
// for a call that never produced a ServiceResponse.
//
// The four codes that mean "the request itself is wrong, retrying cannot help"
// exit with ExitRequestError; this is the same set as the Python client's
// NON_RETRYABLE_EXCEPTION_BY_STATUS, whose exceptions all map to exit code 1,
// and it covers the auth interceptor's refusals (requirement 15.5). Every other
// status code is a transport level failure that the Call_Wrapper retries until
// the Retry_Window is exhausted, which ends as unreachable (requirement 15.4).
func ExitCodeForStatus(code codes.Code) int {
	switch code {
	case codes.InvalidArgument, codes.NotFound, codes.PermissionDenied, codes.Unauthenticated:
		return ExitRequestError
	default:
		return ExitUnreachable
	}
}
