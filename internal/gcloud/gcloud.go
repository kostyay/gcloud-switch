// Package gcloud is the sole interface for gcloud CLI configuration state. It
// reads and writes the config files directly; the only time it shells out to
// the gcloud binary is the interactive re-login offered after a switch.
package gcloud

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/ini.v1"
)

// workforcePoolMarker identifies a workforce identity federation principal,
// whose interactive login must go through a login config file rather than the
// standard Google OAuth account chooser.
const workforcePoolMarker = "/workforcePools/"

// defaultConfigName is the configuration gcloud treats as active when the
// active_config pointer file is absent.
const defaultConfigName = "default"

const configFilePrefix = "config_"

// ErrNotConfigured is returned when the gcloud config root does not exist.
var ErrNotConfigured = errors.New("gcloud not configured — run 'gcloud init'")

// ErrNoConfigurations is returned when the config root has no configurations.
var ErrNoConfigurations = errors.New("no gcloud configurations found")

// ErrNotFound is returned when a named configuration does not exist.
var ErrNotFound = errors.New("configuration not found")

// ErrNoPrevious is returned when no previous configuration has been recorded.
var ErrNoPrevious = errors.New("no previous configuration")

// ErrExists is returned when renaming onto a configuration that already exists.
var ErrExists = errors.New("configuration already exists")

// ErrInvalidName is returned when a configuration name violates gcloud's naming
// rule (a lowercase letter followed by lowercase letters, digits, or hyphens).
var ErrInvalidName = errors.New("invalid configuration name")

// validName matches gcloud's configuration naming rule.
var validName = regexp.MustCompile(`^[a-z][-a-z0-9]*$`)

// Config describes a single gcloud configuration.
type Config struct {
	Name    string
	Account string
	Project string
	Region  string
	// LoginConfigFile is the [auth] login_config_file property, set for
	// configurations that authenticate via a workforce/BeyondCorp login config.
	LoginConfigFile string
}

// IsWorkforce reports whether the configuration authenticates as a workforce
// identity federation principal.
func (cfg Config) IsWorkforce() bool {
	return strings.Contains(cfg.Account, workforcePoolMarker)
}

// NeedsLoginConfig reports a workforce configuration whose login_config_file is
// not persisted. Without it, gcloud (and gctx's re-login) silently fall back to
// the standard Google OAuth flow instead of the workforce login.
func (cfg Config) NeedsLoginConfig() bool {
	return cfg.IsWorkforce() && cfg.LoginConfigFile == ""
}

// Client operates on a gcloud config root and gctx's own state file.
type Client struct {
	root          string
	statePath     string
	kubeCachePath string
}

// New returns a Client rooted at the gcloud config directory. statePath is the
// file where gctx records the previously active configuration. kubeCachePath is
// the gke-gcloud-auth-plugin token cache, invalidated on every switch.
func New(root, statePath, kubeCachePath string) *Client {
	return &Client{root: root, statePath: statePath, kubeCachePath: kubeCachePath}
}

func (c *Client) configurationsDir() string {
	return filepath.Join(c.root, "configurations")
}

func (c *Client) activeConfigPath() string {
	return filepath.Join(c.root, "active_config")
}

func (c *Client) configPath(name string) string {
	return filepath.Join(c.configurationsDir(), configFilePrefix+name)
}

// List returns all configurations, sorted by name.
func (c *Client) List() ([]Config, error) {
	entries, err := os.ReadDir(c.configurationsDir())
	if err != nil {
		if os.IsNotExist(err) {
			if _, statErr := os.Stat(c.root); os.IsNotExist(statErr) {
				return nil, ErrNotConfigured
			}
			return nil, ErrNoConfigurations
		}
		return nil, fmt.Errorf("read configurations dir: %w", err)
	}

	var configs []Config
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), configFilePrefix) {
			continue
		}
		cfg, err := c.parseConfig(strings.TrimPrefix(e.Name(), configFilePrefix))
		if err != nil {
			return nil, err
		}
		configs = append(configs, cfg)
	}
	if len(configs) == 0 {
		return nil, ErrNoConfigurations
	}
	sort.Slice(configs, func(i, j int) bool { return configs[i].Name < configs[j].Name })
	return configs, nil
}

func (c *Client) parseConfig(name string) (Config, error) {
	f, err := ini.Load(c.configPath(name))
	if err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", name, err)
	}
	core := f.Section("core")
	return Config{
		Name:            name,
		Account:         core.Key("account").String(),
		Project:         core.Key("project").String(),
		Region:          f.Section("compute").Key("region").String(),
		LoginConfigFile: f.Section("auth").Key("login_config_file").String(),
	}, nil
}

// Current returns the name of the active configuration. When the active_config
// pointer is absent, gcloud treats "default" as active.
func (c *Client) Current() (string, error) {
	data, err := os.ReadFile(c.activeConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return defaultConfigName, nil
		}
		return "", fmt.Errorf("read active_config: %w", err)
	}
	name := strings.TrimSpace(string(data))
	if name == "" {
		return defaultConfigName, nil
	}
	return name, nil
}

