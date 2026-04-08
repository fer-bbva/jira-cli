package warmup

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ankitpokhrel/jira-cli/api"
	"github.com/ankitpokhrel/jira-cli/internal/cmdutil"
	"github.com/ankitpokhrel/jira-cli/pkg/jira"
)

// NewCmdWarmup is a warmup command.
func NewCmdWarmup() *cobra.Command {
	return &cobra.Command{
		Use:   "warmup",
		Short: "Warm up browser-backed Jira session",
		Long: `Warm up browser-backed Jira session.

This is mainly useful for cookie-based authentication behind SSO, reverse proxies,
or sticky load balancers where a valid browser session seed is not always enough
for the first REST request.`,
		Run: warmup,
	}
}

func warmup(cmd *cobra.Command, _ []string) {
	debug, err := cmd.Flags().GetBool("debug")
	cmdutil.ExitIfError(err)

	authType := viper.GetString("auth_type")
	if authType != string(jira.AuthTypeCookie) {
		cmdutil.Failed("This command is only for cookie-based authentication (current auth_type: %s)", authType)
		return
	}

	client := api.DefaultClient(debug)

	info, err := func() (*jira.ServerInfo, error) {
		s := cmdutil.Info("Warming REST session...")
		defer s.Stop()

		return client.WarmupSession()
	}()
	cmdutil.ExitIfError(err)

	cmdutil.Success(
		"REST session warmed on %s %s (build %d)",
		info.DeploymentType,
		info.Version,
		info.BuildNumber,
	)

	boardID := viper.GetInt("board.id")
	if boardID <= 0 {
		cmdutil.Warn("Agile warm-up skipped: no board.id configured.")
		return
	}

	err = func() error {
		s := cmdutil.Info(fmt.Sprintf("Warming agile session for board %d...", boardID))
		defer s.Stop()

		return client.WarmupAgileSession(boardID)
	}()
	if err != nil {
		cmdutil.Warn("REST session is ready, but agile warm-up failed: %s", err.Error())
		return
	}

	cmdutil.Success("Agile session warmed for board %d", boardID)
}
