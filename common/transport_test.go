// Tests of the transport selection chain (M3 task 9).
//
// The chain mirrors the Python client's resolve_transport: the connect
// config's server.transport field outranks the TAKLER_TRANSPORT environment
// variable, gRPC is the default, matching is case-insensitive and tolerant of
// surrounding whitespace, and an unrecognized name degrades to the next source
// with one line of diagnostics rather than failing -- a typo must never strand
// a job script without a client.
package common

import (
	"bytes"
	"strings"
	"testing"
)

// lookupFrom is the lookupEnv double: it answers from a literal map.
func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	}
}

func TestResolveTransportDefaultsToGrpc(t *testing.T) {
	var warn bytes.Buffer

	if got := ResolveTransport("", lookupFrom(nil), &warn); got != TransportGrpc {
		t.Errorf("transport = %q, want %q", got, TransportGrpc)
	}
	if warn.Len() != 0 {
		t.Errorf("wrote %q, want silence when nothing is configured", warn.String())
	}
}

// The connect config outranks the environment variable, which is the address
// resolution chain's own ordering.
func TestResolveTransportPrecedence(t *testing.T) {
	var warn bytes.Buffer

	got := ResolveTransport(TransportHttp, lookupFrom(map[string]string{EnvTransport: TransportGrpc}), &warn)
	if got != TransportHttp {
		t.Errorf("transport = %q, want %q: the connect config outranks the environment", got, TransportHttp)
	}

	got = ResolveTransport("", lookupFrom(map[string]string{EnvTransport: TransportHttp}), &warn)
	if got != TransportHttp {
		t.Errorf("transport = %q, want %q from the environment", got, TransportHttp)
	}

	if warn.Len() != 0 {
		t.Errorf("wrote %q, want silence for recognized names", warn.String())
	}
}

// Matching is case-insensitive and tolerates surrounding whitespace, so
// "HTTP", " Http " and "http" select the same transport.
func TestResolveTransportToleratesCaseAndWhitespace(t *testing.T) {
	var warn bytes.Buffer

	if got := ResolveTransport(" HTTP ", lookupFrom(nil), &warn); got != TransportHttp {
		t.Errorf("transport = %q, want %q", got, TransportHttp)
	}
	if warn.Len() != 0 {
		t.Errorf("wrote %q, want silence for a tolerated spelling", warn.String())
	}
}

// An absent value at any level -- empty or whitespace-only -- lets the next
// source take effect without diagnostics.
func TestResolveTransportBlankFallsThrough(t *testing.T) {
	var warn bytes.Buffer

	got := ResolveTransport("  ", lookupFrom(map[string]string{EnvTransport: TransportHttp}), &warn)
	if got != TransportHttp {
		t.Errorf("transport = %q, want %q: a blank config value falls through to the environment", got, TransportHttp)
	}
	if warn.Len() != 0 {
		t.Errorf("wrote %q, want silence for a blank value", warn.String())
	}

	warn.Reset()
	got = ResolveTransport("", lookupFrom(map[string]string{EnvTransport: "\t"}), &warn)
	if got != TransportGrpc {
		t.Errorf("transport = %q, want %q: a blank environment value falls through to the default", got, TransportGrpc)
	}
	if warn.Len() != 0 {
		t.Errorf("wrote %q, want silence for a blank value", warn.String())
	}
}

// An unrecognized name degrades to the next source with exactly one
// diagnostics line naming the offending value and its source, at every level.
func TestResolveTransportInvalidNameDegradesWithWarning(t *testing.T) {
	t.Run("config value degrades to the environment", func(t *testing.T) {
		var warn bytes.Buffer

		got := ResolveTransport("grpcc", lookupFrom(map[string]string{EnvTransport: TransportHttp}), &warn)

		if got != TransportHttp {
			t.Errorf("transport = %q, want %q from the environment", got, TransportHttp)
		}
		lines := strings.Split(strings.TrimRight(warn.String(), "\n"), "\n")
		if len(lines) != 1 {
			t.Fatalf("wrote %d lines, want exactly 1: %q", len(lines), warn.String())
		}
		for _, want := range []string{"grpcc", "connect config", TransportGrpc, TransportHttp} {
			if !strings.Contains(lines[0], want) {
				t.Errorf("warning %q does not contain %q", lines[0], want)
			}
		}
	})

	t.Run("environment value degrades to the default", func(t *testing.T) {
		var warn bytes.Buffer

		got := ResolveTransport("", lookupFrom(map[string]string{EnvTransport: "htp"}), &warn)

		if got != TransportGrpc {
			t.Errorf("transport = %q, want the default %q", got, TransportGrpc)
		}
		if !strings.Contains(warn.String(), EnvTransport) {
			t.Errorf("warning %q does not name %s", warn.String(), EnvTransport)
		}
		if !strings.Contains(warn.String(), "htp") {
			t.Errorf("warning %q does not name the offending value", warn.String())
		}
	})
}
