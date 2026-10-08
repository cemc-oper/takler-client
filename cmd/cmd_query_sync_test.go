package cmd

import "testing"

func TestSyncCommandIsRegisteredWithPollingFlags(t *testing.T) {
	root := newCommandsBuilder().addAll().build().getCommand()
	command, _, err := root.Find([]string{"sync"})
	if err != nil || command == nil || command.Name() != "sync" {
		t.Fatalf("sync command = %v, %v", command, err)
	}
	for _, name := range []string{"scope", "flow", "depth", "polls", "interval"} {
		if command.Flags().Lookup(name) == nil {
			t.Fatalf("missing --%s", name)
		}
	}
}
