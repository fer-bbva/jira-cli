package browser

import (
	"bytes"
	"os"
	"os/exec"
	"runtime"

	"github.com/google/shlex"
	"github.com/pkg/browser"
)

// Browse opens given url in a web browser.
//
// It looks for `JIRA_BROWSER` and `BROWSER` env respectively to decide which
// executable to use. If none of them are set, the default browser is invoked.
func Browse(url string) error {
	opener := getBrowserFromENV()

	if opener == "" {
		// Launch default browser.
		return browser.OpenURL(url)
	}

	args, err := shlex.Split(opener)
	if err != nil {
		return err
	}
	exe, err := exec.LookPath(args[0])
	if err != nil {
		return err
	}

	args = append(args[1:], url)
	cmd := exec.Command(exe, args...)

	// io.Writer to which executed commands write standard output and error.
	// We are not interested in any output from cmd, so let's discard them.
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &bytes.Buffer{}

	return cmd.Run()
}

// BrowseWithDevTools opens a URL in a browser and tries to auto-open devtools when supported.
func BrowseWithDevTools(url string) error {
	if runtime.GOOS == "darwin" {
		cmd := exec.Command("open", "-na", "Google Chrome", "--args", "--new-window", "--auto-open-devtools-for-tabs", url)
		cmd.Stdout = &bytes.Buffer{}
		cmd.Stderr = &bytes.Buffer{}
		if err := cmd.Run(); err == nil {
			return nil
		}
	}

	return Browse(url)
}

func getBrowserFromENV() string {
	br := os.Getenv("JIRA_BROWSER")
	if br == "" {
		br = os.Getenv("BROWSER")
	}
	return br
}
