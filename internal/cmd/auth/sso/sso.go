package sso

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/zalando/go-keyring"

	"github.com/ankitpokhrel/jira-cli/internal/cmdutil"
	jiraConfig "github.com/ankitpokhrel/jira-cli/internal/config"
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
		persistCookieAuthConfig(server, login)
		return
	}

	requirePlaywrightCLI("jira auth sso")

	me, sessionCookie, err := authenticateViaPlaywright(server, login)
	if err != nil {
		cmdutil.Failed("Playwright-assisted SSO failed: %s", err.Error())
		return
	}

	persistCookieAuthConfig(server, login)
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

	me, sessionCookie, err := authenticateViaPlaywright(server, login)
	if err != nil {
		cmdutil.Failed("Playwright-assisted reauth failed: %s", err.Error())
		return
	}

	persistCookieAuthConfig(server, login)
	persistAuthenticatedSession(me, sessionCookie, login)
}

func requirePlaywrightCLI(command string) {
	if _, err := playwrightCLICommand(); err != nil {
		cmdutil.Failed("%s requires Playwright CLI: %s", command, err.Error())
	}
}

func cookieAuthContext() (string, string) {
	authType := viper.GetString("auth_type")
	if authType != "" && authType != string(jira.AuthTypeCookie) {
		cmdutil.Warn("Config auth_type=%s will be updated to cookie for browser-backed SSO.", authType)
	}

	server := strings.TrimSpace(viper.GetString("server"))
	login := strings.TrimSpace(viper.GetString("login"))

	var err error
	server, login, err = promptCookieAuthBootstrap(server, login)
	if err != nil {
		cmdutil.Failed("Failed to read cookie auth settings: %s", err.Error())
	}

	return server, login
}

func promptCookieAuthBootstrap(server, login string) (string, string, error) {
	questions := make([]*survey.Question, 0, 2)

	if strings.TrimSpace(server) == "" {
		questions = append(questions, &survey.Question{
			Name: "server",
			Prompt: &survey.Input{
				Message: "Link to Jira server:",
				Help:    "This is the Jira base URL, for example https://jira.globaldevtools.bbva.com.",
			},
			Validate: func(val interface{}) error {
				str, ok := val.(string)
				if !ok || strings.TrimSpace(str) == "" {
					return fmt.Errorf("not a valid URL")
				}
				parsed, err := url.Parse(strings.TrimSpace(str))
				if err != nil || parsed.Scheme == "" || parsed.Host == "" {
					return fmt.Errorf("not a valid URL")
				}
				if parsed.Scheme != "http" && parsed.Scheme != "https" {
					return fmt.Errorf("not a valid URL")
				}
				return nil
			},
		})
	}

	if strings.TrimSpace(login) == "" {
		questions = append(questions, &survey.Question{
			Name: "login",
			Prompt: &survey.Input{
				Message: "Jira login username:",
				Help:    "This is the login that jira-cli will use to find the browser-backed session in the keychain.",
			},
			Validate: survey.Required,
		})
	}

	if len(questions) == 0 {
		return strings.TrimSpace(server), strings.TrimSpace(login), nil
	}

	answers := struct {
		Server string
		Login  string
	}{}

	if err := survey.Ask(questions, &answers); err != nil {
		return "", "", err
	}

	if strings.TrimSpace(server) == "" {
		server = answers.Server
	}
	if strings.TrimSpace(login) == "" {
		login = answers.Login
	}

	return strings.TrimSpace(server), strings.TrimSpace(login), nil
}

func persistCookieAuthConfig(server, login string) {
	path, err := jiraCLIConfigPath()
	if err != nil {
		cmdutil.Failed("Failed to resolve jira config path: %s", err.Error())
		return
	}

	config := viper.New()
	config.SetConfigFile(path)
	config.SetConfigType(configFileType(path))

	if jiraConfig.Exists(path) {
		if err := config.ReadInConfig(); err != nil {
			cmdutil.Failed("Failed to read jira config %s: %s", path, err.Error())
			return
		}
	}

	config.Set("auth_type", jira.AuthTypeCookie.String())
	config.Set("server", strings.TrimSpace(server))
	config.Set("login", strings.TrimSpace(login))
	if strings.TrimSpace(config.GetString("installation")) == "" {
		config.Set("installation", inferInstallationFromServer(server))
	}

	if !jiraConfig.Exists(path) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			cmdutil.Failed("Failed to create jira config directory: %s", err.Error())
			return
		}
		if err := config.WriteConfigAs(path); err != nil {
			cmdutil.Failed("Failed to write jira config %s: %s", path, err.Error())
			return
		}
		cmdutil.Success("Created Jira config at %s", path)
		return
	}

	if err := config.WriteConfig(); err != nil {
		cmdutil.Failed("Failed to update jira config %s: %s", path, err.Error())
	}
}

func jiraCLIConfigPath() (string, error) {
	if path := strings.TrimSpace(viper.GetString("config")); path != "" {
		return normalizeConfigPath(path), nil
	}
	if path := strings.TrimSpace(os.Getenv("JIRA_CONFIG_FILE")); path != "" {
		return normalizeConfigPath(path), nil
	}
	if path := strings.TrimSpace(viper.ConfigFileUsed()); path != "" {
		return path, nil
	}

	home, err := cmdutil.GetConfigHome()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, jiraConfig.Dir, jiraConfig.FileName+"."+jiraConfig.FileType), nil
}

func normalizeConfigPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return trimmed
	}
	if ext := strings.ToLower(filepath.Ext(trimmed)); ext == ".yml" || ext == ".yaml" {
		return trimmed
	}
	return trimmed + "." + jiraConfig.FileType
}

func configFileType(path string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if ext == "yaml" {
		return ext
	}
	if ext == jiraConfig.FileType {
		return ext
	}
	return jiraConfig.FileType
}

func inferInstallationFromServer(server string) string {
	parsed, err := url.Parse(strings.TrimSpace(server))
	if err == nil && strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".atlassian.net") {
		return jira.InstallationTypeCloud
	}
	return jira.InstallationTypeLocal
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
	if me.Login != configuredLogin {
		cmdutil.Failed("SSO session belongs to user '%s' but config expects '%s'", me.Login, configuredLogin)
		return
	}

	if err := keyring.Set("jira-cli", configuredLogin, sessionCookie); err != nil {
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
