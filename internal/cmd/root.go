package cmd

import (
	"github.com/spf13/cobra"
)

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "cmgr",
		Short:         "Multi-session Claude orchestrator (claude-manager)",
		Long:          "cmgr drives multiple headless `claude -p` sessions on your subscription. One manager session coordinates per-repo worker sessions via a shared contract.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		projectCmd(),
		workerCmd(),
		sendCmd(),
		broadcastCmd(),
		logCmd(),
		statusCmd(),
		inboxCmd(),
		contractCmd(),
		detachedRunCmd(),
	)
	return root
}
