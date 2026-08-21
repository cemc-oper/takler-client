// Error_Code classification carried by ServiceResponse.flag.
//
// flag == 0 means success and any non zero value means failure. The mapping
// below mirrors ERROR_NAME_BY_CODE in the Python package
// takler/server/protocol/error_code.py: same keys, same names. The two sides
// are kept in sync by hand, so any change here must be applied there as well.
package common

// Error_Code values that have a dedicated name in the Python implementation.
const (
	// ErrorCodeSuccess marks a command that succeeded.
	ErrorCodeSuccess int32 = 0

	// ErrorCodeGenericTaklerError is a takler owned error without a dedicated
	// Error_Code of its own.
	ErrorCodeGenericTaklerError int32 = 1

	// ErrorCodeInternalServerError is any server side failure that is not a
	// takler error.
	ErrorCodeInternalServerError int32 = 99
)

// UnknownErrorName is the placeholder name returned for codes that are not
// registered in ErrorNameByCode.
const UnknownErrorName = "unknown"

// ErrorNameByCode maps an Error_Code to its classification name. The mapping is
// injective and its key set is exactly that of the Python ERROR_NAME_BY_CODE.
var ErrorNameByCode = map[int32]string{
	ErrorCodeSuccess:             "success",
	ErrorCodeGenericTaklerError:  "takler_error",
	10:                           "node_not_found",
	11:                           "invalid_node_path",
	12:                           "node_type",
	13:                           "unsupported_value",
	14:                           "flow_state",
	15:                           "invalid_request",
	20:                           "expression_syntax",
	30:                           "job_submission",
	31:                           "zombie",
	40:                           "transport",
	41:                           "client_connection",
	42:                           "server_response",
	43:                           "permission_denied",
	ErrorCodeInternalServerError: "internal_error",
}

// ErrorName returns the classification name of code, or UnknownErrorName when
// the code is not registered. It never panics.
func ErrorName(code int32) string {
	if name, ok := ErrorNameByCode[code]; ok {
		return name
	}
	return UnknownErrorName
}
