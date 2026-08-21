// Retry policy and time sources for the Go Call_Wrapper.
//
// This file holds everything the client needs to decide *whether* and *how
// long* to wait before retrying an RPC, deliberately separated from the RPC
// plumbing itself:
//
//   - the command classification (CommandKind) that selects the default retry
//     window,
//   - the gRPC status code classification (RetryableStatusCodes),
//   - the backoff schedule (backoffSeconds),
//   - the TAKLER_TIMEOUT resolution (resolveRetryWindow),
//   - and the window bookkeeping (RetryPolicy).
//
// Every constant and every classification mirrors the Python client's
// takler/client/retry.py, which is the reference implementation of the
// cross-language contract (requirement 14.14). Any change here must be applied
// there as well.
//
// Requirements: 14.2, 14.3, 14.4, 14.9, 14.10, 14.11, 14.12, 14.14.
package common

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"google.golang.org/grpc/codes"
)

// CommandKind is how a client call is classified for retry purposes.
//
// The classification only selects the default Retry_Window: a child command
// runs inside a job script, where losing a status update desynchronizes the
// server from reality, so it keeps retrying for a day (requirement 14.10). An
// interactive control or query command must not hang an operator's terminal, so
// it gives up after a minute (requirement 14.11).
type CommandKind int

const (
	// KindChild classifies a Child_Command, i.e. a call made by a job script.
	KindChild CommandKind = iota

	// KindControl classifies a Control_Command, i.e. an operator mutation.
	KindControl

	// KindQuery classifies a Query_Command, i.e. an operator read.
	KindQuery
)

// String returns the same lowercase name the Python CommandKind enum uses, so
// the two clients' diagnostics read alike.
func (k CommandKind) String() string {
	switch k {
	case KindChild:
		return "child"
	case KindControl:
		return "control"
	case KindQuery:
		return "query"
	default:
		return "unknown"
	}
}

// EnvRetryWindow is the environment variable holding the Retry_Window in
// seconds (requirement 14.9).
const EnvRetryWindow = "TAKLER_TIMEOUT"

// DefaultSingleTimeout is the per-call deadline used when none is configured
// (requirement 14.2). Every RPC carries it, so even a wedged TCP connection
// turns into a DEADLINE_EXCEEDED and enters the retry loop instead of blocking
// forever. Mirrors DEFAULT_SINGLE_TIMEOUT = 10.0 in retry.py.
const DefaultSingleTimeout = 10 * time.Second

// MaxBackoff is the upper bound of the exponential backoff (requirement 14.4).
// Mirrors MAX_BACKOFF_SECONDS = 60.0 in retry.py.
const MaxBackoff = 60 * time.Second

// DefaultRetryWindowByKind is the default Retry_Window per command kind
// (requirements 14.10, 14.11). Mirrors DEFAULT_RETRY_WINDOW_BY_KIND in
// retry.py: one day for child commands, one minute for everything else.
var DefaultRetryWindowByKind = map[CommandKind]time.Duration{
	KindChild:   86400 * time.Second,
	KindControl: 60 * time.Second,
	KindQuery:   60 * time.Second,
}

// RetryableStatusCodes are the gRPC status codes that mean "transport level
// failure, worth retrying" (requirement 14.3). Mirrors RETRYABLE_STATUS_CODES
// in retry.py. The complementary set, the four codes that mean "the request
// itself is wrong, retrying cannot help", lives in exitcode.go's
// ExitCodeForStatus.
var RetryableStatusCodes = map[codes.Code]bool{
	codes.Unavailable:       true,
	codes.DeadlineExceeded:  true,
	codes.ResourceExhausted: true,
	codes.Unknown:           true,
}

// IsRetryableStatus reports whether a call that failed with code may be retried.
func IsRetryableStatus(code codes.Code) bool {
	return RetryableStatusCodes[code]
}

// A non-negative integer, and nothing else. [0-9] instead of \d on purpose: \d
// in Go's regexp is ASCII only already, but spelling the class out keeps this
// pattern and the Python one literally identical.
var nonNegativeIntPattern = regexp.MustCompile(`^[0-9]+$`)

