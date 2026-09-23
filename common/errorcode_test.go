package common

import (
	"math"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
)

// This file is one half of a drift guard. Its other half is the Python test
// tests/client/test_cross_language_contract.py. Both restate the same fixed
// tables from the m2-security design's "Cross-Language Contract" section, by
// hand, so that a change made on one side without the matching change on the
// other side turns into a test failure rather than a silent protocol
// divergence.
//
// Consequently every expected value below is a literal. Nothing here may be
// derived from ErrorNameByCode, DefaultRetryWindowByKind or
// RetryableStatusCodes: a table that reads the map it is meant to police can
// never detect a wrong edit to that map.

// errorCodeContractEntry is one row of the contract's Error_Code table: the
// numeric flag carried by ServiceResponse.flag and its classification name.
type errorCodeContractEntry struct {
	code int32
	name string
}

// errorCodeContractTable transcribes the Error_Code column and the
// classification name column of the contract table, i.e. ERROR_NAME_BY_CODE in
// takler/server/protocol/error_code.py. Sixteen rows: 0, 1, 10~15, 20, 30, 31,
// 40~43, 99.
var errorCodeContractTable = []errorCodeContractEntry{
	{code: 0, name: "success"},
	{code: 1, name: "takler_error"},
	{code: 10, name: "node_not_found"},
	{code: 11, name: "invalid_node_path"},
	{code: 12, name: "node_type"},
	{code: 13, name: "unsupported_value"},
	{code: 14, name: "flow_state"},
	{code: 15, name: "invalid_request"},
	{code: 16, name: "batch_failed"},
	{code: 20, name: "expression_syntax"},
	{code: 30, name: "job_submission"},
	{code: 31, name: "zombie"},
	{code: 40, name: "transport"},
	{code: 41, name: "client_connection"},
	{code: 42, name: "server_response"},
	{code: 43, name: "permission_denied"},
	{code: 99, name: "internal_error"},
}

// errorCodeUnregisteredCodes are codes the contract table deliberately leaves
// out: the gaps between the allocated ranges, the values just past each range,
// a negative flag, and both int32 extremes. All of them must classify as
// UnknownErrorName (requirement 15.7).
var errorCodeUnregisteredCodes = []int32{
	2, 9, 17, 19, 21, 29, 32, 39, 44, 98, 100,
	-1, math.MinInt32, math.MaxInt32,
}

// errorCodeRetryWindowEntry is one row of the contract's Retry_Window defaults.
type errorCodeRetryWindowEntry struct {
	kind    CommandKind
	seconds int64
}

// errorCodeRetryWindowTable transcribes the two Retry_Window rows of the
// contract's retry constant table, i.e. DEFAULT_RETRY_WINDOW_BY_KIND in
// takler/client/retry.py: one day for a child command, one minute for the two
// interactive kinds.
var errorCodeRetryWindowTable = []errorCodeRetryWindowEntry{
	{kind: KindChild, seconds: 86400},
	{kind: KindControl, seconds: 60},
	{kind: KindQuery, seconds: 60},
}

// errorCodeRetryableStatusCodes transcribes the "可重试状态码" row of the
// contract, i.e. RETRYABLE_STATUS_CODES in takler/client/retry.py.
var errorCodeRetryableStatusCodes = []codes.Code{
	codes.Unavailable,
	codes.DeadlineExceeded,
	codes.ResourceExhausted,
	codes.Unknown,
}

// errorCodeNonRetryableStatusCodes transcribes the "不可重试状态码" row of the
// contract, i.e. the key set of NON_RETRYABLE_EXCEPTION_BY_STATUS in
// takler/client/retry.py. These four must never be retried: the request itself
// is wrong or refused, so repeating it cannot change the outcome.
var errorCodeNonRetryableStatusCodes = []codes.Code{
	codes.InvalidArgument,
	codes.NotFound,
	codes.PermissionDenied,
	codes.Unauthenticated,
}

