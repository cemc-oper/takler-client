// The Go Call_Wrapper: the single path every RPC of this client takes.
//
// retry.go answers "whether and how long to wait", credentials.go answers "what
// credentials does this call carry", exitcode.go answers "what does a failure
// exit with". This file is the RPC plumbing that puts the three together and
// runs the attempt loop, which is why it sits in its own file rather than in
// retry.go: retry.go is deliberately free of RPC knowledge, and keeping it that
// way is what lets its own tests drive the window bookkeeping with nothing but a
// fake clock.
//
// Everything a command method used to repeat -- a hardcoded per-call timeout, a
// log.Fatalf on failure, no retry at all -- lives here once, so a command method
// is left with building a request and reading a response (requirements 14.1,
// 13.7).
//
// The attempt loop mirrors the Python client's ServiceClient._call in
// takler/client/service_client.py, which is the reference implementation of the
// cross-language contract. Any change to the classification, the ordering of the
// checks or the outcome of an exhausted window must be applied there as well.
//
// Requirements: 14.1, 14.3, 14.5, 14.6, 14.7, 14.8, 14.13, 13.7.
package common

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Call invokes invoke with a per-attempt timeout, backoff retry, credential
// injection and error to exit code mapping (requirement 14.1).
//
// The type parameters keep each RPC's concrete request and response types, so no
// caller casts an interface{} back to what it already knew it had. invoke is
// meant to be a generated stub method value, e.g. client.RunCommandInit, whose
// signature the constraint is written for; both type parameters are inferred
// from it.
//
// c supplies the credentials (requirement 13.7) and the server address that
// appears in diagnostics. It is not connected here: the connection belongs to
// withConnection, and invoke is a method of the client bound to it, which keeps
// one connection per command rather than one per attempt.
//
// ctx is the caller's context. It bounds the whole logical call, retries
// included; each attempt additionally gets the policy's single timeout
// (requirement 14.2).
//
// name is the command name as it appears in diagnostics, e.g. "complete". kind
// classifies the call, which selects both the credentials it carries and the
// default Retry_Window.
//
// The returned response is whatever the server answered, including a response
// whose flag is non zero: that is a *successful* RPC carrying the server's
// Error_Code, so it is neither retried nor turned into an error here
// (requirement 14.8). Deciding what to do with the flag is the caller's job.
//
// The returned error, when there is one, is always an *ExitError, and the
// process exit code it carries distinguishes the three failure modes: the
// request itself was refused (ExitRequestError, requirement 14.7), the server
// could not be reached within the Retry_Window (ExitUnreachable, requirement
// 14.5), or the credentials could not be assembled at all (ExitRequestError,
// requirement 13.12).
func Call[Req any, Resp any](
	c *TaklerServiceClient,
	ctx context.Context,
	name string,
	kind CommandKind,
	req Req,
	invoke func(context.Context, Req, ...grpc.CallOption) (Resp, error),
) (Resp, error) {
	return callWith(c, ctx, name, kind, req, invoke, callSettings{})
}

// callSettings is the seam Call's tests reach through: the retry policy and the
// diagnostics sink, both of which Call itself takes from the process (the
// Retry_Window resolved from TAKLER_TIMEOUT, and the real standard error).
//
// It exists because an 86400 second Retry_Window is unobservable otherwise: a
// test injects a policy whose clock and sleep are fake, and the retry loop
// believes a day went by in microseconds. This is the same injection point the
// Python ServiceClient exposes as its clock and sleep constructor arguments,
// only reached through an unexported type here, so the exported surface stays
// the Call signature of the design.
//
// A zero callSettings is the production configuration.
type callSettings struct {
	// policy is the Retry_Window bookkeeping. nil means NewRetryPolicy(kind).
	policy *RetryPolicy

	// warn is where the per-retry line goes. nil means os.Stderr.
	warn io.Writer
}

// callWith is Call with the retry policy and the diagnostics sink supplied.
//
// The loop's structure, and the order of its three decisions, is the contract
// shared with Python's ServiceClient._call:
//
//  1. A non retryable status code returns immediately: the request itself is
//     wrong, so spending the Retry_Window on it only delays the error
//     (requirement 14.7).
//  2. A retryable status code asks the policy for the next delay. No delay means
//     the window is exhausted, which ends the call as unreachable (requirement
//     14.5); a delay means one line of diagnostics and one wait before the next
//     attempt (requirements 14.3, 14.6).
//  3. Anything else -- a response, with any flag -- returns as is (requirement
//     14.8).
func callWith[Req any, Resp any](
	c *TaklerServiceClient,
	ctx context.Context,
	name string,
	kind CommandKind,
	req Req,
	invoke func(context.Context, Req, ...grpc.CallOption) (Resp, error),
	settings callSettings,
) (Resp, error) {
	var zero Resp

	if ctx == nil {
		// A nil context would panic inside context.WithTimeout. Treating it as
		// "no deadline from the caller" keeps a caller's oversight from turning
		// a reachable server into a crash.
		ctx = context.Background()
	}

	policy := settings.policy
	if policy == nil {
		policy = NewRetryPolicy(kind)
	}
	warn := settings.warn
	if warn == nil {
		warn = os.Stderr
	}

	// The credentials are assembled once, before any attempt: they do not change
	// between attempts, and an unreadable secret file is a configuration error of
	// the request that no amount of retrying can fix, so it must surface before
	// the client touches the server at all (requirements 13.7, 13.12).
	md, err := c.Credentials().BuildMetadata(kind)
	if err != nil {
		return zero, err
	}
	callCtx := metadata.NewOutgoingContext(ctx, md)

	address := c.getServerAddress()
	started := policy.Now()

	for attempt := 1; ; attempt++ {
		response, err := callOnce(callCtx, policy.Timeout(), req, invoke)
		if err == nil {
			return response, nil
		}

		code := status.Code(err)
		if !IsRetryableStatus(code) {
			return zero, NewExitError(
				ExitCodeForStatus(code),
				fmt.Sprintf(
					"%s on server %s failed with gRPC status %v: %s",
					name, address, code, status.Convert(err).Message(),
				),
			)
		}

		elapsed := policy.Now().Sub(started)
		delay, ok := policy.NextDelay(attempt, elapsed)
		if !ok {
			// The window is over, which includes the Retry_Window == 0 case:
			// there the first failure already lands here, so exactly one attempt
			// happened (requirement 14.13).
			return zero, NewExitError(
				ExitUnreachable,
				fmt.Sprintf(
					"server %s is unreachable after %d attempts, last gRPC status %v",
					address, attempt, code,
				),
			)
		}

		fmt.Fprintf(
			warn,
			"retry %s to %s: elapsed=%.1fs, status=%v\n",
			name, address, elapsed.Seconds(), code,
		)
		policy.Sleep(delay)
	}
}

// callOnce makes one attempt under its own deadline (requirement 14.2).
//
// It is a function rather than the body of the loop so that the deferred cancel
// runs per attempt: deferring inside the loop would hold one live timer per
// attempt until the whole call returns, and a child command retrying for a day
// accumulates thousands of them. Cancelling once a unary call has returned is
// safe, because the response has been fully received by then.
func callOnce[Req any, Resp any](
	ctx context.Context,
	timeout time.Duration,
	req Req,
	invoke func(context.Context, Req, ...grpc.CallOption) (Resp, error),
) (Resp, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return invoke(attemptCtx, req)
}
