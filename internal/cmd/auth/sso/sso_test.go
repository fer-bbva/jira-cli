package sso

import "testing"

func TestNormalizeConfigPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "keeps yml", in: "/tmp/jira.yml", want: "/tmp/jira.yml"},
		{name: "keeps yaml", in: "/tmp/jira.yaml", want: "/tmp/jira.yaml"},
		{name: "adds extension", in: "/tmp/jira", want: "/tmp/jira.yml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeConfigPath(tt.in); got != tt.want {
				t.Fatalf("normalizeConfigPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestInferInstallationFromServer(t *testing.T) {
	tests := []struct {
		name   string
		server string
		want   string
	}{
		{name: "cloud", server: "https://example.atlassian.net", want: "Cloud"},
		{name: "local", server: "https://jira.globaldevtools.bbva.com", want: "Local"},
		{name: "invalid defaults local", server: "not-a-url", want: "Local"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inferInstallationFromServer(tt.server); got != tt.want {
				t.Fatalf("inferInstallationFromServer(%q) = %q, want %q", tt.server, got, tt.want)
			}
		})
	}
}
