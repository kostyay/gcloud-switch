// Package gcloud is the sole interface for gcloud CLI configuration state. It
// reads and writes the config files directly; the only time it shells out to
// the gcloud binary is the interactive re-login offered after a switch.
package gcloud

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/ini.v1"
)

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

// Client operates on a gcloud config root and gctx's own state file.
type Client struct {
	root      string
	statePath string
}

// New returns a Client rooted at the gcloud config directory. statePath is the
// file where gctx records the previously active configuration.
func New(root, statePath string) *Client {
	return &Client{root: root, statePath: statePath}
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
	return c.savePrevious(current)
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
