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
	playwrightPollInterval  = 1 * time.Second
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

	if err := waitForPlaywrightAuthenticatedSession(workspaceDir, server); err != nil {
		return nil, "", err
	}

	me, sessionCookie, err := tryImportPlaywrightState(workspaceDir, server, expectedLogin, statePath)
	if err != nil {
		return nil, "", err
	}

	return me, sessionCookie, nil
}

func waitForPlaywrightAuthenticatedSession(workspaceDir, server string) error {
	output, err := runPlaywrightCLIInDir(
		workspaceDir,
		"-s="+playwrightSessionName,
		"--raw",
		"run-code",
		playwrightLoginDetectorScript(server, playwrightLoginTimeout),
	)
	if err != nil {
		if strings.TrimSpace(output) != "" {
			return fmt.Errorf("playwright-assisted SSO timed out waiting for a valid Jira session: %s", strings.TrimSpace(output))
		}
		return fmt.Errorf("playwright-assisted SSO timed out waiting for a valid Jira session: %w", err)
	}

	return nil
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

func playwrightLoginDetectorScript(server string, timeout time.Duration) string {
	return fmt.Sprintf(`async page => {
	const serverOrigin = new URL(%q).origin;
	const context = page.context();
	const timeoutMs = %d;
	const attachedPages = new Set();
	const cleanup = [];
	let settled = false;
	let validating = false;

	const isServerURL = value => {
		try {
			return new URL(value).origin === serverOrigin;
		} catch {
			return false;
		}
	};

	const isInterestingResponse = response => {
		if (!isServerURL(response.url())) {
			return false;
		}

		if (response.status() >= 400) {
			return false;
		}

		const resourceType = response.request().resourceType();
		if (['image', 'font', 'media'].includes(resourceType)) {
			return false;
		}

		const pathname = new URL(response.url()).pathname;
		return pathname === '/' || pathname.startsWith('/browse/') || pathname.startsWith('/secure/') || pathname.startsWith('/rest/');
	};

	const done = await new Promise((resolve, reject) => {
		const finish = (fn, value) => {
			if (settled) {
				return;
			}
			settled = true;
			for (const dispose of cleanup) {
				try {
					dispose();
				} catch {
					// Ignore cleanup failures.
				}
			}
			fn(value);
		};

		const validate = async reason => {
			if (settled || validating) {
				return;
			}
			validating = true;
			try {
				const response = await context.request.get(serverOrigin + '/rest/api/2/myself', { timeout: 5000 });
				if (response.status() < 400) {
					finish(resolve, reason);
				}
			} catch {
				// Session is not ready yet.
			} finally {
				validating = false;
			}
		};

		const attachPage = currentPage => {
			if (attachedPages.has(currentPage)) {
				return;
			}
			attachedPages.add(currentPage);

			const onFrameNavigated = frame => {
				if (frame === currentPage.mainFrame() && isServerURL(frame.url())) {
					void validate('framenavigated:' + frame.url());
				}
			};
			const onLoad = () => {
				if (isServerURL(currentPage.url())) {
					void validate('load:' + currentPage.url());
				}
			};
			const onDOMContentLoaded = () => {
				if (isServerURL(currentPage.url())) {
					void validate('domcontentloaded:' + currentPage.url());
				}
			};

			currentPage.on('framenavigated', onFrameNavigated);
			currentPage.on('load', onLoad);
			currentPage.on('domcontentloaded', onDOMContentLoaded);
			cleanup.push(() => currentPage.removeListener('framenavigated', onFrameNavigated));
			cleanup.push(() => currentPage.removeListener('load', onLoad));
			cleanup.push(() => currentPage.removeListener('domcontentloaded', onDOMContentLoaded));
		};

		const onPage = currentPage => {
			attachPage(currentPage);
			void validate('page:' + currentPage.url());
		};
		const onResponse = response => {
			if (isInterestingResponse(response)) {
				void validate('response:' + response.url());
			}
		};
		const onClose = () => finish(reject, new Error('playwright browser session closed before Jira authentication completed'));

		for (const currentPage of context.pages()) {
			attachPage(currentPage);
		}

		context.on('page', onPage);
		context.on('response', onResponse);
		page.on('close', onClose);
		cleanup.push(() => context.removeListener('page', onPage));
		cleanup.push(() => context.removeListener('response', onResponse));
		cleanup.push(() => page.removeListener('close', onClose));

		const timer = setTimeout(() => finish(reject, new Error('timeout waiting for Jira authentication to complete')), timeoutMs);
		cleanup.push(() => clearTimeout(timer));

		void validate('initial');
	});

	return done;
}`,
		server,
		timeout.Milliseconds(),
	)
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