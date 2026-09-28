package common

import (
	"context"
	"crypto/x509"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"net/url"
	"testing"
	"time"
)

func TestTLSAndConfigurationFailuresAreNeverRetried(t *testing.T) {
	for _, wire := range []string{"http", "grpc"} {
		for _, name := range []string{"ping", "show", "coroutine"} {
			t.Run(wire+"/"+name, func(t *testing.T) {
				client, buildErr := NewTaklerServiceClient("localhost", "33083", wire, SecurityLevels{})
				if buildErr != nil {
					t.Fatal(buildErr)
				}
				var failure error = status.Error(codes.Unavailable, "TLS handshake: certificate verify failed")
				if wire == "http" {
					client.transport = NewHttpTransport("localhost", "33083", SecurityLevels{})
					failure = x509.UnknownAuthorityError{}
				}
				count := 0
				policy := &RetryPolicy{RetryWindow: time.Minute, sleep: func(time.Duration) { t.Fatal("TLS failure retried") }}
				_, err := callWith(client, context.Background(), name, KindQuery, 0, func(context.Context, int) (int, error) { count++; return 0, failure }, callSettings{policy: policy, warn: io.Discard})
				assertExitError(t, err, ExitRequestError)
				if count != 1 {
					t.Fatalf("attempts=%d", count)
				}
			})
		}
	}
	if v := classifyHttpError(&url.Error{Op: "Post", URL: "invalid://host", Err: errors.New("unsupported protocol scheme")}); v.Retryable || v.ExitCode != ExitRequestError {
		t.Fatalf("configuration verdict=%+v", v)
	}
}
