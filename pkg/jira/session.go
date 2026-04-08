package jira

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const cookieWarmupAttempts = 5

// WarmupSession refreshes cookie-based session state by simulating a small same-site browser flow
// before retrying a low-cost REST endpoint.
func (c *Client) WarmupSession() (*ServerInfo, error) {
	httpClient := &http.Client{Transport: c.transport, Jar: c.jar}
	ctx := context.Background()

	return c.warmupServerSession(ctx, httpClient, c.server+baseURLv2+"/serverInfo")
}

// WarmupAgileSession refreshes agile session state for the configured board.
func (c *Client) WarmupAgileSession(boardID int) error {
	if boardID <= 0 {
		return nil
	}

	httpClient := &http.Client{Transport: c.transport, Jar: c.jar}
	ctx := context.Background()
	boardURL := fmt.Sprintf("%s%s/board/%d/sprint?state=active&startAt=0&maxResults=1", c.server, baseURLv1, boardID)

	if err := c.warmupCookieSession(ctx, httpClient, boardURL); err != nil {
		return err
	}

	_, err := c.Sprints(boardID, "state=active", 0, 1)

	return err
}

// WarmupIssueSession refreshes browser-backed session state for an issue-centric flow.
func (c *Client) WarmupIssueSession(key string) error {
	if key == "" {
		return nil
	}

	httpClient := &http.Client{Transport: c.transport, Jar: c.jar}
	ctx := context.Background()

	return c.warmupCookieSession(ctx, httpClient, c.server+"/browse/"+key)
}

func (c *Client) warmupServerSession(ctx context.Context, httpClient *http.Client, target string) (*ServerInfo, error) {
	var lastErr error

	for range cookieWarmupAttempts {
		_ = c.warmupCookieSession(ctx, httpClient, target)

		info, err := c.ServerInfo()
		if err == nil {
			return info, nil
		}
		lastErr = err
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("unable to warm up jira session")
	}

	return nil, lastErr
}

func (c *Client) warmupCookieSession(ctx context.Context, httpClient *http.Client, target string) error {
	var lastErr error
	success := false

	for _, step := range c.cookieWarmupTargets(target) {
		for range cookieWarmupAttempts {
			req, err := c.buildRequest(http.MethodGet, step.URL, nil, step.Headers)
			if err != nil {
				lastErr = err
				continue
			}

			res, err := httpClient.Do(req.WithContext(ctx))
			if res != nil {
				_ = res.Body.Close()
			}
			if err == nil && res != nil && res.StatusCode < http.StatusBadRequest {
				success = true
				break
			}
			if err != nil {
				lastErr = err
				continue
			}
			lastErr = fmt.Errorf("warmup target %s returned %s", step.URL, res.Status)
		}
	}

	if success {
		return nil
	}

	return lastErr
}

type warmupTarget struct {
	URL     string
	Headers Header
}

func (c *Client) cookieWarmupTargets(target string) []warmupTarget {
	targets := []warmupTarget{{URL: c.server + "/"}}

	if key := issueKeyFromTarget(target); key != "" {
		browseURL := c.server + "/browse/" + key
		targets = append(targets,
			warmupTarget{URL: browseURL},
			warmupTarget{
				URL: browseURL,
				Headers: Header{
					"Referer":          browseURL,
					"X-Requested-With": "XMLHttpRequest",
					"Accept":           "*/*",
				},
			},
		)
	}

	if boardID := boardIDFromTarget(target); boardID != "" {
		boardPage := c.server + "/secure/RapidBoard.jspa?rapidView=" + boardID + "&view=planning&issueLimit=100"
		targets = append(targets,
			warmupTarget{URL: boardPage},
			warmupTarget{
				URL: c.server + "/rest/greenhopper/1.0/xboard/plan/backlog/data.json?rapidViewId=" + boardID,
				Headers: Header{
					"Referer": boardPage,
					"Accept":  "*/*",
				},
			},
			warmupTarget{
				URL: c.server + "/rest/greenhopper/1.0/sidebar/globalBoard?rapidViewId=" + boardID,
				Headers: Header{
					"Referer":          boardPage,
					"Accept":           "*/*",
					"X-Requested-With": "XMLHttpRequest",
				},
			},
		)
	}

	targets = append(targets, warmupTarget{URL: c.server + baseURLv2 + "/serverInfo"})

	return dedupeTargets(targets)
}

func issueKeyFromTarget(target string) string {
	const (
		issuePathV2 = "/rest/api/2/issue/"
		issuePathV3 = "/rest/api/3/issue/"
	)

	for _, prefix := range []string{issuePathV2, issuePathV3} {
		if idx := strings.Index(target, prefix); idx != -1 {
			key := target[idx+len(prefix):]
			if cut := strings.Index(key, "/"); cut != -1 {
				key = key[:cut]
			}
			if cut := strings.IndexAny(key, "?"); cut != -1 {
				key = key[:cut]
			}
			return key
		}
	}

	return ""
}

func boardIDFromTarget(target string) string {
	u, err := url.Parse(target)
	if err == nil {
		if rapidView := u.Query().Get("rapidView"); rapidView != "" {
			return rapidView
		}
	}

	const boardPath = "/rest/agile/1.0/board/"
	idx := strings.Index(target, boardPath)
	if idx == -1 {
		return ""
	}

	rest := target[idx+len(boardPath):]
	if cut := strings.IndexAny(rest, "/?"); cut != -1 {
		rest = rest[:cut]
	}

	if _, err := strconv.Atoi(rest); err != nil {
		return ""
	}

	return rest
}

func dedupeTargets(targets []warmupTarget) []warmupTarget {
	seen := make(map[string]struct{}, len(targets))
	out := make([]warmupTarget, 0, len(targets))

	for _, target := range targets {
		if target.URL == "" {
			continue
		}
		key := target.URL + "|" + target.Headers["Referer"] + "|" + target.Headers["X-Requested-With"]
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, target)
	}

	return out
}