// Feature: m2-security, Property 9: 跨语言常量一致性
// Validates: Requirements 14.14, 15.6, 16.17
//
// TestCrossLanguageContractConstants asserts that the Error_Code mapping and the
// retry constants of this package equal the fixed tables of the spec's
// "Cross-Language Contract" section, item by item and in both directions: no
// contract entry missing from the implementation, no implementation entry absent
// from the contract.
func TestCrossLanguageContractConstants(t *testing.T) {
	t.Run("error name by code has no missing key", func(t *testing.T) {
		for _, entry := range errorCodeContractTable {
			name, ok := ErrorNameByCode[entry.code]
			if !ok {
				t.Errorf("ErrorNameByCode is missing contract code %d (%s)", entry.code, entry.name)
				continue
			}
			if name != entry.name {
				t.Errorf("ErrorNameByCode[%d] = %q, want %q", entry.code, name, entry.name)
			}
		}
	})

	t.Run("error name by code has no extra key", func(t *testing.T) {
		contract := make(map[int32]string, len(errorCodeContractTable))
		for _, entry := range errorCodeContractTable {
			contract[entry.code] = entry.name
		}
		for code, name := range ErrorNameByCode {
			if _, ok := contract[code]; !ok {
				t.Errorf("ErrorNameByCode has code %d (%q) that the contract does not list", code, name)
			}
		}
		if len(ErrorNameByCode) != len(errorCodeContractTable) {
			t.Errorf("len(ErrorNameByCode) = %d, want %d", len(ErrorNameByCode), len(errorCodeContractTable))
		}
	})

	t.Run("error name returns the contract name", func(t *testing.T) {
		for _, entry := range errorCodeContractTable {
			if got := ErrorName(entry.code); got != entry.name {
				t.Errorf("ErrorName(%d) = %q, want %q", entry.code, got, entry.name)
			}
		}
	})

	t.Run("unregistered codes are unknown", func(t *testing.T) {
		if UnknownErrorName != "unknown" {
			t.Errorf("UnknownErrorName = %q, want %q", UnknownErrorName, "unknown")
		}
		for _, code := range errorCodeUnregisteredCodes {
			if got := ErrorName(code); got != "unknown" {
				t.Errorf("ErrorName(%d) = %q, want %q", code, got, "unknown")
			}
		}
	})

	t.Run("single timeout and backoff cap", func(t *testing.T) {
		if DefaultSingleTimeout != 10*time.Second {
			t.Errorf("DefaultSingleTimeout = %v, want %v", DefaultSingleTimeout, 10*time.Second)
		}
		if MaxBackoff != 60*time.Second {
			t.Errorf("MaxBackoff = %v, want %v", MaxBackoff, 60*time.Second)
		}
	})

	t.Run("retry window defaults", func(t *testing.T) {
		for _, entry := range errorCodeRetryWindowTable {
			want := time.Duration(entry.seconds) * time.Second
			got, ok := DefaultRetryWindowByKind[entry.kind]
			if !ok {
				t.Errorf("DefaultRetryWindowByKind is missing kind %v", entry.kind)
				continue
			}
			if got != want {
				t.Errorf("DefaultRetryWindowByKind[%v] = %v, want %v", entry.kind, got, want)
			}
		}
		if len(DefaultRetryWindowByKind) != len(errorCodeRetryWindowTable) {
			t.Errorf(
				"len(DefaultRetryWindowByKind) = %d, want %d",
				len(DefaultRetryWindowByKind), len(errorCodeRetryWindowTable),
			)
		}
	})

	t.Run("retry window env var name", func(t *testing.T) {
		if EnvRetryWindow != "TAKLER_TIMEOUT" {
			t.Errorf("EnvRetryWindow = %q, want %q", EnvRetryWindow, "TAKLER_TIMEOUT")
		}
	})

	t.Run("retryable status codes have no missing entry", func(t *testing.T) {
		for _, code := range errorCodeRetryableStatusCodes {
			if !RetryableStatusCodes[code] {
				t.Errorf("RetryableStatusCodes does not contain contract code %v", code)
			}
			if !IsRetryableStatus(code) {
				t.Errorf("IsRetryableStatus(%v) = false, want true", code)
			}
		}
	})

	t.Run("retryable status codes have no extra entry", func(t *testing.T) {
		contract := make(map[codes.Code]bool, len(errorCodeRetryableStatusCodes))
		for _, code := range errorCodeRetryableStatusCodes {
			contract[code] = true
		}
		registered := 0
		for code, retryable := range RetryableStatusCodes {
			if !retryable {
				continue
			}
			registered++
			if !contract[code] {
				t.Errorf("RetryableStatusCodes contains %v that the contract does not list", code)
			}
		}
		if registered != len(errorCodeRetryableStatusCodes) {
			t.Errorf("RetryableStatusCodes has %d retryable codes, want %d", registered, len(errorCodeRetryableStatusCodes))
		}
	})

	t.Run("non retryable status codes stay non retryable", func(t *testing.T) {
		for _, code := range errorCodeNonRetryableStatusCodes {
			if IsRetryableStatus(code) {
				t.Errorf("IsRetryableStatus(%v) = true, want false", code)
			}
		}
	})
}
