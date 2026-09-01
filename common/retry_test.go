// Behavioural tests for the retry layer of retry.go (requirements 16.13,
// 16.14).
//
// Division of labour with the neighbouring test files:
//
//   - errorcode_test.go asserts the *values* of the retry constants against the
//     spec's "Cross-Language Contract" table (Property 9): the single timeout
//     default, the backoff cap, the per-kind default Retry_Window and the
//     retryable status code set.
//   - this file asserts the *behaviour* built on top of them: the shape of the
//     backoff sequence (Property 7), the four TAKLER_TIMEOUT resolution cases,
//     the Retry_Window bookkeeping of RetryPolicy, and the retry loop of the
//     generic Call end to end against the in-process server (requirements 14.1,
//     14.5 ~ 14.8, 14.13, 13.7).
//
// Nothing here sleeps for real. The window tests inject a fake clock into the
// unexported now / sleep fields of RetryPolicy, which is possible because the
// test lives in the same package, and they assert that the wall clock barely
// moved while a 86400 second window was consumed. The Call tests reach the same
// seam through callSettings.
//
// Helpers are prefixed with retry so they cannot collide with the helpers of the
// other test files in this package. The in-process gRPC server comes from
// testing_test.go and is reused as is.
package common

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// retryExpectedBackoff is the wait before retry 1 through 8, transcribed from
// the spec's "重试常量" table (1、2、4、8、16、32、60、60). It is written as
// literals on purpose: a table derived from MaxBackoff or from the formula in
// backoffSeconds could not detect a wrong formula.
var retryExpectedBackoff = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	32 * time.Second,
	60 * time.Second,
	60 * time.Second,
}

// retryFakeClock is a clock whose only way to advance is to sleep on it.
//
// It plays the role the Python RetryPolicy's injected clock and sleep play: the
// retry loop believes hours went by while the test spends microseconds. Every
// slept duration is recorded, so a test can assert both the sequence of waits
// and that the loop slept exactly as many times as it retried.
type retryFakeClock struct {
	current time.Time
	sleeps  []time.Duration
}

// newRetryFakeClock starts a fake clock at a fixed instant. The instant itself
// is arbitrary: only differences matter to RetryPolicy.
func newRetryFakeClock() *retryFakeClock {
	return &retryFakeClock{current: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
}

// Now returns the current fake time.
func (c *retryFakeClock) Now() time.Time { return c.current }

// Sleep records d and advances the fake time by it, without blocking.
func (c *retryFakeClock) Sleep(d time.Duration) {
	c.sleeps = append(c.sleeps, d)
	c.current = c.current.Add(d)
}

// slept returns the total fake time spent sleeping.
func (c *retryFakeClock) slept() time.Duration {
	var total time.Duration
	for _, d := range c.sleeps {
		total += d
	}
	return total
}

// retryPolicyWithClock builds a policy over window whose time source and sleep
// are clock, i.e. one that cannot block the test process.
func retryPolicyWithClock(window time.Duration, clock *retryFakeClock) *RetryPolicy {
	return &RetryPolicy{
		RetryWindow:   window,
		SingleTimeout: DefaultSingleTimeout,
		now:           clock.Now,
		sleep:         clock.Sleep,
	}
}

// retryDrainWindow drives NextDelay the way a retry loop over a permanently
// failing call would: ask for the next delay, stop when the window is over,
// otherwise sleep and count one more retry.
//
// It deliberately performs no RPC and knows nothing about status codes: the
// generic Call of task 14.2 owns that part. What is asserted here is only that
// the window bookkeeping terminates and stays inside its budget.
//
// maxRetries guards against a bookkeeping bug turning the test into an infinite
// loop; exceeding it fails the test rather than hanging the suite.
func retryDrainWindow(t *testing.T, policy *RetryPolicy, clock *retryFakeClock, maxRetries int) []time.Duration {
	t.Helper()

	start := policy.Now()
	var delays []time.Duration
	for attempt := 1; ; attempt++ {
		elapsed := policy.Now().Sub(start)
		delay, ok := policy.NextDelay(attempt, elapsed)
		if !ok {
			return delays
		}
		if elapsed+delay > policy.RetryWindow {
			t.Fatalf(
				"retry %d: elapsed %v + delay %v exceeds the window %v",
				attempt, elapsed, delay, policy.RetryWindow,
			)
		}
		policy.Sleep(delay)
		delays = append(delays, delay)
		if len(delays) > maxRetries {
			t.Fatalf("retry loop did not stop after %d retries, window %v", maxRetries, policy.RetryWindow)
		}
	}
}

// Feature: m2-security, Property 7: 退避序列单调有界
// Validates: Requirements 14.4, 16.13
//
// TestBackoffSequenceMonotonicBounded asserts the backoff schedule twice over,
// from two angles that fail on different bugs: the literal values of retries 1
// through 8 catch a wrong formula, while the shape assertions -- monotonically
// non-decreasing, never above 60 seconds -- catch a wrong or missing cap far
// beyond the eighth retry, where a long lived child command actually lives.
func TestBackoffSequenceMonotonicBounded(t *testing.T) {
	t.Run("literal sequence of the first eight retries", func(t *testing.T) {
		for i, want := range retryExpectedBackoff {
			attempt := i + 1
			if got := backoffSeconds(attempt); got != want {
				t.Errorf("backoffSeconds(%d) = %v, want %v", attempt, got, want)
			}
		}
	})

	t.Run("monotonically non decreasing and bounded by 60s", func(t *testing.T) {
		// Well past the cap: a child command with the default 86400 second
		// window can reach retry number 1400 and beyond.
		const lastAttempt = 1500
		previous := backoffSeconds(1)
		for attempt := 1; attempt <= lastAttempt; attempt++ {
			got := backoffSeconds(attempt)
			if got < previous {
				t.Fatalf("backoffSeconds(%d) = %v, less than the previous %v", attempt, got, previous)
			}
			if got > MaxBackoff {
				t.Fatalf("backoffSeconds(%d) = %v, above the cap %v", attempt, got, MaxBackoff)
			}
			if got <= 0 {
				t.Fatalf("backoffSeconds(%d) = %v, want a positive wait", attempt, got)
			}
			previous = got
		}
	})

	t.Run("non positive attempt is clamped to the first wait", func(t *testing.T) {
		// The only caller counts from 1, so a value below 1 is a programming
		// error. It must still not shift by a negative amount or return a
		// non-positive wait that would spin the retry loop.
		for _, attempt := range []int{0, -1, -100} {
			if got := backoffSeconds(attempt); got != 1*time.Second {
				t.Errorf("backoffSeconds(%d) = %v, want %v", attempt, got, 1*time.Second)
			}
		}
	})
}

// retryEnvUnset is a lookupEnv that reports every variable as absent.
func retryEnvUnset(string) (string, bool) { return "", false }

// retryEnvSet returns a lookupEnv reporting EnvRetryWindow as set to raw and
// every other variable as absent.
func retryEnvSet(raw string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		if key == EnvRetryWindow {
			return raw, true
		}
		return "", false
	}
}

