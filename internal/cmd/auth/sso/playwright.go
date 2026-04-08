package sso

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ankitpokhrel/jira-cli/internal/cmdutil"
	"github.com/ankitpokhrel/jira-cli/pkg/jira"
)

const playwrightSessionName = "jira"

const (
	playwrightLoginTimeout  = 5 * time.Minute
	playwrightPollInterval  = 3 * time.Second
)

func playwrightCLIAvailable() bool {
	_, err := playwrightCLICommand()
	return err == nil
}

func authenticateViaPlaywright(server, expectedLogin string) (*jira.Me, string, error) {
	workspaceDir, err := playwrightWorkspaceDir()
	if err != nil {
		return nil, "", err
	}
	profileDir, err := playwrightProfileDir()
	if err != nil {
		return nil, "", err
	}
	statePath, err := playwrightStatePath(workspaceDir)
	if err != nil {
		return nil, "", err
	}

	cmdutil.Warn("Playwright CLI detected. Opening an isolated Jira browser session for interactive SSO login.")
	cmdutil.Warn("Complete the BBVA login there, including the mobile second factor. I will detect the Jira session automatically.")

	_ = closePlaywrightSession(workspaceDir)
	defer func() { _ = closePlaywrightSession(workspaceDir) }()

	if _, err := runPlaywrightCLIInDir(workspaceDir, "-s="+playwrightSessionName, "open", server, "--browser=chrome", "--headed", "--profile="+profileDir); err != nil {
		return nil, "", fmt.Errorf("unable to open Playwright browser session: %w", err)
	}

	deadline := time.Now().Add(playwrightLoginTimeout)
	for {
		me, sessionCookie, err := tryImportPlaywrightState(workspaceDir, server, expectedLogin, statePath)
		if err == nil {
			return me, sessionCookie, nil
		}

		if time.Now().After(deadline) {
			return nil, "", fmt.Errorf("playwright-assisted SSO timed out waiting for a valid Jira session: %w", err)
		}

		time.Sleep(playwrightPollInterval)
	}

}

func tryImportPlaywrightState(workspaceDir, server, expectedLogin, statePath string) (*jira.Me, string, error) {

	if _, err := runPlaywrightCLIInDir(workspaceDir, "-s="+playwrightSessionName, "state-save", statePath); err != nil {
		return nil, "", fmt.Errorf("unable to save Playwright browser state: %w", err)
	}

	sessionCookie, err := jira.ExtractCookieTokenFromStorageStateFile(statePath, server)
	if err != nil {
		return nil, "", fmt.Errorf("unable to extract Jira cookies from Playwright state: %w", err)
	}

	client := jira.NewClient(jira.Config{
		Server:   server,
		APIToken: sessionCookie,
		AuthType: &[]jira.AuthType{jira.AuthTypeCookie}[0],
	})
	_, _ = client.WarmupSession()
	me, err := client.Me()
	if err != nil {
		return nil, "", fmt.Errorf("playwright-imported browser session is not valid: %w", err)
	}
	if expectedLogin != "" && me.Login != expectedLogin {
		return nil, "", fmt.Errorf("playwright-imported browser session belongs to '%s' but config expects '%s'", me.Login, expectedLogin)
	}

	return me, sessionCookie, nil
}

func playwrightWorkspaceDir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("unable to determine user cache dir for Playwright session: %w", err)
	}
	path := filepath.Join(cacheDir, "jira-cli", "playwright", "workspace")
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", fmt.Errorf("unable to create Playwright workspace dir: %w", err)
	}
	return path, nil
}

func playwrightProfileDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("unable to determine user config dir for Playwright profile: %w", err)
	}
	path := filepath.Join(configDir, "jira-cli", "playwright", "profiles", "jira")
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", fmt.Errorf("unable to create Playwright profile dir: %w", err)
	}
	return path, nil
}

func playwrightStatePath(workspaceDir string) (string, error) {
	path := filepath.Join(workspaceDir, ".playwright-cli", "jira-auth-state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("unable to create Playwright state dir: %w", err)
	}
	return path, nil
}

func closePlaywrightSession(workspaceDir string) error {
	_, err := runPlaywrightCLIInDir(workspaceDir, "-s="+playwrightSessionName, "close")
	return err
}

func playwrightCLICommand() ([]string, error) {
	if _, err := exec.LookPath("playwright-cli"); err == nil {
		return []string{"playwright-cli"}, nil
	}
	if _, err := exec.LookPath("npx"); err == nil {
		return []string{"npx", "--no-install", "playwright-cli"}, nil
	}
	return nil, fmt.Errorf("playwright-cli is not installed")
}

func runPlaywrightCLI(args ...string) (string, error) {
	return runPlaywrightCLIInDir("", args...)
}

func runPlaywrightCLIInDir(dir string, args ...string) (string, error) {
	base, err := playwrightCLICommand()
	if err != nil {
		return "", err
	}
	cmdArgs := append(base[1:], args...)
	cmd := exec.Command(base[0], cmdArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	output := strings.TrimSpace(stdout.String())
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return output, fmt.Errorf("%s", msg)
	}

	return output, nil
}