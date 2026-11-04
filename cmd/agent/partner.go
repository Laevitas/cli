package agent

import (
	"encoding/json"
	"github.com/laevitas/cli/internal/agentclient"
	"github.com/spf13/cobra"
)

func init() {
	var origin, signer, message, contextID, registryRPC string
	partner := &cobra.Command{Use: "partner", Short: "Inspect signed 6022 discovery and call its advertised A2A endpoint"}
	partner.PersistentFlags().StringVar(&origin, "origin", "", "6022 node HTTPS origin (required)")
	partner.PersistentFlags().StringVar(&signer, "signer", "", "EVM signer from the verified ENS/NFT identity")
	partner.PersistentFlags().StringVar(&registryRPC, "registry-rpc", "", "Optional HTTPS RPC for read-only 6022 registry checks (chain must match)")
	partner.AddCommand(&cobra.Command{Use: "inspect", Short: "Verify both discovery signatures without sending a message or payment", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		data, err := agentclient.InspectPartnerWithRPC(cmd.Context(), origin, signer, registryRPC)
		if err != nil {
			return commandError(cmd, err)
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"success": true, "data": data, "meta": map[string]any{"command": cmd.CommandPath()}})
	}})
	ask := &cobra.Command{Use: "ask", Short: "Send an A2A message; return any payment challenge without signing it", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		data, err := agentclient.AskPartner(cmd.Context(), origin, signer, message, contextID)
		if err != nil {
			return commandError(cmd, err)
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"success": true, "data": data, "meta": map[string]any{"command": cmd.CommandPath()}})
	}}
	ask.Flags().StringVar(&message, "message", "", "Message to the remote agent")
	ask.Flags().StringVar(&contextID, "context-id", "", "Conversation context returned by the agent")
	partner.AddCommand(ask)
	Cmd.AddCommand(partner)
}
