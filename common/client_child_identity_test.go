package common

import (
	"errors"
	"testing"
)

func TestChildMethodsRejectMissingAttemptBeforeDial(t *testing.T) {
	var client *TaklerServiceClient
	calls := map[string]func() error{
		"init":     func() error { _, err := client.RunCommandInit("/f/t", "job", ""); return err },
		"complete": func() error { _, err := client.RunCommandComplete("/f/t", ""); return err },
		"abort":    func() error { _, err := client.RunCommandAbort("/f/t", "failed", ""); return err },
		"event":    func() error { _, err := client.RunCommandEvent("/f", "ready", "", "/f/t"); return err },
		"meter":    func() error { _, err := client.RunCommandMeter("/f", "progress", "50", "", "/f/t"); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			var exit *ExitError
			if err := call(); !errors.As(err, &exit) || exit.Code != ExitRequestError {
				t.Fatalf("missing attempt: got %v, want request error", err)
			}
		})
	}
}

func TestChildOptionsUseCanonicalAttemptAndSource(t *testing.T) {
	options, err := childOptions("/f/c", testAttemptID, "/f/c/t")
	if err != nil {
		t.Fatal(err)
	}
	if options.GetAttemptId() != testAttemptID || options.GetSourceTaskPath() != "/f/c/t" {
		t.Fatalf("identity not encoded: %v", options)
	}
	for _, invalid := range []string{"", "not-a-uuid", "123E4567-E89B-42D3-A456-426614174000"} {
		if _, err := childOptions("/f/c", invalid, "/f/c/t"); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
}