// retryAllKinds is every command kind together with its default Retry_Window in
// seconds, from the spec's "重试常量" table.
var retryAllKinds = []struct {
	kind    CommandKind
	seconds int
}{
	{kind: KindChild, seconds: 86400},
	{kind: KindControl, seconds: 60},
	{kind: KindQuery, seconds: 60},
}

// TestResolveRetryWindowFrom covers the four TAKLER_TIMEOUT cases of requirement
// 16.14 -- unset, a valid non-negative integer, a blank string, and an
// unparseable string -- with the environment and the warning sink injected, so
// neither the process environment nor the real standard error is touched
// (requirements 14.9 ~ 14.12).
func TestResolveRetryWindowFrom(t *testing.T) {
	t.Run("unset falls back to the kind default in silence", func(t *testing.T) {
		for _, entry := range retryAllKinds {
			t.Run(entry.kind.String(), func(t *testing.T) {
				var warn bytes.Buffer
				want := time.Duration(entry.seconds) * time.Second
				got := resolveRetryWindowFrom(entry.kind, retryEnvUnset, &warn)
				if got != want {
					t.Errorf("resolveRetryWindowFrom(%v, unset) = %v, want %v", entry.kind, got, want)
				}
				if warn.Len() != 0 {
					t.Errorf("unset %s produced output %q, want none", EnvRetryWindow, warn.String())
				}
			})
		}
	})

	t.Run("non negative integer wins over the default", func(t *testing.T) {
		cases := []struct {
			raw  string
			want time.Duration
		}{
			{raw: "0", want: 0},
			{raw: "1", want: 1 * time.Second},
			{raw: "45", want: 45 * time.Second},
			{raw: "86400", want: 86400 * time.Second},
			// Surrounding blanks are stripped like Python's str.strip() does,
			// so a value pasted with a trailing newline still parses.
			{raw: "  30  ", want: 30 * time.Second},
			{raw: "30\n", want: 30 * time.Second},
			{raw: "007", want: 7 * time.Second},
		}
		for _, c := range cases {
			t.Run(fmt.Sprintf("%q", c.raw), func(t *testing.T) {
				var warn bytes.Buffer
				// The kind default differs from every expected value, so a
				// silent fallback cannot pass as a successful parse.
				got := resolveRetryWindowFrom(KindChild, retryEnvSet(c.raw), &warn)
				if got != c.want {
					t.Errorf("resolveRetryWindowFrom(child, %q) = %v, want %v", c.raw, got, c.want)
				}
				if warn.Len() != 0 {
					t.Errorf("%s = %q produced output %q, want none", EnvRetryWindow, c.raw, warn.String())
				}
			})
		}
	})

	t.Run("blank value warns and falls back", func(t *testing.T) {
		for _, raw := range []string{"", " ", "   ", "\t", "\n", " \t\r\n "} {
			t.Run(fmt.Sprintf("%q", raw), func(t *testing.T) {
				retryAssertWarnsAndFallsBack(t, KindControl, raw, 60*time.Second)
			})
		}
	})

	t.Run("unparseable value warns and falls back", func(t *testing.T) {
		cases := []string{
			"abc",
			"-1",
			"-0",
			"12.5",
			"1e3",
			"60s",
			"12 34",
			"1_000",
			"0x10",
			"+7",
			// Digits only, yet larger than a Duration can hold: reported like
			// any other bad value rather than silently wrapping.
			"99999999999999999999",
		}
		for _, raw := range cases {
			t.Run(fmt.Sprintf("%q", raw), func(t *testing.T) {
				retryAssertWarnsAndFallsBack(t, KindChild, raw, 86400*time.Second)
			})
		}
	})

	t.Run("unknown kind falls back to the interactive window", func(t *testing.T) {
		// An unregistered kind is a programming error. Failing closed on the
		// short window is the safe choice: a caller that gives up after a
		// minute beats one that never returns.
		var warn bytes.Buffer
		unknown := CommandKind(99)
		got := resolveRetryWindowFrom(unknown, retryEnvUnset, &warn)
		if got != 60*time.Second {
			t.Errorf("resolveRetryWindowFrom(unknown, unset) = %v, want %v", got, 60*time.Second)
		}
		if warn.Len() != 0 {
			t.Errorf("an unknown kind produced output %q, want none: the environment is fine", warn.String())
		}
	})
}

