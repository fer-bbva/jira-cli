package jira

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractCookieTokenFromStorageState(t *testing.T) {
	data := []byte(`{
		"cookies": [
			{"name":"GCP_IAP_UID","value":"123","domain":".bbva.com","path":"/","expires":9999999999},
			{"name":"JSESSIONID","value":"abc","domain":"jira.globaldevtools.bbva.com","path":"/","expires":9999999999},
			{"name":"AWSALB","value":"bal","domain":"jira.globaldevtools.bbva.com","path":"/","expires":9999999999},
			{"name":"OTHER","value":"skip","domain":"example.com","path":"/","expires":9999999999}
		],
		"origins": []
	}`)

	cookie, err := ExtractCookieTokenFromStorageState(data, "https://jira.globaldevtools.bbva.com")
	require.NoError(t, err)
	assert.Equal(t, "GCP_IAP_UID=123; JSESSIONID=abc; AWSALB=bal", cookie)
}

func TestExtractCookieTokenFromStorageStateFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	err := os.WriteFile(path, []byte(`{
		"cookies": [
			{"name":"JSESSIONID","value":"abc","domain":"jira.example.com","path":"/","expires":9999999999}
		],
		"origins": []
	}`), 0o600)
	require.NoError(t, err)

	cookie, err := ExtractCookieTokenFromStorageStateFile(path, "https://jira.example.com")
	require.NoError(t, err)
	assert.Equal(t, "JSESSIONID=abc", cookie)
}

func TestCookieMatchesHost(t *testing.T) {
	assert.True(t, cookieMatchesHost(".bbva.com", "jira.globaldevtools.bbva.com"))
	assert.True(t, cookieMatchesHost("jira.globaldevtools.bbva.com", "jira.globaldevtools.bbva.com"))
	assert.False(t, cookieMatchesHost("example.com", "jira.globaldevtools.bbva.com"))
}