package jira

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
)

type harFile struct {
	Log struct {
		Entries []harEntry `json:"entries"`
	} `json:"log"`
}

type harEntry struct {
	Request struct {
		Method  string      `json:"method"`
		URL     string      `json:"url"`
		Headers []harHeader `json:"headers"`
	} `json:"request"`
	Response struct {
		Status  int         `json:"status"`
		Headers []harHeader `json:"headers"`
	} `json:"response"`
}

type harHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ExtractCookieTokenFromHAR extracts the most promising authenticated Jira Cookie header from a HAR file.
func ExtractCookieTokenFromHAR(harPath, server string) (string, error) {
	data, err := os.ReadFile(harPath)
	if err != nil {
		return "", err
	}

	var har harFile
	if err := json.Unmarshal(data, &har); err != nil {
		return "", err
	}

	serverURL, err := url.Parse(server)
	if err != nil {
		return "", err
	}

	var fallback string
	for i := len(har.Log.Entries) - 1; i >= 0; i-- {
		entry := har.Log.Entries[i]
		cookie, ok := headerValue(entry.Request.Headers, "cookie")
		if !ok || cookie == "" {
			continue
		}

		requestURL, err := url.Parse(entry.Request.URL)
		if err != nil || !sameHARHost(serverURL, requestURL) {
			continue
		}

		if entry.Response.Status >= 400 {
			continue
		}

		xUser, _ := headerValue(entry.Response.Headers, "x-ausername")
		loginReason, _ := headerValue(entry.Response.Headers, "x-seraph-loginreason")
		if strings.EqualFold(loginReason, "OK") && xUser != "" && !strings.EqualFold(xUser, "anonymous") {
			return NormalizeCookieToken(cookie), nil
		}

		if fallback == "" {
			fallback = NormalizeCookieToken(cookie)
		}
	}

	if fallback != "" {
		return fallback, nil
	}

	return "", fmt.Errorf("no authenticated Jira Cookie header found in HAR")
}

func headerValue(headers []harHeader, name string) (string, bool) {
	for _, h := range headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value, true
		}
	}

	return "", false
}

func sameHARHost(serverURL, requestURL *url.URL) bool {
	return strings.EqualFold(serverURL.Scheme, requestURL.Scheme) && strings.EqualFold(serverURL.Host, requestURL.Host)
}

// ExtractCookieTokenFromCurl extracts Cookie header from a copied cURL command.
func ExtractCookieTokenFromCurl(curlPath string) (string, error) {
	data, err := os.ReadFile(curlPath)
	if err != nil {
		return "", err
	}

	text := strings.ReplaceAll(string(data), "\\\n", " ")
	text = strings.ReplaceAll(text, "\\\r\n", " ")

	markers := []string{"Cookie: ", "cookie: "}
	for _, marker := range markers {
		idx := strings.Index(text, marker)
		if idx == -1 {
			continue
		}
		start := idx + len(marker)
		quote := byte(0)
		for i := idx - 1; i >= 0; i-- {
			if text[i] == '\'' || text[i] == '"' {
				quote = text[i]
				break
			}
			if text[i] == ' ' {
				break
			}
		}
		end := len(text)
		if quote != 0 {
			if j := strings.IndexByte(text[start:], quote); j != -1 {
				end = start + j
			}
		} else if j := strings.IndexAny(text[start:], "\n\r"); j != -1 {
			end = start + j
		}

		return NormalizeCookieToken(text[start:end]), nil
	}

	return "", fmt.Errorf("no Cookie header found in cURL command")
}