// retryAssertWarnsAndFallsBack asserts that raw is rejected: the resolved window
// is the kind default and exactly one line naming the offending value goes to
// the warning sink (requirement 14.12).
func retryAssertWarnsAndFallsBack(t *testing.T, kind CommandKind, raw string, want time.Duration) {
	t.Helper()

	var warn bytes.Buffer
	got := resolveRetryWindowFrom(kind, retryEnvSet(raw), &warn)
	if got != want {
		t.Errorf("resolveRetryWindowFrom(%v, %q) = %v, want the default %v", kind, raw, got, want)
	}

	output := warn.String()
	if output == "" {
		t.Fatalf("%s = %q produced no output, want one warning line", EnvRetryWindow, raw)
	}
	if lines := strings.Count(strings.TrimRight(output, "\n"), "\n"); lines != 0 {
		t.Errorf("%s = %q produced %d lines, want exactly one:\n%s", EnvRetryWindow, raw, lines+1, output)
	}
	if !strings.HasSuffix(output, "\n") {
		t.Errorf("warning for %q is not newline terminated: %q", raw, output)
	}
	if !strings.Contains(output, EnvRetryWindow) {
		t.Errorf("warning for %q does not name %s: %q", raw, EnvRetryWindow, output)
	}
	// The quoted form is what makes a blank or whitespace value visible in a
	// terminal, which is the whole point of requirement 14.12.
	if quoted := fmt.Sprintf("%q", raw); !strings.Contains(output, quoted) {
		t.Errorf("warning for %q does not contain the offending value %s: %q", raw, quoted, output)
	}
}

// TestResolveRetryWindowFromProcessEnv checks the thin wrapper that binds
// resolveRetryWindowFrom to the real environment, which the injected tests above
// cannot reach (requirements 14.9 ~ 14.11).
//
// Only accepted values are exercised here: a rejected one would write to the
// real standard error, and that output belongs to the injected tests.
func TestResolveRetryWindowFromProcessEnv(t *testing.T) {
	t.Run("reads a valid value from the environment", func(t *testing.T) {
		t.Setenv(EnvRetryWindow, "25")
		if got := resolveRetryWindow(KindChild); got != 25*time.Second {
			t.Errorf("resolveRetryWindow(child) = %v, want %v", got, 25*time.Second)
		}
	})

	t.Run("kind defaults apply when the variable is absent", func(t *testing.T) {
		// t.Setenv registers the restore of the previous state with t.Cleanup,
		// so removing the variable afterwards is safe and reversible: the
		// process environment is back to normal when the test ends.
		t.Setenv(EnvRetryWindow, "unused")
		if err := os.Unsetenv(EnvRetryWindow); err != nil {
			t.Fatalf("unset %s: %v", EnvRetryWindow, err)
		}
		for _, entry := range retryAllKinds {
			want := time.Duration(entry.seconds) * time.Second
			if got := resolveRetryWindow(entry.kind); got != want {
				t.Errorf("resolveRetryWindow(%v) = %v, want %v", entry.kind, got, want)
			}
		}
	})
}

// TestRetryPolicyWindowWithFakeClock drives the Retry_Window bookkeeping of a
// child command's 86400 second window against a fake clock and asserts it
// terminates, stays inside its budget, and never sleeps for real (requirements
// 14.3, 14.4, 16.13).
//
// The loop here is a stand-in for the retry loop of the generic Call: what is
// under test is RetryPolicy alone, with no RPC involved, so a failure points at
// the bookkeeping rather than at the plumbing. The end-to-end counterpart, the
// same window driven through Call against the in-process server, is
// TestCallStopsWhenWindowExhausted.
func TestRetryPolicyWindowWithFakeClock(t *testing.T) {
	const window = 86400 * time.Second

	clock := newRetryFakeClock()
	policy := retryPolicyWithClock(window, clock)

	wallStart := time.Now()
	delays := retryDrainWindow(t, policy, clock, 2000)
	wallElapsed := time.Since(wallStart)

	if len(delays) == 0 {
		t.Fatal("the retry loop stopped before the first retry, want a full window of retries")
	}

	// The window is spent exactly: the final delay is clipped to whatever time
	// was left, so the accumulated wait lands on the budget rather than
	// overshooting it.
	var total time.Duration
	for _, d := range delays {
		total += d
	}
	if total > window {
		t.Errorf("accumulated wait = %v, exceeds the window %v", total, window)
	}
	if total != window {
		t.Errorf("accumulated wait = %v, want the window %v to be spent exactly", total, window)
	}

	// The first six delays are the uncapped part of the schedule, and no delay
	// anywhere may exceed the cap.
	for i, want := range retryExpectedBackoff {
		if i >= len(delays) {
			t.Fatalf("the loop made only %d retries, want at least %d", len(delays), len(retryExpectedBackoff))
		}
		if delays[i] != want {
			t.Errorf("delay %d = %v, want %v", i+1, delays[i], want)
		}
	}
	for i, d := range delays {
		if d > MaxBackoff {
			t.Errorf("delay %d = %v, above the cap %v", i+1, d, MaxBackoff)
		}
		if d <= 0 {
			t.Errorf("delay %d = %v, want a positive wait", i+1, d)
		}
	}
	// Only the last delay may shrink, and only because it was clipped to the
	// time left in the window.
	for i := 1; i < len(delays)-1; i++ {
		if delays[i] < delays[i-1] {
			t.Errorf("delay %d = %v, less than the previous %v", i+1, delays[i], delays[i-1])
		}
	}
	if last := delays[len(delays)-1]; last > MaxBackoff {
		t.Errorf("final delay = %v, above the cap %v", last, MaxBackoff)
	}

	// No further retry is allowed once the window is spent.
	if delay, ok := policy.NextDelay(len(delays)+1, window); ok {
		t.Errorf("NextDelay after the window returned (%v, true), want ok=false", delay)
	}

	// Every retry slept exactly once, and all of that sleeping was fake: a day
	// of backoff went by on the fake clock while the wall clock barely moved.
	if len(clock.sleeps) != len(delays) {
		t.Errorf("slept %d times for %d retries, want one sleep per retry", len(clock.sleeps), len(delays))
	}
	if clock.slept() != total {
		t.Errorf("fake clock slept %v, want %v", clock.slept(), total)
	}
	const wallBudget = 2 * time.Second
	if wallElapsed > wallBudget {
		t.Errorf("the loop took %v of wall clock time, want under %v: it slept for real", wallElapsed, wallBudget)
	}
}