// backoffSeconds returns the wait before retry number attempt.
//
// It implements min(2^(attempt-1), 60) seconds (requirement 14.4): 1, 2, 4, 8,
// 16, 32, then 60 for every later retry. attempt is 1-based; a value below 1 is
// clamped to 1 rather than shifting by a negative amount, because the only
// caller is a retry loop that starts counting at 1.
func backoffSeconds(attempt int) time.Duration {
	// 2^6 == 64 already exceeds the cap, so short-circuit instead of shifting
	// by a large amount for a long lived child command.
	if attempt >= 7 {
		return MaxBackoff
	}
	if attempt < 1 {
		return 1 * time.Second
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}

// resolveRetryWindow resolves the Retry_Window for kind from the environment.
//
// TAKLER_TIMEOUT wins when it holds a non-negative integer string (requirement
// 14.9). When it is unset, the default for the command kind applies with no
// output at all (requirements 14.10, 14.11). When it is set but empty,
// whitespace only, or not parseable as a non-negative integer, a single line
// naming the offending value goes to standard error and the same default
// applies (requirement 14.12).
//
// A window of 0 is a legitimate result and means "one attempt, no retry"
// (requirement 14.13).
func resolveRetryWindow(kind CommandKind) time.Duration {
	return resolveRetryWindowFrom(kind, os.LookupEnv, os.Stderr)
}

// resolveRetryWindowFrom is resolveRetryWindow with the environment and the
// warning sink injected, so tests can exercise the four cases of requirement
// 16.14 without touching the process environment or the real standard error.
func resolveRetryWindowFrom(
	kind CommandKind,
	lookupEnv func(string) (string, bool),
	warn io.Writer,
) time.Duration {
	defaultWindow, ok := DefaultRetryWindowByKind[kind]
	if !ok {
		// An unknown kind is a programming error, not a user error. Fall back to
		// the interactive window: a caller that never returns is worse than one
		// that gives up after a minute.
		defaultWindow = DefaultRetryWindowByKind[KindQuery]
	}

	raw, found := lookupEnv(EnvRetryWindow)
	if !found {
		return defaultWindow
	}

	text := trimASCIISpace(raw)
	if nonNegativeIntPattern.MatchString(text) {
		seconds, err := time.ParseDuration(text + "s")
		if err == nil {
			return seconds
		}
		// Digits only yet unparseable means the value overflows a Duration,
		// i.e. more than roughly 292 years. Report it like any other bad value.
	}

	fmt.Fprintf(
		warn,
		"invalid %s value %q; falling back to %v for %v commands.\n",
		EnvRetryWindow, raw, defaultWindow, kind,
	)
	return defaultWindow
}

// trimASCIISpace trims the same characters Python's str.strip() removes from an
// ASCII string: space, tab, newline, carriage return, vertical tab and form
// feed.
func trimASCIISpace(s string) string {
	start := 0
	for start < len(s) && isASCIISpace(s[start]) {
		start++
	}
	end := len(s)
	for end > start && isASCIISpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isASCIISpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

// RetryPolicy is the Retry_Window bookkeeping, with time and sleep injectable.
//
// The policy owns no RPC knowledge: the Call_Wrapper decides which failures are
// retryable and asks NextDelay how long to wait, or whether the window is over.
//
// now and sleep are the two injection points that let a test span an 86400
// second window in microseconds with a fake clock, the same role the Python
// RetryPolicy's clock and sleep fields play. Both may be nil: the zero value of
// RetryPolicy is usable and falls back to time.Now and time.Sleep.
type RetryPolicy struct {
	// RetryWindow is the total time budget for one logical call, counted from
	// the first attempt.
	RetryWindow time.Duration

	// SingleTimeout is the per-attempt deadline handed to gRPC (requirement
	// 14.2). Zero means DefaultSingleTimeout.
	SingleTimeout time.Duration

	// now is the time source. nil means time.Now.
	now func() time.Time

	// sleep is the blocking sleep. nil means time.Sleep.
	sleep func(time.Duration)
}

// NewRetryPolicy returns the policy for kind: the Retry_Window resolved from
// TAKLER_TIMEOUT, the default single timeout, and the real clock and sleep.
func NewRetryPolicy(kind CommandKind) *RetryPolicy {
	return &RetryPolicy{
		RetryWindow:   resolveRetryWindow(kind),
		SingleTimeout: DefaultSingleTimeout,
		now:           time.Now,
		sleep:         time.Sleep,
	}
}

// Now returns the current time from the injected clock, or from time.Now when
// no clock was injected.
func (p *RetryPolicy) Now() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// Sleep waits for d using the injected sleep, or time.Sleep when none was
// injected. A non-positive d returns immediately without calling either.
func (p *RetryPolicy) Sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	if p.sleep != nil {
		p.sleep(d)
		return
	}
	time.Sleep(d)
}

// Timeout returns the per-attempt deadline, substituting DefaultSingleTimeout
// for an unset (zero) SingleTimeout (requirement 14.2).
func (p *RetryPolicy) Timeout() time.Duration {
	if p.SingleTimeout <= 0 {
		return DefaultSingleTimeout
	}
	return p.SingleTimeout
}

// NextDelay returns how long to wait before retry attempt, and whether that
// retry may happen at all.
//
// The delay is backoffSeconds clipped to the time left in the window, so the
// accumulated wait can never exceed RetryWindow (requirements 14.3, 14.4). ok
// is false when the window is exhausted and no further retry may happen: with
// RetryWindow == 0 the first failure already has elapsed >= 0 == RetryWindow,
// so exactly one attempt happens (requirement 14.13).
//
// attempt is the 1-based number of the retry that is about to happen and
// elapsed is the time spent since the first attempt.
func (p *RetryPolicy) NextDelay(attempt int, elapsed time.Duration) (delay time.Duration, ok bool) {
	remaining := p.RetryWindow - elapsed
	if remaining <= 0 {
		return 0, false
	}
	backoff := backoffSeconds(attempt)
	if backoff > remaining {
		return remaining, true
	}
	return backoff, true
}
