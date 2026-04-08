package jira

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	defaultSSODestinationPath = "/secure/Dashboard.jspa"
	ssoApprovalAttempts       = 20
	ssoApprovalInterval       = 3 * time.Second
	htmlAcceptHeader          = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"
	browserAcceptLanguage     = "es-ES,es;q=0.9"
)

var ssoRedirectPattern = regexp.MustCompile(`window\.location\.assign\("([^"]+)"\)`)

// AuthenticateSSO performs a browser-like SSO login flow and returns the resulting Jira cookie token.
func (c *Client) AuthenticateSSO(username, password string) (*Me, string, error) {
	if c.authType == nil || c.authType.String() != string(AuthTypeCookie) {
		return nil, "", fmt.Errorf("jira: SSO login requires cookie auth type")
	}
	if c.jar == nil {
		c.jar = newCookieJar(c.server, "")
	}

	browser := &http.Client{Transport: c.transport, Jar: c.jar, Timeout: 60 * time.Second}

	loginURL := fmt.Sprintf("%s/login.jsp?permissionViolation=true&os_destination=%s&page_caps=&user_role=USER", c.server, url.QueryEscape(defaultSSODestinationPath))
	_, loginHTML, err := c.browserGet(browser, loginURL, "")
	if err != nil {
		return nil, "", err
	}

	ssoURL, err := extractSSORedirectURL(loginHTML)
	if err != nil {
		return nil, "", err
	}

	currentURL, idpLoginHTML, err := c.browserGet(browser, ssoURL, c.server+"/")
	if err != nil {
		return nil, "", err
	}

	loginAction, loginForm, err := parseHTMLForm(idpLoginHTML, []string{"j_username", "j_password"})
	if err != nil {
		return nil, "", fmt.Errorf("jira: unable to parse SSO credential form: %w", err)
	}

	loginForm.Set("j_username", username)
	loginForm.Set("j_password", password)
	if _, ok := loginForm["_eventId_proceed"]; !ok {
		loginForm.Set("_eventId_proceed", "")
	}

	currentURL, nextHTML, err := c.browserPostForm(browser, resolveFormAction(currentURL, loginAction), loginForm, currentURL.String())
	if err != nil {
		return nil, "", err
	}

	if strings.Contains(nextHTML, "j_username") && strings.Contains(nextHTML, "j_password") {
		return nil, "", fmt.Errorf("jira: SSO credentials were rejected or login form was shown again")
	}

	for attempt := range ssoApprovalAttempts {
		if samlAction, samlForm, ok := parseSAMLConsumerForm(nextHTML); ok {
			_, _, err = c.browserPostForm(browser, resolveFormAction(currentURL, samlAction), samlForm, currentURL.String())
			if err != nil {
				return nil, "", err
			}

			_, _ = c.WarmupSession()
			me, err := c.Me()
			if err != nil {
				return nil, "", err
			}

			return me, c.cookieToken(), nil
		}

		approvalAction, approvalForm, ok := parseApprovalForm(nextHTML)
		if !ok {
			return nil, "", fmt.Errorf("jira: unable to parse SSO approval form after credential step")
		}

		if attempt > 0 {
			time.Sleep(ssoApprovalInterval)
		}

		currentURL, nextHTML, err = c.browserPostForm(browser, resolveFormAction(currentURL, approvalAction), approvalForm, currentURL.String())
		if err != nil {
			return nil, "", err
		}
	}

	return nil, "", fmt.Errorf("jira: SSO approval did not complete in time")
}

func (c *Client) browserGet(client *http.Client, target, referer string) (*url.URL, string, error) {
	req, err := c.buildRequest(http.MethodGet, target, nil, Header{
		"Accept":                 htmlAcceptHeader,
		"Accept-Language":        browserAcceptLanguage,
		"Sec-Fetch-Dest":         "document",
		"Sec-Fetch-Mode":         "navigate",
		"Sec-Fetch-Site":         secFetchSiteValue(referer, target),
		"Upgrade-Insecure-Requests": "1",
	})
	if err != nil {
		return nil, "", err
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", err
	}
	if res.StatusCode >= http.StatusBadRequest {
		return nil, "", fmt.Errorf("jira: SSO bootstrap request failed with %s at %s%s", res.Status, target, summarizeHTMLTitle(body))
	}

	return res.Request.URL, string(body), nil
}

