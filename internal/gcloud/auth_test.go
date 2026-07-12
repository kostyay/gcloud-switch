package gcloud

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialPath(t *testing.T) {
	tests := []struct {
		name    string
		account string
		want    string
	}{
		{
			name:    "email account is literal",
			account: "me@example.com",
			want:    "legacy_credentials/me@example.com/adc.json",
		},
		{
			name:    "workforce principal collapses scheme separator",
			account: "principal://iam.googleapis.com/locations/global/pool/subject/me",
			want:    "legacy_credentials/principal/iam.googleapis.com/locations/global/pool/subject/me/adc.json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := New("/root", "")
			got := c.credentialPath(tc.account)
			if got != filepath.Join("/root", tc.want) {
				t.Fatalf("got %q, want %q", got, filepath.Join("/root", tc.want))
			}
		})
	}
}

func TestCheckAuthNoCredentials(t *testing.T) {
	tests := []struct {
		name    string
		account string
		want    error
	}{
		{name: "empty account", account: "", want: errNoAccount},
		{name: "missing credential file", account: "me@example.com", want: errNoCredentials},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := New(t.TempDir(), "")
			if err := c.checkAuth(t.Context(), tc.account); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLoginRequired(t *testing.T) {
	c := New(t.TempDir(), "")
	configs := []Config{
		{Name: "no-account"},
		{Name: "missing-creds", Account: "me@example.com"},
	}

	got := c.LoginRequired(t.Context(), configs)

	if got[0] {
		t.Fatal("config without account should not be flagged")
	}
	if !got[1] {
		t.Fatal("config with missing credentials should be flagged")
	}
}

func TestMissingLoginConfig(t *testing.T) {
	existing := filepath.Join(t.TempDir(), "login.json")
	if err := os.WriteFile(existing, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "no login config", cfg: Config{}, want: ""},
		{name: "present file", cfg: Config{LoginConfigFile: existing}, want: ""},
		{name: "missing file", cfg: Config{LoginConfigFile: "/no/such/login.json"}, want: "/no/such/login.json"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := &ReloginPlan{Config: tc.cfg}
			if got := plan.MissingLoginConfig(); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoginCommand(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "regular account passes account positionally",
			cfg:  Config{Account: "me@example.com"},
			want: "gcloud auth login me@example.com",
		},
		{
			name: "login-config config uses --login-config, not the principal",
			cfg: Config{
				Account:         "principal://iam.googleapis.com/pool/subject/me",
				LoginConfigFile: "/tmp/login.json",
			},
			want: "gcloud auth login --login-config=/tmp/login.json",
		},
		{
			name: "no account falls back to bare login",
			cfg:  Config{},
			want: "gcloud auth login",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(loginCommand(tc.cfg), " ")
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