// Switch activates the named configuration and records the previously active
// one, unless the target is already active (a no-op).
func (c *Client) Switch(name string) error {
	if err := c.ensureExists(name); err != nil {
		return err
	}
	current, err := c.Current()
	if err != nil {
		return err
	}
	if current == name {
		return nil
	}
	if err := writeFileAtomic(c.activeConfigPath(), []byte(name)); err != nil {
		return fmt.Errorf("write active_config: %w", err)
	}
	if err := c.clearKubeAuthCache(); err != nil {
		return err
	}
	return c.savePrevious(current)
}

// Rename renames a configuration, updating the active_config pointer and the
// recorded previous configuration when they refer to the old name.
func (c *Client) Rename(oldName, newName string) error {
	if !validName.MatchString(newName) {
		return fmt.Errorf("%q: %w", newName, ErrInvalidName)
	}
	if err := c.ensureExists(oldName); err != nil {
		return err
	}
	if _, err := os.Stat(c.configPath(newName)); err == nil {
		return fmt.Errorf("%q: %w", newName, ErrExists)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat config %q: %w", newName, err)
	}
	if err := os.Rename(c.configPath(oldName), c.configPath(newName)); err != nil {
		return fmt.Errorf("rename config: %w", err)
	}

	current, err := c.Current()
	if err != nil {
		return err
	}
	if current == oldName {
		if err := writeFileAtomic(c.activeConfigPath(), []byte(newName)); err != nil {
			return fmt.Errorf("write active_config: %w", err)
		}
	}
	if prev, err := c.Previous(); err == nil && prev == oldName {
		return c.savePrevious(newName)
	}
	return nil
}

// SetLoginConfig persists the [auth] login_config_file property for a
// configuration, so its interactive login uses the workforce flow. The path
// must exist; it is stored as an absolute path.
func (c *Client) SetLoginConfig(name, path string) error {
	if err := c.ensureExists(name); err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve login config path %q: %w", path, err)
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("login config file %q: %w", abs, err)
	}
	return c.writeLoginConfig(name, abs)
}

// ClearLoginConfig removes the [auth] login_config_file property from a
// configuration, dropping the [auth] section when it becomes empty.
func (c *Client) ClearLoginConfig(name string) error {
	if err := c.ensureExists(name); err != nil {
		return err
	}
	return c.writeLoginConfig(name, "")
}

// writeLoginConfig sets (or, with an empty path, deletes) the login_config_file
// key and writes the config back atomically.
func (c *Client) writeLoginConfig(name, path string) error {
	f, err := ini.Load(c.configPath(name))
	if err != nil {
		return fmt.Errorf("parse config %q: %w", name, err)
	}
	sec := f.Section("auth")
	if path == "" {
		sec.DeleteKey("login_config_file")
		if len(sec.Keys()) == 0 {
			f.DeleteSection("auth")
		}
	} else {
		sec.Key("login_config_file").SetValue(path)
	}
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		return fmt.Errorf("render config %q: %w", name, err)
	}
	return writeFileAtomic(c.configPath(name), buf.Bytes())
}

// LoginConfigWarning returns a hint when the named configuration is a workforce
// account missing its login_config_file, or "" when there is nothing to warn.
func (c *Client) LoginConfigWarning(name string) string {
	cfg, err := c.parseConfig(name)
	if err != nil || !cfg.NeedsLoginConfig() {
		return ""
	}
	return fmt.Sprintf(
		"warning: %q is a workforce account but auth/login_config_file is not set.\n"+
			"  fix: gctx login-config %s <path>",
		cfg.Account, name)
}

func (c *Client) ensureExists(name string) error {
	if _, err := os.Stat(c.configPath(name)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%q: %w", name, ErrNotFound)
		}
		return fmt.Errorf("stat config %q: %w", name, err)
	}
	return nil
}

// Previous returns the last active configuration recorded before the most
// recent switch, or ErrNoPrevious when none exists.
func (c *Client) Previous() (string, error) {
	data, err := os.ReadFile(c.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNoPrevious
		}
		return "", fmt.Errorf("read previous: %w", err)
	}
	name := strings.TrimSpace(string(data))
	if name == "" {
		return "", ErrNoPrevious
	}
	return name, nil
}

// clearKubeAuthCache removes the gke-gcloud-auth-plugin token cache so kubectl
// re-mints a token for the newly active account instead of serving the stale
// one. A missing cache is not an error.
func (c *Client) clearKubeAuthCache() error {
	if c.kubeCachePath == "" {
		return nil
	}
	if err := os.Remove(c.kubeCachePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear kube auth cache: %w", err)
	}
	return nil
}

func (c *Client) savePrevious(name string) error {
	if err := os.MkdirAll(filepath.Dir(c.statePath), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	if err := writeFileAtomic(c.statePath, []byte(name)); err != nil {
		return fmt.Errorf("write previous: %w", err)
	}
	return nil
}

// writeFileAtomic writes data to path via a temp file and rename so a crash
// mid-write cannot corrupt the target.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