// TestRetryPolicyZeroWindowSingleAttempt asserts that a Retry_Window of 0 means
// one attempt and no retry (requirement 14.13).
func TestRetryPolicyZeroWindowSingleAttempt(t *testing.T) {
	t.Run("no retry is ever granted", func(t *testing.T) {
		clock := newRetryFakeClock()
		policy := retryPolicyWithClock(0, clock)

		if delay, ok := policy.NextDelay(1, 0); ok {
			t.Errorf("NextDelay(1, 0) = (%v, true), want ok=false with a zero window", delay)
		}
		if delay, ok := policy.NextDelay(1, 5*time.Second); ok {
			t.Errorf("NextDelay(1, 5s) = (%v, true), want ok=false with a zero window", delay)
		}

		delays := retryDrainWindow(t, policy, clock, 4)
		if len(delays) != 0 {
			t.Errorf("the loop made %d retries (%v), want none", len(delays), delays)
		}
		if len(clock.sleeps) != 0 {
			t.Errorf("the loop slept %v, want no sleep at all", clock.sleeps)
		}
	})

	t.Run("a positive window does grant the first retry", func(t *testing.T) {
		// The counterpart of the assertion above: ok=false must come from the
		// zero window, not from NextDelay refusing every first retry.
		clock := newRetryFakeClock()
		policy := retryPolicyWithClock(60*time.Second, clock)
		delay, ok := policy.NextDelay(1, 0)
		if !ok {
			t.Fatal("NextDelay(1, 0) = ok=false with a 60s window, want the first retry granted")
		}
		if delay != 1*time.Second {
			t.Errorf("NextDelay(1, 0) delay = %v, want %v", delay, 1*time.Second)
		}
	})
}

// TestRetryStatusClassificationAgainstServer checks the retry decision against
// real gRPC statuses produced by the in-process server of testing_test.go
// (requirement 14.3).
//
// The classification is asserted at the point where the retry loop consults it,
// i.e. on the status code the client actually observes, rather than on a code
// written down by hand.
func TestRetryStatusClassificationAgainstServer(t *testing.T) {
	t.Run("a retryable transport failure is retried inside the window", func(t *testing.T) {
		server := newFakeServer(t, fakeAlwaysFail(codes.Unavailable))

		_, err := server.Client.RunRequestPing(context.Background(), &pb.PingRequest{})
		if err == nil {
			t.Fatalf("RunRequestPing succeeded, want %v", codes.Unavailable)
		}
		code := status.Code(err)
		if code != codes.Unavailable {
			t.Fatalf("status code = %v, want %v", code, codes.Unavailable)
		}
		if !IsRetryableStatus(code) {
			t.Fatalf("IsRetryableStatus(%v) = false, want true", code)
		}

		clock := newRetryFakeClock()
		policy := retryPolicyWithClock(60*time.Second, clock)
		if _, ok := policy.NextDelay(1, 0); !ok {
			t.Error("NextDelay(1, 0) = ok=false, want the retry granted inside a 60s window")
		}
	})

	t.Run("a non retryable status is not retried", func(t *testing.T) {
		server := newFakeServer(t, fakeAlwaysFail(codes.PermissionDenied))

		_, err := server.Client.RunCommandRequeue(context.Background(), &pb.RequeueCommand{})
		if err == nil {
			t.Fatalf("RunCommandRequeue succeeded, want %v", codes.PermissionDenied)
		}
		code := status.Code(err)
		if code != codes.PermissionDenied {
			t.Fatalf("status code = %v, want %v", code, codes.PermissionDenied)
		}
		// The window is wide open, so only the classification can stop the
		// retry here.
		if IsRetryableStatus(code) {
			t.Errorf("IsRetryableStatus(%v) = true, want false", code)
		}
		if got := server.Servicer.callCount(); got != 1 {
			t.Errorf("call count = %d, want 1", got)
		}
	})
}

