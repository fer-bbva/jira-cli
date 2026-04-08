package sso

import (
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/zalando/go-keyring"

	"github.com/ankitpokhrel/jira-cli/internal/cmdutil"
	"github.com/ankitpokhrel/jira-cli/pkg/jira"
)

// NewCmdSSO authenticates against Jira using the configured SSO flow.
func NewCmdSSO() *cobra.Command {
	return &cobra.Command{
		Use:   "sso",
		Short: "Sign in to Jira through SSO and store the resulting browser session",
		Long: `Sign in to Jira through SSO and store the resulting browser session.

This command is intended for cookie-based authentication setups behind corporate SSO.
It prompts for username and password, waits for second-factor approval, and stores the final Jira session in the keychain.`,
		Run: authenticate,
	}
}

func authenticate(cmd *cobra.Command, _ []string) {
	authType := viper.GetString("auth_type")
	if authType != string(jira.AuthTypeCookie) {
		cmdutil.Failed("jira auth sso requires auth_type=cookie in your config (current auth_type: %s)", authType)
		return
	}

	server := viper.GetString("server")
	login := strings.TrimSpace(viper.GetString("login"))
	if server == "" {
		cmdutil.Failed("Missing server in config. Run 'jira init --auth-type cookie' first.")
		return
	}

	answers := struct {
		Username string
		Password string
	}{}

	questions := []*survey.Question{
		{
			Name: "username",
			Prompt: &survey.Input{
				Message: "SSO username:",
				Default: login,
				Help:    "Corporate username used in the BBVA IdP login form.",
			},
			Validate: survey.Required,
		},
		{
			Name: "password",
			Prompt: &survey.Password{
				Message: "SSO password:",
				Help:    "Your corporate password. It is only used for the live SSO request and is not stored.",
			},
			Validate: survey.Required,
		},
	}

	if err := survey.Ask(questions, &answers); err != nil {
		cmdutil.Failed("Failed to read SSO credentials: %s", err.Error())
		return
	}

	client := jira.NewClient(jira.Config{
		Server:   server,
		Login:    login,
		AuthType: &[]jira.AuthType{jira.AuthTypeCookie}[0],
		Insecure: &[]bool{viper.GetBool("insecure")}[0],
		Debug:    viper.GetBool("debug"),
	})

	fmt.Println("Starting SSO login for", server)
	cmdutil.Warn("Approve the BBVA authentication request on your work phone when it appears.")

	s := cmdutil.Info("Completing SSO login...")
	me, sessionCookie, err := client.AuthenticateSSO(strings.TrimSpace(answers.Username), answers.Password)
	if err != nil {
		s.Stop()
		cmdutil.Failed("SSO login failed: %s", err.Error())
		return
	}
	s.Stop()

	configuredLogin := strings.TrimSpace(login)
	if configuredLogin != "" && me.Login != configuredLogin {
		cmdutil.Failed("SSO session belongs to user '%s' but config expects '%s'", me.Login, configuredLogin)
		return
	}

	keyringLogin := me.Login
	if configuredLogin != "" {
		keyringLogin = configuredLogin
	}

	if err := keyring.Set("jira-cli", keyringLogin, sessionCookie); err != nil {
		cmdutil.Failed("Failed to store browser session in keychain: %s", err.Error())
		return
	}

	cmdutil.Success("SSO login completed for %s (%s)", me.Name, me.Login)
	cmdutil.Warn("Your browser-backed Jira session is now stored in the keychain. Run 'jira session warmup' if the first request still needs reheating.")
}