func (c *Client) browserPostForm(client *http.Client, target string, values url.Values, referer string) (*url.URL, string, error) {
	req, err := c.buildRequest(http.MethodPost, target, []byte(values.Encode()), Header{
		"Accept":                  htmlAcceptHeader,
		"Accept-Language":         browserAcceptLanguage,
		"Content-Type":            "application/x-www-form-urlencoded",
		"Origin":                  originOf(target),
		"Sec-Fetch-Dest":          "document",
		"Sec-Fetch-Mode":          "navigate",
		"Sec-Fetch-Site":          secFetchSiteValue(referer, target),
		"Upgrade-Insecure-Requests": "1",
	})
	if err != nil {
		return nil, "", err
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", err
	}
	if res.StatusCode >= http.StatusBadRequest {
		return nil, "", fmt.Errorf("jira: SSO form POST failed with %s at %s%s", res.Status, target, summarizeHTMLTitle(body))
	}

	return res.Request.URL, string(body), nil
}

func extractSSORedirectURL(loginHTML string) (string, error) {
	matches := ssoRedirectPattern.FindStringSubmatch(loginHTML)
	if len(matches) < 2 {
		return "", fmt.Errorf("jira: unable to locate SSO redirect URL in login page")
	}

	return matches[1], nil
}

func parseApprovalForm(doc string) (string, url.Values, bool) {
	action, values, err := parseHTMLForm(doc, []string{"originUserId", "statusHandle"})
	if err != nil {
		return "", nil, false
	}
	if _, ok := values["_eventId_proceed"]; !ok {
		values.Set("_eventId_proceed", "")
	}
	return action, values, true
}

func parseSAMLConsumerForm(doc string) (string, url.Values, bool) {
	action, values, err := parseHTMLForm(doc, []string{"SAMLResponse", "RelayState"})
	if err != nil {
		return "", nil, false
	}
	return action, values, true
}

func parseHTMLForm(doc string, requiredFields []string) (string, url.Values, error) {
	node, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		return "", nil, err
	}

	var walk func(*html.Node) (string, url.Values, bool)
	walk = func(n *html.Node) (string, url.Values, bool) {
		if n.Type == html.ElementNode && n.Data == "form" {
			action := attrValue(n, "action")
			values := url.Values{}
			collectFormInputs(n, values)

			ok := true
			for _, field := range requiredFields {
				if _, exists := values[field]; !exists {
					ok = false
					break
				}
			}
			if ok {
				return action, values, true
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if action, values, ok := walk(c); ok {
				return action, values, true
			}
		}

		return "", nil, false
	}

	action, values, ok := walk(node)
	if !ok {
		return "", nil, fmt.Errorf("required form fields not found: %s", strings.Join(requiredFields, ", "))
	}

	return action, values, nil
}

func collectFormInputs(n *html.Node, values url.Values) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "input":
			name := attrValue(n, "name")
			if name != "" {
				values.Set(name, attrValue(n, "value"))
			}
		case "button":
			name := attrValue(n, "name")
			if name != "" {
				values.Set(name, attrValue(n, "value"))
			}
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectFormInputs(c, values)
	}
}

func attrValue(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func resolveFormAction(base *url.URL, action string) string {
	if action == "" {
		return base.String()
	}
	ref, err := url.Parse(action)
	if err != nil {
		return action
	}
	return base.ResolveReference(ref).String()
}

func originOf(target string) string {
	u, err := url.Parse(target)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func secFetchSiteValue(referer, target string) string {
	if referer == "" {
		return "none"
	}
	ref, err := url.Parse(referer)
	if err != nil {
		return "same-origin"
	}
	tgt, err := url.Parse(target)
	if err != nil {
		return "same-origin"
	}
	if strings.EqualFold(ref.Host, tgt.Host) {
		return "same-origin"
	}
	return "same-site"
}

func (c *Client) cookieToken() string {
	if c.jar == nil {
		return ""
	}
	u, err := url.Parse(c.server)
	if err != nil {
		return ""
	}
	cookies := c.jar.Cookies(u)
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name == "" || cookie.Value == "" {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

func summarizeHTMLTitle(body []byte) string {
	text := string(body)
	if matches := regexp.MustCompile(`(?is)<title>(.*?)</title>`).FindStringSubmatch(text); len(matches) > 1 {
		title := strings.TrimSpace(html.UnescapeString(matches[1]))
		if title != "" {
			return fmt.Sprintf(" | page=%q", title)
		}
	}

	if idx := strings.Index(strings.ToLower(text), "transaction id:"); idx != -1 {
		snippet := text[idx:]
		snippet = strings.ReplaceAll(snippet, "\n", " ")
		snippet = strings.ReplaceAll(snippet, "\r", " ")
		snippet = strings.Join(strings.Fields(snippet), " ")
		if len(snippet) > 120 {
			snippet = snippet[:120]
		}
		return fmt.Sprintf(" | %s", snippet)
	}

	return ""
}