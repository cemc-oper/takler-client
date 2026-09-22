// The Go Call_Wrapper: the single path every call of this client takes.
//
// retry.go answers "whether and how long to wait", credentials.go answers "what
// credentials does this call carry", exitcode.go answers "what does a failure
// exit with". This file is the plumbing that puts the three together and hands
// the attempt loop to RunWithRetry, the transport-neutral decision layer of
// retry.go: what is gRPC specific here -- the metadata the credentials become
// and the classifyGrpcError mapping -- is exactly what another transport
// replaces, nothing more (M3 task 9).
//
// Everything a command method used to repeat -- a hardcoded per-call timeout, a
// log.Fatalf on failure, no retry at all -- lives here once, so a command method
// is left with building a request and reading a response (requirements 14.1,
// 13.7).
//
// The attempt loop mirrors the Python client's run_with_retry in
// takler/client/retry.py, which is the reference implementation of the
// cross-language contract. Any change to the classification, the ordering of the
// checks or the outcome of an exhausted window must be applied there as well.
//
// Requirements: 14.1, 14.3, 14.5, 14.6, 14.7, 14.8, 14.13, 13.7.
package common

import (
	"context"
	"io"
	"os"
	"time"

	"google.golang.org/grpc/metadata"
)

// CallCommand opens the transport, sends req through Call and closes the
// transport again, returning whatever the server answered.
//
// It is what a command method calls, and it is the reason a command method is
// down to building a request and reading a response (requirement 14.1): the
// connection lifetime lives in withTransport, the per-attempt timeout, the
// retry window and the credential injection live in Call, and neither appears
// at the call site.
//
// invoke is a method expression of the Transport interface, e.g.
// Transport.RunCommandInit. Spelling it that way rather than as a method value
// is what lets a call site name the command in one identifier: the receiver is
// not known before the connection exists, so a method value would have to be
// produced inside a closure, and that closure's signature would have to be
// written out in full at every one of the sixteen call sites. The generated
// pb.TaklerServerClient interface no longer appears here at all -- the
// transport behind the interface is the gRPC one today and the HTTP one of M3
// task 10 tomorrow, and no call site can tell the difference. Both type
// parameters are inferred from the method expression together with req.
//
// The context is the process-wide background one: a command method has no
// caller-supplied deadline to honour, and the deadlines that matter -- the per
// attempt timeout and the Retry_Window -- are the policy's, inside Call.
func CallCommand[Req any, Resp any](
	c *TaklerServiceClient,
	name string,
	kind CommandKind,
	req Req,
	invoke func(Transport, context.Context, Req) (Resp, error),
) (Resp, error) {
	var response Resp

	err := c.withTransport(func(transport Transport) error {
		var err error
		response, err = Call(
			c, context.Background(), name, kind, req,
			func(ctx context.Context, request Req) (Resp, error) {
				return invoke(transport, ctx, request)
			},
		)
		return err
	})
	if err != nil {
		var zero Resp
		return zero, err
	}

	return response, nil
}

// Call invokes invoke with a per-attempt timeout, backoff retry, credential
// injection and error to exit code mapping (requirement 14.1).
//
// The type parameters keep each command's concrete request and response types,
// so no caller casts an interface{} back to what it already knew it had. invoke
// is meant to be a Transport method bound to an open transport, e.g. produced
// from Transport.RunCommandInit; both type parameters are inferred from it.
//
// c supplies the credentials (requirement 13.7) and the server address that
// appears in diagnostics. It is not connected here: the connection belongs to
// withTransport, and invoke is a method of the transport it opened, which keeps
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
// whose flag is non zero: that is a *successful* call carrying the server's
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
	invoke func(context.Context, Req) (Resp, error),
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
// The gRPC specific parts are exactly two: the credentials become gRPC
// metadata on the outgoing context, and a failed attempt is classified by
// classifyGrpcError. The attempt loop itself is RunWithRetry's, shared with
// every future transport.
func callWith[Req any, Resp any](
	c *TaklerServiceClient,
	ctx context.Context,
	name string,
	kind CommandKind,
	req Req,
	invoke func(context.Context, Req) (Resp, error),
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

	return RunWithRetry(
		policy, warn, name, c.getServerAddress(),
		func() (Resp, error) {
			return callOnce(callCtx, policy.Timeout(), req, invoke)
		},
		classifyGrpcError,
	)
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
	invoke func(context.Context, Req) (Resp, error),
) (Resp, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return invoke(attemptCtx, req)
}
