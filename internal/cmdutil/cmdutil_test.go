package cmdutil

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestNamedArgs(t *testing.T) {
	newCmd := func(example string) *cobra.Command {
		return &cobra.Command{
			Use:     "book <market> <symbol-or-currency>",
			Example: example,
		}
	}

	tests := []struct {
		name    string
		args    []string
		example string
		wantErr string
	}{
		{
			name:    "zero args names first missing arg",
			args:    nil,
			example: "\n  laevitas dash book perpetuals BTC --margin linear\n",
			wantErr: "book: missing argument <market>\n\n  Usage:    book <market> <symbol-or-currency>\n  Example:  laevitas dash book perpetuals BTC --margin linear",
		},
		{
			name:    "one arg names second missing arg",
			args:    []string{"perpetuals"},
			example: "  laevitas dash book perpetuals BTC --margin linear",
			wantErr: "book: missing argument <symbol-or-currency>\n\n  Usage:    book <market> <symbol-or-currency>\n  Example:  laevitas dash book perpetuals BTC --margin linear",
		},
		{
			name: "exact args pass",
			args: []string{"perpetuals", "BTC"},
		},
		{
			name:    "too many args lists expected placeholders",
			args:    []string{"perpetuals", "BTC", "extra"},
			example: "  laevitas dash book perpetuals BTC --margin linear",
			wantErr: "book: too many arguments (got 3, expected 2: <market> <symbol-or-currency>)",
		},
		{
			name:    "empty example omitted",
			args:    nil,
			example: "",
			wantErr: "book: missing argument <market>\n\n  Usage:    book <market> <symbol-or-currency>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NamedArgs("market", "symbol-or-currency")(newCmd(tt.example), tt.args)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("NamedArgs returned error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("NamedArgs returned nil error")
			}
			if got := err.Error(); got != tt.wantErr {
				t.Fatalf("error = %q, want %q", got, tt.wantErr)
			}
		})
	}
}
