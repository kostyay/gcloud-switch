// Package env resolves filesystem paths from environment variables, keeping
// environment lookups out of business logic.
package env

import (
	"fmt"
	"os"
	"path/filepath"
)

// GcloudRoot resolves the gcloud config root: $CLOUDSDK_CONFIG, else
// ~/.config/gcloud (gcloud hardcodes ~/.config, ignoring XDG_CONFIG_HOME).
func GcloudRoot() (string, error) {
	if v := os.Getenv("CLOUDSDK_CONFIG"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "gcloud"), nil
}

// PreviousStatePath resolves gctx's state file recording the previously active
// configuration: <$XDG_CONFIG_HOME|~/.config>/gcloud-switch/previous.
func PreviousStatePath() (string, error) {
	home, err := configHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "gcloud-switch", "previous"), nil
}

// configHome resolves the user config base: $XDG_CONFIG_HOME, else ~/.config.
// Note: not os.UserConfigDir(), which returns ~/Library/Application Support on
// macOS; gctx deliberately uses ~/.config there to sit beside gcloud's own dir.
func configHome() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config"), nil
}
