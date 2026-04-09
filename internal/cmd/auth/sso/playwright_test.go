package sso

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePlaywrightAuthResult(t *testing.T) {
	result, err := parsePlaywrightAuthResult(`{
		"cookie": "AWSALB=bal; JSESSIONID=abc",
		"reason": "response:https://jira.example.com/rest/api/2/myself",
		"pageUrl": "https://jira.example.com/secure/RapidBoard.jspa",
		"openPages": ["https://jira.example.com/secure/RapidBoard.jspa"],
		"me": {
			"name": "user.test",
			"displayName": "User Test",
			"emailAddress": "user@test.com",
			"timeZone": "Europe/Madrid"
		}
	}`)
	require.NoError(t, err)
	assert.Equal(t, "AWSALB=bal; JSESSIONID=abc", result.Cookie)
	assert.Equal(t, "user.test", result.Me.Login)
	assert.Equal(t, "User Test", result.Me.Name)
	assert.Equal(t, "response:https://jira.example.com/rest/api/2/myself", result.Reason)
}

func TestParsePlaywrightAuthResultRequiresCookieAndUser(t *testing.T) {
	_, err := parsePlaywrightAuthResult(`{"cookie":"","me":{"name":""}}`)
	require.Error(t, err)
}

func TestParsePlaywrightAuthResultWithWrappedOutput(t *testing.T) {
	result, err := parsePlaywrightAuthResult(`### Result
# extra cli noise
{"cookie":"JSESSIONID=abc","reason":"initial","pageUrl":"https://jira.example.com/","me":{"name":"user.test","displayName":"User Test"}}`)
	require.NoError(t, err)
	assert.Equal(t, "JSESSIONID=abc", result.Cookie)
	assert.Equal(t, "user.test", result.Me.Login)
}
