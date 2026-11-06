//go:build !js

package cmd

import (
	"github.com/laevitas/cli/cmd/dash"
	wscmd "github.com/laevitas/cli/cmd/ws"
)

func registerPlatformCommands() {
	rootCmd.AddCommand(wscmd.Cmd)
	rootCmd.AddCommand(dash.Cmd)
	rootCmd.AddCommand(watchCmd)
}
