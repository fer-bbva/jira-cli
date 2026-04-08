package jira

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type playwrightStorageState struct {
	Cookies []playwrightCookie `json:"cookies"`
	Origins []struct {
		Origin       string `json:"origin"`
		LocalStorage []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"localStorage"`
	} `json:"origins"`
}

type playwrightCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	HTTPOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
}

// ExtractCookieTokenFromStorageStateFile extracts a Jira-compatible Cookie header value from a Playwright storageState file.
func ExtractCookieTokenFromStorageStateFile(storageStatePath, server string) (string, error) {
	data, err := os.ReadFile(storageStatePath)
	if err != nil {
		return "", err
	}

	return ExtractCookieTokenFromStorageState(data, server)
}

// ExtractCookieTokenFromStorageState extracts a Jira-compatible Cookie header value from Playwright storageState JSON.
func ExtractCookieTokenFromStorageState(data []byte, server string) (string, error) {
	var state playwrightStorageState
	if err := json.Unmarshal(data, &state); err != nil {
		return "", err
	}

	serverURL, err := url.Parse(server)
	if err != nil {
		return "", err
	}

	now := time.Now().Unix()
	parts := make([]string, 0, len(state.Cookies))
	for _, cookie := range state.Cookies {
		if cookie.Name == "" || cookie.Value == "" {
			continue
		}
		if cookie.Expires > 0 && int64(cookie.Expires) <= now {
			continue
		}
		if !cookieMatchesHost(cookie.Domain, serverURL.Hostname()) {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}

	if len(parts) == 0 {
		return "", fmt.Errorf("no matching cookies found in Playwright storage state for %s", serverURL.Hostname())
	}

	return NormalizeCookieToken(strings.Join(parts, "; ")), nil
}

func cookieMatchesHost(domain, host string) bool {
	domain = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(domain), "."))
	host = strings.TrimSpace(strings.ToLower(host))
	if domain == "" || host == "" {
		return false
	}
	if domain == host {
		return true
	}
	return strings.HasSuffix(host, "."+domain)
}