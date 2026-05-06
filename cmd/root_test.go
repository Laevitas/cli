package cmd

import (
	"fmt"
	"strings"
	"testing"
)

func TestAugmentUnknownCommandSkipsExistingSuggestion(t *testing.T) {
	err := fmt.Errorf("unknown command %q for %q\n\nDid you mean this?\n\tcommands", "comands", "laevitas")
	got := augmentUnknownCommandError(err, []string{"comands"})
	if got.Error() != err.Error() {
		t.Fatalf("augmentUnknownCommandError duplicated existing suggestion:\n%s", got)
	}
}

func TestAugmentUnknownFlagAfterCommandTypo(t *testing.T) {
	got := augmentUnknownCommandError(
		fmt.Errorf("unknown flag: --currency"),
		[]string{"perps", "snapshto", "--currency", "BTC"},
	)
	if !strings.Contains(got.Error(), "Did you mean this?\n\tsnapshot") {
		t.Fatalf("missing snapshot suggestion:\n%s", got)
	}
}

func TestAugmentUnknownFlagDoesNotRewriteRealFlagError(t *testing.T) {
	err := fmt.Errorf("unknown flag: --bogus")
	got := augmentUnknownCommandError(err, []string{"perps", "snapshot", "--bogus"})
	if got.Error() != err.Error() {
		t.Fatalf("augmentUnknownCommandError rewrote a real flag error:\n%s", got)
	}
}
