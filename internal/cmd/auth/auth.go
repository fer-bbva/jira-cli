package auth

import (
	"github.com/spf13/cobra"

	"github.com/ankitpokhrel/jira-cli/internal/cmd/auth/sso"
)

// NewCmdAuth is the auth command namespace.
func NewCmdAuth() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate and inspect Jira browser sessions",
		Long:  "Authenticate and inspect Jira browser-backed SSO sessions.",
	}

	cmd.AddCommand(
		sso.NewCmdSSO(),
		sso.NewCmdStatus(),
		sso.NewCmdReauth(),
	)

	return cmd
}
