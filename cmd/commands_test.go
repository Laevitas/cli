package cmd

import "testing"

func TestCommandManifestSkipsHiddenCommands(t *testing.T) {
	manifest := buildCommandManifest(rootCmd, "")
	for _, entry := range manifest.Commands {
		if entry.Path == "dash demo" {
			t.Fatalf("hidden command %q leaked into manifest", entry.Path)
		}
	}
}
