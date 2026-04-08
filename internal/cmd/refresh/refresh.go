package refresh

import (
	"fmt"

	"github.com/AlecAivazis/survey/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/zalando/go-keyring"

	"github.com/ankitpokhrel/jira-cli/internal/cmdutil"
	"github.com/ankitpokhrel/jira-cli/pkg/jira"
)

// NewCmdRefresh is a refresh command.
func NewCmdRefresh() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Refresh browser session for cookie-based authentication",
		Long: `Refresh browser session for cookie-based authentication.

This command is only applicable when using 'cookie' auth type.
It allows you to update the browser session seed without re-running the full 'jira init' setup.`,
		Run: refresh,
	}

	cmd.Flags().String("from-har", "", "Import the Cookie header automatically from an authenticated HAR file")
	cmd.Flags().String("from-curl", "", "Import the Cookie header automatically from a copied cURL command file")

	return cmd
}

func refresh(cmd *cobra.Command, _ []string) {
	authType := viper.GetString("auth_type")
	if authType != string(jira.AuthTypeCookie) {
		cmdutil.Failed("This command is only for cookie-based authentication (current auth_type: %s)", authType)
		return
	}

	server := viper.GetString("server")
	login := viper.GetString("login")

	if server == "" || login == "" {
		cmdutil.Failed("Missing server or login in config. Please run 'jira init' first.")
		return
	}

	fmt.Println("Refresh browser-backed session for", server)
	fmt.Println()
	fmt.Println("1. Open", server, "in a browser")
	fmt.Println("2. Sign in (authenticate via SSO/certificate as needed)")
	fmt.Println("3. Open DevTools → Network and copy the Cookie header from a working Jira REST/XHR request")
	fmt.Println("4. Paste the full Cookie header value (recommended) or at least JSESSIONID")
	fmt.Println()

	var sessionCookie string
	fromHAR, err := cmd.Flags().GetString("from-har")
	cmdutil.ExitIfError(err)
	fromCurl, err := cmd.Flags().GetString("from-curl")
	cmdutil.ExitIfError(err)

	if fromHAR != "" && fromCurl != "" {
		cmdutil.Failed("Use only one of --from-har or --from-curl")
		return
	}

	if fromCurl != "" {
		s := cmdutil.Info("Extracting browser session from cURL command...")
		sessionCookie, err = jira.ExtractCookieTokenFromCurl(fromCurl)
		s.Stop()
		if err != nil {
			cmdutil.Failed("Failed to extract Cookie header from cURL command: %s", err.Error())
			return
		}
		cmdutil.Success("Imported Cookie header from cURL command: %s", fromCurl)
	} else if fromHAR != "" {
		s := cmdutil.Info("Extracting browser session from HAR...")
		sessionCookie, err = jira.ExtractCookieTokenFromHAR(fromHAR, server)
		s.Stop()
		if err != nil {
			cmdutil.Failed("Failed to extract Cookie header from HAR: %s\nTip: Chrome HAR exports may omit cookies; use --from-curl with a 'Copy as cURL' request instead.", err.Error())
			return
		}
		cmdutil.Success("Imported Cookie header from HAR: %s", fromHAR)
	} else {
		prompt := &survey.Password{
			Message: "Paste Cookie header or JSESSIONID value:",
			Help:    "If Jira sits behind SSO or a load balancer, paste the full Cookie header value from a working request.",
		}

		if err := survey.AskOne(prompt, &sessionCookie, survey.WithValidator(survey.Required)); err != nil {
			cmdutil.Failed("Failed to read input: %s", err.Error())
			return
		}
	}

	sessionCookie = jira.NormalizeCookieToken(sessionCookie)

	s := cmdutil.Info("Validating browser session...")

	client := jira.NewClient(jira.Config{
		Server:   server,
		APIToken: sessionCookie,
		AuthType: &[]jira.AuthType{jira.AuthTypeCookie}[0],
	})

	_, _ = client.WarmupSession()

	me, err := client.Me()
	if err != nil {
		s.Stop()
		cmdutil.Failed("Failed to validate cookie session: %s", err.Error())
		return
	}
	s.Stop()

	if me.Login != login {
		cmdutil.Failed("Cookie session belongs to user '%s' but config expects '%s'", me.Login, login)
		return
	}

	if err := keyring.Set("jira-cli", login, sessionCookie); err != nil {
		cmdutil.Failed("Failed to store browser session in keychain: %s", err.Error())
		return
	}

	cmdutil.Success("Browser session refreshed for %s (%s)", me.Name, me.Login)
	cmdutil.Warn("Tip: run 'jira session warmup' if the next command still needs a session reheat.")
}