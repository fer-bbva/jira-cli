package jira

import "strings"

// NormalizeCookieToken normalizes a cookie value copied from a browser or request header.
// It accepts either a raw JSESSIONID value or a full Cookie header value.
func NormalizeCookieToken(token string) string {
	token = strings.TrimSpace(token)
	token = strings.ReplaceAll(token, "\r", "")
	token = strings.ReplaceAll(token, "\n", " ")
	if strings.HasPrefix(strings.ToLower(token), "cookie:") {
		token = strings.TrimSpace(token[len("cookie:"):])
	}
	token = strings.Join(strings.Fields(token), " ")
	token = strings.TrimSpace(token)
	token = strings.TrimSuffix(token, ";")

	return token
}
