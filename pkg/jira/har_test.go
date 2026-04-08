package jira

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractCookieTokenFromHARPrefersAuthenticatedEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.har")

	err := os.WriteFile(path, []byte(`{
		"log": {
			"entries": [
				{
					"request": {
						"method": "GET",
						"url": "https://jira.example.com/rest/api/2/myself",
						"headers": [{"name": "Cookie", "value": "JSESSIONID=bad; AWSALB=old"}]
					},
					"response": {
						"status": 401,
						"headers": [{"name": "X-AUSERNAME", "value": "anonymous"}]
					}
				},
				{
					"request": {
						"method": "GET",
						"url": "https://jira.example.com/browse/TEST-1",
						"headers": [{"name": "Cookie", "value": "Cookie: JSESSIONID=good; AWSALB=fresh"}]
					},
					"response": {
						"status": 200,
						"headers": [
							{"name": "X-Seraph-LoginReason", "value": "OK"},
							{"name": "X-AUSERNAME", "value": "fer"}
						]
					}
				}
			]
		}
	}`), 0o600)
	require.NoError(t, err)

	cookie, err := ExtractCookieTokenFromHAR(path, "https://jira.example.com")
	require.NoError(t, err)
	assert.Equal(t, "JSESSIONID=good; AWSALB=fresh", cookie)
}

func TestExtractCookieTokenFromHARFallsBackToLastSuccessfulCookie(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fallback.har")

	err := os.WriteFile(path, []byte(`{
		"log": {
			"entries": [
				{
					"request": {
						"method": "GET",
						"url": "https://jira.example.com/secure/Dashboard.jspa",
						"headers": [{"name": "Cookie", "value": "JSESSIONID=maybe; AWSALB=maybe"}]
					},
					"response": {
						"status": 200,
						"headers": []
					}
				}
			]
		}
	}`), 0o600)
	require.NoError(t, err)

	cookie, err := ExtractCookieTokenFromHAR(path, "https://jira.example.com")
	require.NoError(t, err)
	assert.Equal(t, "JSESSIONID=maybe; AWSALB=maybe", cookie)
}

func TestExtractCookieTokenFromCurl(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "request.curl")

	err := os.WriteFile(path, []byte("curl 'https://jira.example.com/rest/api/2/myself' -H 'Accept: */*' -H 'Cookie: JSESSIONID=good; AWSALB=fresh; AWSALBCORS=fresh2'"), 0o600)
	require.NoError(t, err)

	cookie, err := ExtractCookieTokenFromCurl(path)
	require.NoError(t, err)
	assert.Equal(t, "JSESSIONID=good; AWSALB=fresh; AWSALBCORS=fresh2", cookie)
}

func TestExtractCookieTokenFromCurlText(t *testing.T) {
	cookie, err := ExtractCookieTokenFromCurlText("curl 'https://jira.example.com' \\\n -H 'Cookie: JSESSIONID=good; AWSALB=fresh'")
	require.NoError(t, err)
	assert.Equal(t, "JSESSIONID=good; AWSALB=fresh", cookie)
}

func TestExtractCookieTokenFromCurlTextCookieFlag(t *testing.T) {
	cookie, err := ExtractCookieTokenFromCurlText("curl 'https://jira.example.com/rest/wrm/2.0/resources' \\\n -H 'accept: */*' \\\n -b 'JSESSIONID=good; AWSALB=fresh; AWSALBCORS=fresh2'")
	require.NoError(t, err)
	assert.Equal(t, "JSESSIONID=good; AWSALB=fresh; AWSALBCORS=fresh2", cookie)
}
