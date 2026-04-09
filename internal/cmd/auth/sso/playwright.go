package sso

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
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
	playwrightLoginTimeout = 5 * time.Minute
)

type playwrightAuthResult struct {
	Cookie  string  `json:"cookie"`
	Reason  string  `json:"reason"`
	PageURL string  `json:"pageUrl"`
	Me      jira.Me `json:"me"`
}

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

	cmdutil.Warn("Playwright CLI detected. Opening an isolated Jira browser session for interactive SSO login.")
	cmdutil.Warn("Complete the BBVA login there, including the mobile second factor. I will detect the Jira session automatically.")

	_ = closePlaywrightSession(workspaceDir)
	defer func() { _ = closePlaywrightSession(workspaceDir) }()

	if _, err := runPlaywrightCLIInDir(workspaceDir, "-s="+playwrightSessionName, "open", server, "--browser=chrome", "--headed", "--profile="+profileDir); err != nil {
		return nil, "", fmt.Errorf("unable to open Playwright browser session: %w", err)
	}

	result, err := waitForPlaywrightAuthenticatedSession(workspaceDir, server)
	if err != nil {
		return nil, "", err
	}

	sessionCookie := jira.NormalizeCookieToken(result.Cookie)
	if sessionCookie == "" {
		return nil, "", fmt.Errorf("playwright-assisted SSO succeeded but did not return Jira cookies")
	}
	if result.Me.Login == "" {
		return nil, "", fmt.Errorf("playwright-assisted SSO succeeded but did not return Jira user info")
	}
	if expectedLogin != "" && result.Me.Login != expectedLogin {
		return nil, "", fmt.Errorf("playwright-assisted browser session belongs to '%s' but config expects '%s'", result.Me.Login, expectedLogin)
	}

	if result.Reason != "" {
		if result.PageURL != "" {
			cmdutil.Success("Playwright detected Jira session via %s (%s)", result.Reason, result.PageURL)
		} else {
			cmdutil.Success("Playwright detected Jira session via %s", result.Reason)
		}
	}

	me := result.Me
	return &me, sessionCookie, nil
}

func waitForPlaywrightAuthenticatedSession(workspaceDir, server string) (*playwrightAuthResult, error) {
	output, err := runPlaywrightCLIInDir(
		workspaceDir,
		"-s="+playwrightSessionName,
		"--raw",
		"run-code",
		playwrightLoginDetectorScript(server, playwrightLoginTimeout),
	)
	if err != nil {
		if strings.TrimSpace(output) != "" {
			return nil, fmt.Errorf("playwright-assisted SSO failed while waiting for a valid Jira session: %s", strings.TrimSpace(output))
		}
		return nil, fmt.Errorf("playwright-assisted SSO failed while waiting for a valid Jira session: %w", err)
	}

	result, err := parsePlaywrightAuthResult(output)
	if err != nil {
		return nil, fmt.Errorf("unable to parse Playwright-authenticated Jira session: %w | output=%s", err, summarizePlaywrightOutput(output))
	}

	return result, nil
}

func parsePlaywrightAuthResult(output string) (*playwrightAuthResult, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, fmt.Errorf("empty output")
	}

	for _, candidate := range playwrightAuthResultCandidates(trimmed) {
		var result playwrightAuthResult
		if err := json.Unmarshal([]byte(candidate), &result); err != nil {
			continue
		}
		if strings.TrimSpace(result.Cookie) == "" {
			continue
		}
		if strings.TrimSpace(result.Me.Login) == "" {
			continue
		}

		return &result, nil
	}

	return nil, fmt.Errorf("no valid auth payload found in output")
}

func playwrightAuthResultCandidates(output string) []string {
	candidates := []string{output}

	var unquoted string
	if err := json.Unmarshal([]byte(output), &unquoted); err == nil && strings.TrimSpace(unquoted) != "" {
		candidates = append(candidates, strings.TrimSpace(unquoted))
	}

	if start := strings.Index(output, "{"); start != -1 {
		if end := strings.LastIndex(output, "}"); end > start {
			candidates = append(candidates, strings.TrimSpace(output[start:end+1]))
		}
	}

	if len(candidates) == 1 {
		return candidates
	}

	seen := make(map[string]struct{}, len(candidates))
	unique := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		unique = append(unique, candidate)
	}

	return unique
}

func summarizePlaywrightOutput(output string) string {
	trimmed := strings.TrimSpace(output)
	trimmed = strings.ReplaceAll(trimmed, "\n", " ")
	trimmed = strings.ReplaceAll(trimmed, "\r", " ")
	trimmed = strings.Join(strings.Fields(trimmed), " ")
	if trimmed == "" {
		return `""`
	}
	if len(trimmed) > 240 {
		trimmed = trimmed[:240] + "..."
	}
	return fmt.Sprintf("%q", trimmed)
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

func closePlaywrightSession(workspaceDir string) error {
	_, err := runPlaywrightCLIInDir(workspaceDir, "-s="+playwrightSessionName, "close")
	return err
}

func playwrightLoginDetectorScript(server string, timeout time.Duration) string {
	serverOrigin := strings.TrimRight(server, "/")
	if parsedServer, err := url.Parse(server); err == nil && parsedServer.Scheme != "" && parsedServer.Host != "" {
		serverOrigin = parsedServer.Scheme + "://" + parsedServer.Host
	}

	return fmt.Sprintf(`async page => {
	const serverOrigin = %q;
	const serverURL = %q;
	const context = page.context();
	const timeoutMs = %d;
	const attachedPages = new Set();
	const cleanup = [];
	let settled = false;
	let validating = false;

	const isServerURL = value => {
		return typeof value === 'string' && value.startsWith(serverOrigin);
	};

	const pathOf = value => {
		if (!isServerURL(value)) {
			return '';
		}
		const rest = value.slice(serverOrigin.length);
		return rest === '' ? '/' : rest;
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

		const pathname = pathOf(response.url());
		return pathname === '/' || pathname.startsWith('/browse/') || pathname.startsWith('/secure/') || pathname.startsWith('/rest/');
	};

	const donePromise = new Promise((resolve, reject) => {
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
					const me = await response.json();
					const cookies = await context.cookies([serverURL]);
					const cookie = cookies
						.filter(current => current.name && current.value)
						.sort((left, right) => left.name.localeCompare(right.name))
						.map(current => current.name + '=' + current.value)
						.join('; ');
					if (!cookie || !me || !me.name) {
						return;
					}

					finish(resolve, {
						cookie,
						reason,
						pageUrl: page.url(),
						me,
					});
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

		void validate('initial');
	});

	const done = await Promise.race([
		donePromise,
		page.waitForTimeout(timeoutMs).then(() => {
			throw new Error('timeout waiting for Jira authentication to complete');
		}),
	]);

	return done;
}`,
		serverOrigin,
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
