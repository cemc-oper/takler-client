package cmd

import "testing"

func TestChildIdentityResolutionPrecedence(t *testing.T) {
	t.Setenv(TaklerAttemptID, "123e4567-e89b-42d3-a456-426614174000")
	t.Setenv(TaklerName, "/f/source")
	if got := getAttemptID(""); got != "123e4567-e89b-42d3-a456-426614174000" {
		t.Fatalf("environment attempt = %q", got)
	}
	if got := getAttemptID("explicit"); got != "explicit" {
		t.Fatalf("explicit attempt = %q", got)
	}
	if got := getSourceTaskPath("", "/f/target"); got != "/f/source" {
		t.Fatalf("environment source = %q", got)
	}
	if got := getSourceTaskPath("/f/explicit", "/f/target"); got != "/f/explicit" {
		t.Fatalf("explicit source = %q", got)
	}
	t.Setenv(TaklerName, "")
	if got := getSourceTaskPath("", "/f/target"); got != "/f/target" {
		t.Fatalf("target fallback = %q", got)
	}
}