// TestRetryNonZeroFlagIsNotATransportFailure asserts that a response carrying a
// non-zero flag is a *successful* RPC, so nothing in the retry path can turn it
// into a retry (requirement 14.8).
//
// The flag carries the server's Error_Code: the request reached the server and
// was answered, it was simply refused. Repeating it cannot change the outcome.
// At the transport level that shows up as codes.OK, which is not in
// RetryableStatusCodes, and the servicer sees exactly one call.
//
// This is the transport level half of the assertion. The other half, that the
// generic Call hands such a response back to its caller untouched and makes no
// second attempt, is TestCallReturnsNonZeroFlagWithoutRetrying.
func TestRetryNonZeroFlagIsNotATransportFailure(t *testing.T) {
	const flag = 43 // permission_denied, an Error_Code the client must not retry
	server := newFakeServer(t, fakeWithFlag(flag), fakeWithMessage("refused"))

	response, err := server.Client.RunCommandComplete(context.Background(), &pb.CompleteCommand{
		ChildOptions: &pb.ChildCommandOptions{NodePath: "/flow1/task1"},
	})
	if err != nil {
		t.Fatalf("RunCommandComplete: %v, want a successful RPC carrying flag %d", err, flag)
	}
	if got := response.GetFlag(); got != flag {
		t.Fatalf("flag = %d, want %d", got, flag)
	}

	// No transport failure happened, so the code the retry loop would classify
	// is OK, and OK is not retryable. IsRetryableStatus is never consulted for a
	// successful call; asserting it here pins that it could not grant a retry
	// even if it were.
	if code := status.Code(err); code != codes.OK {
		t.Errorf("status code = %v, want %v", code, codes.OK)
	}
	if IsRetryableStatus(codes.OK) {
		t.Errorf("IsRetryableStatus(%v) = true, want false", codes.OK)
	}

	// One call arrived, so nothing retried behind the test's back.
	if got := server.Servicer.callCount(); got != 1 {
		t.Errorf("call count = %d, want exactly 1", got)
	}
	if got := server.Servicer.lastCall(t).Method; got != "RunCommandComplete" {
		t.Errorf("method = %q, want %q", got, "RunCommandComplete")
	}
}

// End-to-end assertions on the generic Call ---------------------------------
//
// Everything below drives the real retry loop of Call against the in-process
// server of testing_test.go, with the fake clock injected through callSettings.
// The tests above pin the pieces; these pin the loop that puts them together.

// retryCallHost, retryCallPort and retryCallAddress are the address a Call test
// client claims to talk to.
//
// The client is never connected: Call reaches the in-process server through the
// invoke closure, i.e. through a stub already bound to the bufconn connection.
// The address only has to appear in the diagnostics, and an unmistakable one
// makes a substring assertion meaningful.
const (
	retryCallHost    = "takler-login-node"
	retryCallPort    = "33083"
	retryCallAddress = retryCallHost + ":" + retryCallPort
)

// retryCallClient returns the client a Call test passes to Call: it supplies the
// server address for diagnostics and the Credentials of the call, and nothing
// else.
func retryCallClient() *TaklerServiceClient {
	return NewTaklerServiceClient(retryCallHost, retryCallPort, SecurityLevels{})
}

// retryRequireExitError asserts that err is an *ExitError carrying wantCode and
// returns it for further assertions on its message.
func retryRequireExitError(t *testing.T, err error, wantCode int) *ExitError {
	t.Helper()

	if err == nil {
		t.Fatalf("the call succeeded, want an *ExitError with code %d", wantCode)
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error is %v (%T), want an *ExitError", err, err)
	}
	if exitErr.Code != wantCode {
		t.Errorf("exit code = %d, want %d (message: %s)", exitErr.Code, wantCode, exitErr.Message)
	}
	return exitErr
}

// retryWarnLines splits the diagnostics sink into lines, asserting that the
// output is either empty or newline terminated.
func retryWarnLines(t *testing.T, warn *bytes.Buffer) []string {
	t.Helper()

	output := warn.String()
	if output == "" {
		return nil
	}
	if !strings.HasSuffix(output, "\n") {
		t.Errorf("diagnostics output is not newline terminated: %q", output)
	}
	return strings.Split(strings.TrimRight(output, "\n"), "\n")
}

// retryAssertMessageContains asserts that message names every want.
func retryAssertMessageContains(t *testing.T, label string, message string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(message, want) {
			t.Errorf("%s does not contain %q: %s", label, want, message)
		}
	}
}

