package session

import (
	"github.com/spf13/cobra"

	"github.com/ankitpokhrel/jira-cli/internal/cmd/session/warmup"
)

// NewCmdSession is a session command.
func NewCmdSession() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage browser-backed Jira session state",
		Long:  "Manage browser-backed Jira session state used by cookie-based authentication.",
	}

	cmd.AddCommand(warmup.NewCmdWarmup())

	return cmd
}
