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
//     and the Retry_Window bookkeeping of RetryPolicy.
//
// Nothing here sleeps for real. The window tests inject a fake clock into the
// unexported now / sleep fields of RetryPolicy, which is possible because the
// test lives in the same package, and they assert that the wall clock barely
// moved while a 86400 second window was consumed.
//
// Helpers are prefixed with retry so they cannot collide with the helpers of the
// other test files in this package. The in-process gRPC server comes from
// testing_test.go and is reused as is.
package common

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	pb "github.com/perillaroc/takler-client/takler_protocol"
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
// The loop here is a stand-in for the retry loop of the generic Call, which is
// task 14.2 and does not exist yet: what is under test is RetryPolicy, not the
// RPC plumbing. Task 14.2 should extend this with the end-to-end version driven
// through Call against the in-process server, rather than duplicate it.
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
// This is the strongest form available today: the end-to-end assertion, that the
// generic Call returns such a response to its caller without retrying, belongs
// with task 14.2's Call and should extend this test rather than duplicate it.
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