// TestCallStopsWhenWindowExhausted is the end-to-end version of
// TestRetryPolicyWindowWithFakeClock: a child command's 86400 second window
// against a server that never recovers, driven through Call (requirements 14.3,
// 14.5, 14.6, 16.13).
//
// It asserts what the policy-only test cannot: that the loop really retries the
// RPC (the server counts the attempts), that it stops when the window is spent
// rather than looping forever, that the failure is an *ExitError carrying exit
// code 4 whose message names the address, the total attempts and the last status
// code, that every retry produced one line of diagnostics, and that a day of
// backoff went by without the process sleeping for real.
func TestCallStopsWhenWindowExhausted(t *testing.T) {
	const window = 86400 * time.Second

	credCleanEnv(t)
	server := newFakeServer(t, fakeAlwaysFail(codes.Unavailable))
	clock := newRetryFakeClock()
	var warn bytes.Buffer

	wallStart := time.Now()
	response, err := callWith(
		retryCallClient(),
		context.Background(),
		"complete",
		KindChild,
		&pb.CompleteCommand{ChildOptions: &pb.ChildCommandOptions{NodePath: "/flow1/task1"}},
		server.Client.RunCommandComplete,
		callSettings{policy: retryPolicyWithClock(window, clock), warn: &warn},
	)
	wallElapsed := time.Since(wallStart)

	if response != nil {
		t.Errorf("response = %v, want the zero value on an exhausted window", response)
	}
	exitErr := retryRequireExitError(t, err, ExitUnreachable)

	attempts := server.Servicer.callCount()
	if attempts < 2 {
		t.Fatalf("the server received %d calls, want the first attempt plus retries", attempts)
	}
	retryAssertMessageContains(
		t, "the unreachable message", exitErr.Message,
		retryCallAddress,
		fmt.Sprintf("%d attempts", attempts),
		codes.Unavailable.String(),
	)

	// One retry per sleep, one attempt per retry plus the first one.
	if len(clock.sleeps) != attempts-1 {
		t.Errorf("slept %d times for %d attempts, want %d", len(clock.sleeps), attempts, attempts-1)
	}
	if clock.slept() != window {
		t.Errorf("fake clock slept %v, want the window %v to be spent exactly", clock.slept(), window)
	}

	// One line per retry, each naming the address, the command and the status
	// code, and the first two carrying the elapsed time of a fresh window
	// (requirement 14.6).
	lines := retryWarnLines(t, &warn)
	if len(lines) != attempts-1 {
		t.Errorf("wrote %d diagnostics lines for %d retries, want one each", len(lines), attempts-1)
	}
	if len(lines) >= 2 {
		want := []string{
			fmt.Sprintf("retry complete to %s: elapsed=0.0s, status=%v", retryCallAddress, codes.Unavailable),
			fmt.Sprintf("retry complete to %s: elapsed=1.0s, status=%v", retryCallAddress, codes.Unavailable),
		}
		for i, w := range want {
			if lines[i] != w {
				t.Errorf("diagnostics line %d = %q, want %q", i+1, lines[i], w)
			}
		}
	}
	for i, line := range lines {
		retryAssertMessageContains(
			t, fmt.Sprintf("diagnostics line %d", i+1), line,
			retryCallAddress, "complete", "elapsed=", codes.Unavailable.String(),
		)
	}

	// A day of backoff on the fake clock, microseconds of it on the wall clock.
	// The budget is generous because the loop makes one real RPC per attempt over
	// the in-process connection; a single real 60 second sleep would still blow
	// straight through it.
	const wallBudget = 10 * time.Second
	if wallElapsed > wallBudget {
		t.Errorf("the call took %v of wall clock time, want under %v: it slept for real", wallElapsed, wallBudget)
	}
}

// TestCallRetriesThenSucceeds asserts the happy path of the retry loop: a server
// that is unreachable for the first two attempts and answers on the third
// (requirements 14.3, 14.4, 14.6).
//
// This is the case the whole feature exists for -- a server restart during a
// job's lifetime -- and the only one that shows the loop returning a response
// obtained *after* retrying.
func TestCallRetriesThenSucceeds(t *testing.T) {
	credCleanEnv(t)
	server := newFakeServer(t, fakeFailFirst(2, codes.Unavailable), fakeWithMessage("queued"))
	clock := newRetryFakeClock()
	var warn bytes.Buffer

	response, err := callWith(
		retryCallClient(),
		context.Background(),
		"init",
		KindChild,
		&pb.InitCommand{
			ChildOptions: &pb.ChildCommandOptions{NodePath: "/flow1/task1"},
			TaskId:       "job-1",
		},
		server.Client.RunCommandInit,
		callSettings{policy: retryPolicyWithClock(86400*time.Second, clock), warn: &warn},
	)
	if err != nil {
		t.Fatalf("callWith: %v, want the third attempt to succeed", err)
	}
	if response == nil {
		t.Fatal("response is nil, want the server's response")
	}
	if got := response.GetFlag(); got != 0 {
		t.Errorf("flag = %d, want 0", got)
	}
	if got := response.GetMessage(); got != "queued" {
		t.Errorf("message = %q, want %q", got, "queued")
	}

	if got := server.Servicer.callCount(); got != 3 {
		t.Errorf("the server received %d calls, want 3", got)
	}
	// The two waits are the head of the backoff schedule, unclipped: the window
	// is a day and only two seconds of it were spent.
	want := []time.Duration{1 * time.Second, 2 * time.Second}
	if len(clock.sleeps) != len(want) {
		t.Fatalf("slept %v, want %v", clock.sleeps, want)
	}
	for i, d := range want {
		if clock.sleeps[i] != d {
			t.Errorf("sleep %d = %v, want %v", i+1, clock.sleeps[i], d)
		}
	}
	if lines := retryWarnLines(t, &warn); len(lines) != 2 {
		t.Errorf("wrote %d diagnostics lines for 2 retries, want one each:\n%s", len(lines), warn.String())
	}
}

