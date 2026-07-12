package gcloud

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/auth/credentials"
)

// authScope is requested when building a token source; refresh does not depend
// on it, but the credential loader requires a scope.
const authScope = "https://www.googleapis.com/auth/cloud-platform"

// authCheckTimeout bounds the live token-refresh network call.
const authCheckTimeout = 10 * time.Second

// errNeedsReauth signals that a stored credential can no longer produce an
// access token and the account must log in again.
var errNeedsReauth = errors.New("credentials expired, re-login required")

// errNoCredentials signals that no stored credential exists for an account, so
// its auth state cannot be verified.
var errNoCredentials = errors.New("no stored credentials for account")

// errNoAccount signals that a configuration has no account set, so there is
// nothing to authenticate — distinct from an account whose credentials are
// missing, and never a re-login state.
var errNoAccount = errors.New("no account configured")

// ReloginPlan describes how to re-authenticate a configuration whose
// credentials have expired.
type ReloginPlan struct {
	Config Config
}

// Command returns the gcloud login command as it would be run.
func (p *ReloginPlan) Command() string {
	return strings.Join(loginCommand(p.Config), " ")
}

// MissingLoginConfig returns the login_config_file path when the config points
// at one that does not exist — a dangling reference that would make the login
// command fail. It returns "" when there is no such problem.
func (p *ReloginPlan) MissingLoginConfig() string {
	path := p.Config.LoginConfigFile
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return path
	}
	return ""
}

// FixLoginConfigCommand returns the gcloud command to repoint the active
// configuration at a valid login config file.
func (p *ReloginPlan) FixLoginConfigCommand() string {
	return "gcloud config set auth/login_config_file <path-to-login-config.json>"
}

// Run executes the login command, wiring the current process's stdio so the
// interactive login flow works.
func (p *ReloginPlan) Run() error {
	cmd := loginCommand(p.Config)
	bin, err := exec.LookPath(cmd[0])
	if err != nil {
		return fmt.Errorf("gcloud not found on PATH: %w", err)
	}
	proc := exec.Command(bin, cmd[1:]...)
	proc.Stdin, proc.Stdout, proc.Stderr = os.Stdin, os.Stdout, os.Stderr
	return proc.Run()
}

// VerifyAuth checks whether the named configuration's credentials can still
// produce an access token, performing a live token refresh. It returns a
// ReloginPlan when a re-login is required, or nil when the credentials are
// valid or cannot be verified (best-effort).
func (c *Client) VerifyAuth(ctx context.Context, name string) *ReloginPlan {
	cfg, err := c.parseConfig(name)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, authCheckTimeout)
	defer cancel()
	if needsRelogin(c.checkAuth(ctx, cfg.Account)) {
		return &ReloginPlan{Config: cfg}
	}
	return nil
}

// needsRelogin reports whether a checkAuth error means the account must log in
// again: its stored credentials are missing or can no longer produce a token.
func needsRelogin(err error) bool {
	return errors.Is(err, errNeedsReauth) || errors.Is(err, errNoCredentials)
}

// LoginRequired reports, per configuration (by position), whether its stored
// credentials are missing or can no longer produce an access token — i.e. a
// re-login is needed. Configurations without an account are never flagged.
// Checks perform live token refreshes and run concurrently, bounded by
// authCheckTimeout.
func (c *Client) LoginRequired(ctx context.Context, configs []Config) []bool {
	ctx, cancel := context.WithTimeout(ctx, authCheckTimeout)
	defer cancel()

	result := make([]bool, len(configs))
	var wg sync.WaitGroup
	for i, cfg := range configs {
		wg.Add(1)
		go func(i int, account string) {
			defer wg.Done()
			result[i] = needsRelogin(c.checkAuth(ctx, account))
		}(i, cfg.Account)
	}
	wg.Wait()
	return result
}

// loginCommand builds the gcloud login command for a config, using its
// login_config_file when present (workforce/BeyondCorp configs).
func loginCommand(cfg Config) []string {
	cmd := []string{"gcloud", "auth", "login"}
	if cfg.LoginConfigFile != "" {
		return append(cmd, "--login-config="+cfg.LoginConfigFile)
	}
	if cfg.Account != "" {
		cmd = append(cmd, cfg.Account)
	}
	return cmd
}

// credentialPath maps an account to its gcloud legacy credential file. gcloud
// stores the account verbatim as a directory, collapsing the "://" in
// workforce principals to a single path separator.
func (c *Client) credentialPath(account string) string {
	dir := strings.ReplaceAll(account, "://", "/")
	return filepath.Join(c.root, "legacy_credentials", dir, "adc.json")
}

// checkAuth reports whether the account's stored credentials can still produce
// an access token. It returns nil when valid, errNeedsReauth when a re-login is
// required, and errNoCredentials when the account has no stored credential.
func (c *Client) checkAuth(ctx context.Context, account string) error {
	if account == "" {
		return errNoAccount
	}
	data, err := os.ReadFile(c.credentialPath(account))
	if err != nil {
		if os.IsNotExist(err) {
			return errNoCredentials
		}
		return fmt.Errorf("read credentials: %w", err)
	}
	creds, err := credentials.DetectDefault(&credentials.DetectOptions{
		CredentialsJSON: data,
		Scopes:          []string{authScope},
	})
	if err != nil {
		return fmt.Errorf("load credentials: %w", err)
	}
	if _, err := creds.Token(ctx); err != nil {
		return fmt.Errorf("%w: %w", errNeedsReauth, err)
	}
	return nil
}
