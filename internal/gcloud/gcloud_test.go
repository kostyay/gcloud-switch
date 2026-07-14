package gcloud

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestClient returns a Client over a fresh gcloud root and state file.
func newTestClient(t *testing.T) *Client {
	t.Helper()
	return New(t.TempDir(), filepath.Join(t.TempDir(), "previous"),
		filepath.Join(t.TempDir(), "gke_gcloud_auth_plugin_cache"))
}

func seedConfig(t *testing.T, c *Client, name, account, project, region string) {
	t.Helper()
	dir := c.configurationsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir configurations: %v", err)
	}
	body := "[core]\naccount = " + account + "\nproject = " + project +
		"\n[compute]\nregion = " + region + "\n"
	if err := os.WriteFile(c.configPath(name), []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func setActive(t *testing.T, c *Client, name string) {
	t.Helper()
	if err := os.WriteFile(c.activeConfigPath(), []byte(name), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestList(t *testing.T) {
	tests := []struct {
		name    string
		seed    func(t *testing.T, c *Client)
		want    []string
		wantErr error
	}{
		{
			name:    "root missing",
			seed:    func(t *testing.T, c *Client) { os.RemoveAll(c.root) },
			wantErr: ErrNotConfigured,
		},
		{
			name:    "no configurations",
			seed:    func(t *testing.T, c *Client) {},
			wantErr: ErrNoConfigurations,
		},
		{
			name: "sorted names",
			seed: func(t *testing.T, c *Client) {
				seedConfig(t, c, "prod", "a@x.com", "p-prod", "us-central1")
				seedConfig(t, c, "dev", "a@x.com", "p-dev", "us-east1")
			},
			want: []string{"dev", "prod"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t)
			tc.seed(t, c)

			got, err := c.List()
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			var names []string
			for _, cfg := range got {
				names = append(names, cfg.Name)
			}
			if len(names) != len(tc.want) {
				t.Fatalf("names = %v, want %v", names, tc.want)
			}
			for i := range names {
				if names[i] != tc.want[i] {
					t.Fatalf("names = %v, want %v", names, tc.want)
				}
			}
		})
	}
}

func TestListParsesFields(t *testing.T) {
	c := newTestClient(t)
	seedConfig(t, c, "prod", "me@x.com", "my-proj", "us-central1")

	got, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	cfg := got[0]
	if cfg.Account != "me@x.com" || cfg.Project != "my-proj" || cfg.Region != "us-central1" {
		t.Fatalf("parsed = %+v", cfg)
	}
}

func TestParseConfigLoginConfigFile(t *testing.T) {
	c := newTestClient(t)
	if err := os.MkdirAll(c.configurationsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[core]\naccount = p@x.com\n[auth]\nlogin_config_file = /tmp/login.json\n"
	if err := os.WriteFile(c.configPath("staging"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := c.parseConfig("staging")
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.Account != "p@x.com" || cfg.LoginConfigFile != "/tmp/login.json" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestCurrent(t *testing.T) {
	tests := []struct {
		name   string
		active string // empty means no active_config file
		want   string
	}{
		{name: "missing pointer defaults", active: "", want: defaultConfigName},
		{name: "reads active", active: "prod\n", want: "prod"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t)
			if tc.active != "" {
				setActive(t, c, tc.active)
			}
			got, err := c.Current()
			if err != nil {
				t.Fatalf("Current: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSwitch(t *testing.T) {
	c := newTestClient(t)
	seedConfig(t, c, "dev", "a@x.com", "p-dev", "us-east1")
	seedConfig(t, c, "prod", "a@x.com", "p-prod", "us-central1")
	setActive(t, c, "dev")

	if err := c.Switch("prod"); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if cur, _ := c.Current(); cur != "prod" {
		t.Fatalf("current = %q, want prod", cur)
	}
	if prev, err := c.Previous(); err != nil || prev != "dev" {
		t.Fatalf("Previous = %q, %v; want dev", prev, err)
	}
}

func TestSwitchClearsKubeAuthCache(t *testing.T) {
	c := newTestClient(t)
	seedConfig(t, c, "dev", "a@x.com", "p-dev", "us-east1")
	seedConfig(t, c, "prod", "a@x.com", "p-prod", "us-central1")
	setActive(t, c, "dev")
	if err := os.WriteFile(c.kubeCachePath, []byte("stale-token"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := c.Switch("prod"); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if _, err := os.Stat(c.kubeCachePath); !os.IsNotExist(err) {
		t.Fatalf("kube auth cache still present: %v", err)
	}
}

func TestSwitchMissingKubeCacheIsOK(t *testing.T) {
	c := newTestClient(t)
	seedConfig(t, c, "dev", "a@x.com", "p-dev", "us-east1")
	seedConfig(t, c, "prod", "a@x.com", "p-prod", "us-central1")
	setActive(t, c, "dev")

	// No cache file exists; Switch must not error.
	if err := c.Switch("prod"); err != nil {
		t.Fatalf("Switch: %v", err)
	}
}

func TestSwitchToActiveIsNoop(t *testing.T) {
	c := newTestClient(t)
	seedConfig(t, c, "prod", "a@x.com", "p-prod", "us-central1")
	setActive(t, c, "prod")

	if err := c.Switch("prod"); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	// previous must not be written when switching to the already-active config.
	if _, err := c.Previous(); !errors.Is(err, ErrNoPrevious) {
		t.Fatalf("Previous err = %v, want ErrNoPrevious", err)
	}
}

func TestSwitchUnknownConfig(t *testing.T) {
	c := newTestClient(t)
	seedConfig(t, c, "prod", "a@x.com", "p-prod", "us-central1")

	if err := c.Switch("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRename(t *testing.T) {
	c := newTestClient(t)
	seedConfig(t, c, "dev", "a@x.com", "p-dev", "us-east1")
	seedConfig(t, c, "prod", "a@x.com", "p-prod", "us-central1")
	setActive(t, c, "dev")
	if err := c.savePrevious("prod"); err != nil {
		t.Fatal(err)
	}

	if err := c.Rename("dev", "staging"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	if _, err := os.Stat(c.configPath("dev")); !os.IsNotExist(err) {
		t.Fatalf("old config still exists: %v", err)
	}
	if _, err := os.Stat(c.configPath("staging")); err != nil {
		t.Fatalf("new config missing: %v", err)
	}
	if cur, _ := c.Current(); cur != "staging" {
		t.Fatalf("active = %q, want staging", cur)
	}

	// Renaming prod updates the recorded previous pointer.
	if err := c.Rename("prod", "production"); err != nil {
		t.Fatalf("Rename prod: %v", err)
	}
	if prev, _ := c.Previous(); prev != "production" {
		t.Fatalf("previous = %q, want production", prev)
	}
}

func TestRenameErrors(t *testing.T) {
	tests := []struct {
		name           string
		oldName, newTo string
		wantErr        error
	}{
		{name: "old missing", oldName: "nope", newTo: "x", wantErr: ErrNotFound},
		{name: "new exists", oldName: "dev", newTo: "prod", wantErr: ErrExists},
		{name: "invalid name", oldName: "dev", newTo: "Bad Name", wantErr: ErrInvalidName},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t)
			seedConfig(t, c, "dev", "a@x.com", "p-dev", "us-east1")
			seedConfig(t, c, "prod", "a@x.com", "p-prod", "us-central1")

			if err := c.Rename(tc.oldName, tc.newTo); !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestSetLoginConfig(t *testing.T) {
	c := newTestClient(t)
	seedConfig(t, c, "trq", "principal://iam/locations/global/workforcePools/p/subject/me", "proj", "us-central1")
	loginFile := filepath.Join(t.TempDir(), "login.json")
	if err := os.WriteFile(loginFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := c.SetLoginConfig("trq", loginFile); err != nil {
		t.Fatalf("SetLoginConfig: %v", err)
	}

	cfg, err := c.parseConfig("trq")
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.LoginConfigFile != loginFile {
		t.Fatalf("LoginConfigFile = %q, want %q", cfg.LoginConfigFile, loginFile)
	}
	// Existing fields must survive the rewrite.
	if cfg.Project != "proj" || cfg.Region != "us-central1" {
		t.Fatalf("clobbered fields: %+v", cfg)
	}
	if cfg.NeedsLoginConfig() {
		t.Fatal("NeedsLoginConfig true after setting login config")
	}
}

func TestSetLoginConfigMissingFile(t *testing.T) {
	c := newTestClient(t)
	seedConfig(t, c, "trq", "a@x.com", "proj", "us-central1")

	err := c.SetLoginConfig("trq", filepath.Join(t.TempDir(), "nope.json"))
	if err == nil {
		t.Fatal("expected error for missing login config file")
	}
}

func TestSetLoginConfigUnknownConfig(t *testing.T) {
	c := newTestClient(t)
	if err := c.SetLoginConfig("nope", "/tmp/x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestClearLoginConfig(t *testing.T) {
	c := newTestClient(t)
	if err := os.MkdirAll(c.configurationsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[core]\naccount = me@x.com\n[auth]\nlogin_config_file = /tmp/login.json\n"
	if err := os.WriteFile(c.configPath("trq"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := c.ClearLoginConfig("trq"); err != nil {
		t.Fatalf("ClearLoginConfig: %v", err)
	}
	cfg, err := c.parseConfig("trq")
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.LoginConfigFile != "" {
		t.Fatalf("LoginConfigFile = %q, want empty", cfg.LoginConfigFile)
	}
	if cfg.Account != "me@x.com" {
		t.Fatalf("account clobbered: %q", cfg.Account)
	}
}

func TestNeedsLoginConfig(t *testing.T) {
	tests := []struct {
		name    string
		account string
		login   string
		want    bool
	}{
		{name: "workforce no login", account: "principal://x/workforcePools/p/subject/me", want: true},
		{name: "workforce with login", account: "principal://x/workforcePools/p/subject/me", login: "/f.json"},
		{name: "regular account", account: "me@x.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Account: tc.account, LoginConfigFile: tc.login}
			if got := cfg.NeedsLoginConfig(); got != tc.want {
				t.Fatalf("NeedsLoginConfig = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLoginConfigWarning(t *testing.T) {
	c := newTestClient(t)
	seedConfig(t, c, "trq", "principal://iam/workforcePools/p/subject/me", "proj", "us-central1")
	if w := c.LoginConfigWarning("trq"); !strings.Contains(w, "gctx login-config trq") {
		t.Fatalf("warning = %q, want fix hint", w)
	}

	seedConfig(t, c, "prod", "me@x.com", "proj", "us-central1")
	if w := c.LoginConfigWarning("prod"); w != "" {
		t.Fatalf("warning for regular account = %q, want empty", w)
	}
}

func TestPreviousMissing(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.Previous(); !errors.Is(err, ErrNoPrevious) {
		t.Fatalf("err = %v, want ErrNoPrevious", err)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := writeFileAtomic(path, []byte("hello")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
}