// TestCallDoesNotRetryNonRetryableStatus asserts that a status code outside
// RetryableStatusCodes ends the call on the first failure, with the exit code
// that code maps to (requirements 14.7, 15.5).
//
// The window is wide open in every case, so only the classification can stop the
// retry: the server counting exactly one call is the assertion.
func TestCallDoesNotRetryNonRetryableStatus(t *testing.T) {
	cases := []struct {
		code     codes.Code
		exitCode int
	}{
		// The four codes that mean "the request itself is wrong".
		{code: codes.InvalidArgument, exitCode: ExitRequestError},
		{code: codes.NotFound, exitCode: ExitRequestError},
		{code: codes.PermissionDenied, exitCode: ExitRequestError},
		{code: codes.Unauthenticated, exitCode: ExitRequestError},
		// Any other code is neither retryable nor a request error: the client
		// never reached a usable server, which is unreachable.
		{code: codes.Internal, exitCode: ExitUnreachable},
	}

	for _, c := range cases {
		t.Run(c.code.String(), func(t *testing.T) {
			credCleanEnv(t)
			server := newFakeServer(t, fakeAlwaysFail(c.code))
			clock := newRetryFakeClock()
			var warn bytes.Buffer

			response, err := callWith(
				retryCallClient(),
				context.Background(),
				"requeue",
				KindControl,
				&pb.RequeueCommand{},
				server.Client.RunCommandRequeue,
				callSettings{policy: retryPolicyWithClock(86400*time.Second, clock), warn: &warn},
			)
			if response != nil {
				t.Errorf("response = %v, want the zero value on a failed call", response)
			}
			exitErr := retryRequireExitError(t, err, c.exitCode)
			retryAssertMessageContains(
				t, "the failure message", exitErr.Message,
				"requeue", retryCallAddress, c.code.String(),
			)

			if got := server.Servicer.callCount(); got != 1 {
				t.Errorf("the server received %d calls, want exactly 1: %v is not retryable", got, c.code)
			}
			if len(clock.sleeps) != 0 {
				t.Errorf("slept %v, want no wait at all", clock.sleeps)
			}
			if output := warn.String(); output != "" {
				t.Errorf("wrote %q, want no retry line for a call that was not retried", output)
			}
		})
	}
}

// TestCallZeroWindowSingleAttempt asserts that a Retry_Window of 0 makes one
// attempt and no retry, even for a retryable failure (requirement 14.13).
func TestCallZeroWindowSingleAttempt(t *testing.T) {
	credCleanEnv(t)
	server := newFakeServer(t, fakeAlwaysFail(codes.Unavailable))
	clock := newRetryFakeClock()
	var warn bytes.Buffer

	_, err := callWith(
		retryCallClient(),
		context.Background(),
		"ping",
		KindQuery,
		&pb.PingRequest{},
		server.Client.RunRequestPing,
		callSettings{policy: retryPolicyWithClock(0, clock), warn: &warn},
	)
	exitErr := retryRequireExitError(t, err, ExitUnreachable)
	retryAssertMessageContains(
		t, "the unreachable message", exitErr.Message,
		retryCallAddress, "1 attempts", codes.Unavailable.String(),
	)

	if got := server.Servicer.callCount(); got != 1 {
		t.Errorf("the server received %d calls, want exactly 1 with a zero window", got)
	}
	if len(clock.sleeps) != 0 {
		t.Errorf("slept %v, want no wait at all", clock.sleeps)
	}
	if output := warn.String(); output != "" {
		t.Errorf("wrote %q, want no retry line: no retry happened", output)
	}
}

// TestCallReturnsNonZeroFlagWithoutRetrying is the end-to-end counterpart of
// TestRetryNonZeroFlagIsNotATransportFailure: a response carrying a non-zero
// flag comes back from Call unchanged, on the first attempt (requirement 14.8).
//
// Call does not look at the flag at all -- turning an Error_Code into an exit
// code is the caller's job -- so the assertions are that no error was returned,
// that the flag and message arrived intact, and that the wide open window bought
// no second attempt.
func TestCallReturnsNonZeroFlagWithoutRetrying(t *testing.T) {
	const flag = 43 // permission_denied, an Error_Code the client must not retry

	credCleanEnv(t)
	server := newFakeServer(t, fakeWithFlag(flag), fakeWithMessage("refused"))
	clock := newRetryFakeClock()
	var warn bytes.Buffer

	response, err := callWith(
		retryCallClient(),
		context.Background(),
		"suspend",
		KindControl,
		&pb.SuspendCommand{},
		server.Client.RunCommandSuspend,
		callSettings{policy: retryPolicyWithClock(86400*time.Second, clock), warn: &warn},
	)
	if err != nil {
		t.Fatalf("callWith: %v, want the response carrying flag %d", err, flag)
	}
	if got := response.GetFlag(); got != flag {
		t.Errorf("flag = %d, want %d", got, flag)
	}
	if got := response.GetMessage(); got != "refused" {
		t.Errorf("message = %q, want %q", got, "refused")
	}

	if got := server.Servicer.callCount(); got != 1 {
		t.Errorf("the server received %d calls, want exactly 1: a non-zero flag is not retried", got)
	}
	if len(clock.sleeps) != 0 {
		t.Errorf("slept %v, want no wait at all", clock.sleeps)
	}
	if output := warn.String(); output != "" {
		t.Errorf("wrote %q, want no retry line for a successful RPC", output)
	}
}

