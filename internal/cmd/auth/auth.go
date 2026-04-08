package auth

import (
	"github.com/spf13/cobra"

	"github.com/ankitpokhrel/jira-cli/internal/cmd/auth/sso"
)

// NewCmdAuth is the auth command namespace.
func NewCmdAuth() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate and refresh Jira sessions",
		Long:  "Authenticate and refresh Jira sessions, especially for browser-backed SSO setups.",
	}

	cmd.AddCommand(sso.NewCmdSSO())

	return cmd
}