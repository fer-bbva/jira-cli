package sso

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/zalando/go-keyring"

	"github.com/ankitpokhrel/jira-cli/internal/cmdutil"
	"github.com/ankitpokhrel/jira-cli/pkg/jira"
)

// NewCmdSSO authenticates against Jira using the configured SSO flow.
func NewCmdSSO() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sso",
		Short: "Sign in to Jira through SSO and store the resulting browser session",
		Long: `Sign in to Jira through SSO and store the resulting browser session.

This command is intended for cookie-based authentication setups behind corporate SSO.
	It uses Playwright to complete the browser login flow and stores the resulting Jira session in the keychain.`,
		Run: authenticate,
	}

	return cmd
}

func NewCmdStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the status of the stored Jira browser session",
		Long:  "Show whether the Jira browser-backed SSO session stored in the keychain is still valid.",
		Run:   status,
	}
}

func NewCmdReauth() *cobra.Command {
	return &cobra.Command{
		Use:   "reauth",
		Short: "Force a fresh Jira SSO login with Playwright",
		Long:  "Force a fresh Jira SSO login with Playwright and replace the stored browser-backed session.",
		Run:   reauth,
	}
}

func authenticate(cmd *cobra.Command, _ []string) {
	server, login := cookieAuthContext()

	if _, _, ok := existingStoredSession(server, login); ok {
		return
	}

	requirePlaywrightCLI("jira auth sso")

	me, sessionCookie, err := authenticateViaPlaywright(server, configuredLogin(login))
	if err != nil {
		cmdutil.Failed("Playwright-assisted SSO failed: %s", err.Error())
		return
	}

	persistAuthenticatedSession(me, sessionCookie, login)
}

func status(_ *cobra.Command, _ []string) {
	server, login := cookieAuthContext()

	me, _, err := storedSession(server, login)
	if err != nil {
		cmdutil.Failed("Stored Jira session is not ready: %s", err.Error())
		return
	}

	cmdutil.Success("Stored Jira session is valid for %s (%s)", me.Name, me.Login)
	_, _ = fmt.Fprintf(os.Stdout, "server: %s\nlogin: %s\nsource: keychain\nplaywright: %t\n", server, login, playwrightCLIAvailable())
}

func reauth(_ *cobra.Command, _ []string) {
	server, login := cookieAuthContext()
	requirePlaywrightCLI("jira auth reauth")

	me, sessionCookie, err := authenticateViaPlaywright(server, configuredLogin(login))
	if err != nil {
		cmdutil.Failed("Playwright-assisted reauth failed: %s", err.Error())
		return
	}

	persistAuthenticatedSession(me, sessionCookie, login)
}

func requirePlaywrightCLI(command string) {
	if _, err := playwrightCLICommand(); err != nil {
		cmdutil.Failed("%s requires Playwright CLI: %s", command, err.Error())
	}
}

func cookieAuthContext() (string, string) {
	authType := viper.GetString("auth_type")
	if authType != string(jira.AuthTypeCookie) {
		cmdutil.Failed("This command requires auth_type=cookie in your config (current auth_type: %s)", authType)
	}

	server := strings.TrimSpace(viper.GetString("server"))
	if server == "" {
		cmdutil.Failed("Missing server in config. Run 'jira init --auth-type cookie' first.")
	}

	login := configuredLogin(viper.GetString("login"))
	if login == "" {
		cmdutil.Failed("Missing login in config for cookie auth. Run 'jira init --auth-type cookie' first.")
	}

	return server, login
}

func storedSession(server, configured string) (*jira.Me, string, error) {
	configuredLogin := strings.TrimSpace(configured)
	if configuredLogin == "" {
		return nil, "", fmt.Errorf("missing login in config")
	}

	sessionCookie, err := keyring.Get("jira-cli", configuredLogin)
	if err != nil || strings.TrimSpace(sessionCookie) == "" {
		return nil, "", fmt.Errorf("no stored Jira session found in keychain for '%s'", configuredLogin)
	}

	client := jira.NewClient(jira.Config{
		Server:   server,
		APIToken: sessionCookie,
		AuthType: &[]jira.AuthType{jira.AuthTypeCookie}[0],
	})
	me, err := client.Me()
	if err != nil {
		return nil, "", fmt.Errorf("stored Jira session is missing or expired: %w", err)
	}
	if me.Login != configuredLogin {
		return nil, "", fmt.Errorf("stored Jira session belongs to '%s' but config expects '%s'", me.Login, configuredLogin)
	}

	return me, sessionCookie, nil
}

func persistAuthenticatedSession(me *jira.Me, sessionCookie, configured string) {
	configuredLogin := strings.TrimSpace(configured)
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
}

func existingStoredSession(server, configured string) (*jira.Me, string, bool) {
	me, sessionCookie, err := storedSession(server, configured)
	if err != nil {
		return nil, "", false
	}

	cmdutil.Success("Existing Jira session is still valid for %s (%s)", me.Name, me.Login)
	return me, sessionCookie, true
}

func configuredLogin(login string) string {
	return strings.TrimSpace(login)
}