// TestCallInjectsCredentialMetadata asserts that the credentials reach the wire
// through Call itself, so no command implementation contains credential code
// (requirement 13.7).
//
// credentials_test.go asserts what BuildMetadata produces and that such metadata
// survives the wire; what is asserted here is that Call is the thing attaching
// it, and that neither the diagnostics nor an error carries a credential value
// (requirement 13.13).
func TestCallInjectsCredentialMetadata(t *testing.T) {
	t.Run("a child command carries takler-pass", func(t *testing.T) {
		credCleanEnv(t)
		t.Setenv(EnvJobPassword, credPasswordValue)

		server := newFakeServer(t)
		var warn bytes.Buffer
		clock := newRetryFakeClock()

		_, err := callWith(
			retryCallClient(),
			context.Background(),
			"abort",
			KindChild,
			&pb.AbortCommand{ChildOptions: &pb.ChildCommandOptions{NodePath: "/flow1/task1"}},
			server.Client.RunCommandAbort,
			callSettings{policy: retryPolicyWithClock(60*time.Second, clock), warn: &warn},
		)
		if err != nil {
			t.Fatalf("callWith: %v", err)
		}

		call := server.Servicer.lastCall(t)
		if got := call.value(MetadataPass); got != credPasswordValue {
			t.Errorf("%s = %q, want the job password", MetadataPass, got)
		}
		if got := call.value(MetadataSecret); got != "" {
			t.Errorf("%s = %q, want it absent on a child command", MetadataSecret, got)
		}
		credAssertNoCredentialValue(t, "the diagnostics output", warn.String())
	})

	t.Run("an operator command carries takler-secret and takler-user", func(t *testing.T) {
		credCleanEnv(t)
		t.Setenv(EnvSecretFile, credSecretFile(t, credSecretValue+"\n"))
		username := credRequireUsername(t)

		server := newFakeServer(t)
		var warn bytes.Buffer
		clock := newRetryFakeClock()

		_, err := callWith(
			retryCallClient(),
			context.Background(),
			"show",
			KindQuery,
			&pb.ShowRequest{},
			server.Client.RunRequestShow,
			callSettings{policy: retryPolicyWithClock(60*time.Second, clock), warn: &warn},
		)
		if err != nil {
			t.Fatalf("callWith: %v", err)
		}

		call := server.Servicer.lastCall(t)
		if got := call.value(MetadataSecret); got != credSecretValue {
			t.Errorf("%s = %q, want the operator secret", MetadataSecret, got)
		}
		if got := call.value(MetadataUser); got != username {
			t.Errorf("%s = %q, want %q", MetadataUser, got, username)
		}
		if got := call.value(MetadataPass); got != "" {
			t.Errorf("%s = %q, want it absent on an operator command", MetadataPass, got)
		}
		credAssertNoCredentialValue(t, "the diagnostics output", warn.String())
	})
}

// TestCallRejectsUnreadableSecretFileBeforeAnyAttempt asserts that a credential
// the client cannot assemble ends the call before it touches the server, with
// exit code 1 and no retry (requirements 13.7, 13.12).
//
// The operator named a specific secret file and the client cannot honour that.
// Retrying cannot make the file readable, and sending the call without the
// secret would silently downgrade what the operator asked for, so the only sound
// outcome is to fail before the first attempt. The server receiving zero calls
// is the assertion that carries this.
func TestCallRejectsUnreadableSecretFileBeforeAnyAttempt(t *testing.T) {
	credCleanEnv(t)
	missing := filepath.Join(t.TempDir(), "absent.secret")
	t.Setenv(EnvSecretFile, missing)

	server := newFakeServer(t)
	clock := newRetryFakeClock()
	var warn bytes.Buffer

	_, err := callWith(
		retryCallClient(),
		context.Background(),
		"requeue",
		KindControl,
		&pb.RequeueCommand{},
		server.Client.RunCommandRequeue,
		callSettings{policy: retryPolicyWithClock(86400*time.Second, clock), warn: &warn},
	)
	exitErr := retryRequireExitError(t, err, ExitRequestError)
	retryAssertMessageContains(t, "the credential failure message", exitErr.Message, missing)

	if got := server.Servicer.callCount(); got != 0 {
		t.Errorf("the server received %d calls, want none: the call failed before the first attempt", got)
	}
	if len(clock.sleeps) != 0 {
		t.Errorf("slept %v, want no wait at all", clock.sleeps)
	}
	if output := warn.String(); output != "" {
		t.Errorf("wrote %q, want no retry line", output)
	}
}

// TestCallUsesProcessRetryWindow exercises the exported Call, i.e. the
// production wiring the tests above bypass: the Retry_Window resolved from
// TAKLER_TIMEOUT, the real clock and the real standard error (requirements 14.1,
// 14.9, 14.13).
//
// Both cases are chosen to make no retry happen, so nothing sleeps for real and
// nothing is written to the process's standard error.
func TestCallUsesProcessRetryWindow(t *testing.T) {
	t.Run("a zero window from the environment allows one attempt", func(t *testing.T) {
		credCleanEnv(t)
		t.Setenv(EnvRetryWindow, "0")
		server := newFakeServer(t, fakeAlwaysFail(codes.Unavailable))

		_, err := Call(
			retryCallClient(),
			context.Background(),
			"ping",
			KindQuery,
			&pb.PingRequest{},
			server.Client.RunRequestPing,
		)
		exitErr := retryRequireExitError(t, err, ExitUnreachable)
		retryAssertMessageContains(
			t, "the unreachable message", exitErr.Message,
			retryCallAddress, "1 attempts", codes.Unavailable.String(),
		)
		if got := server.Servicer.callCount(); got != 1 {
			t.Errorf("the server received %d calls, want exactly 1", got)
		}
	})

	t.Run("a reachable server answers on the first attempt", func(t *testing.T) {
		credCleanEnv(t)
		t.Setenv(EnvRetryWindow, "0")
		server := newFakeServer(t, fakeWithMessage("pong"))

		response, err := Call(
			retryCallClient(),
			context.Background(),
			"show",
			KindQuery,
			&pb.ShowRequest{},
			server.Client.RunRequestShow,
		)
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if got := response.GetOutput(); got != "pong" {
			t.Errorf("output = %q, want %q", got, "pong")
		}
		if got := server.Servicer.callCount(); got != 1 {
			t.Errorf("the server received %d calls, want exactly 1", got)
		}
	})
}
