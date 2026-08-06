# gcloud-switch (`gctx`)

kubectx for gcloud configurations. Fast, native switching between
`gcloud config configurations` — no Python cold-start.

`gctx` reads and writes gcloud's config files directly
(`~/.config/gcloud/configurations/` and `active_config`), so switching is
instant.

## Install

```bash
brew install kostyay/tap/gctx
```

Or with Go:

```bash
go install github.com/kostyay/gcloud-switch/cmd/gctx@latest
```

## Usage

```bash
gctx                    # fuzzy-pick a configuration to activate (with preview)
gctx <name>             # activate <name>
gctx -                  # activate the previous configuration
gctx -c                 # print the active configuration
gctx rename <old> <new> # rename a configuration
gctx -h                 # help
```

The no-arg picker marks the currently active configuration, and flags any that
need a login with `(login required)`. Each item's preview shows
its account, project, region, and its `login_config_file` when set. Switching is
global — it changes gcloud's active configuration for every shell, exactly like
`gcloud config configurations activate`.

## Auth check

After switching, `gctx` verifies the new configuration's credentials by
attempting a token refresh directly (no `gcloud` subprocess). If the credentials
are expired — common with Workforce Identity Federation / BeyondCorp configs
that require a browser re-login — it prints the exact login command and offers
to run it:

```
gctx: credentials for "acme-prod" (principal://…) are expired.
Log in with: gcloud auth login --login-config=/path/to/login-config.json
Log in now? [y/N]
```

A configuration that has never been logged in gets the same offer. gcloud only
writes `[core] account` after a successful login, so such a config has no
account at all — it is recognised by its `[auth] login_config_file`:

```
gctx: configuration "acme-prod" has never been logged in.
Log in with: gcloud auth login --login-config=/path/to/login-config.json
Log in now? [y/N]
```

The command uses the configuration's `[auth] login_config_file` when set. If
that file is missing (for example, it was written to a temp path that has since
been cleared), `gctx` reports the dangling path and shows the command to repoint
the configuration instead of running a login that would fail. A configuration
with no account set is never flagged — there is nothing to authenticate.

## Configuration

`gctx` honors `CLOUDSDK_CONFIG` for the gcloud config root, falling back to
`~/.config/gcloud`. Its own state (the "previous" configuration) is stored under
`$XDG_CONFIG_HOME/gcloud-switch/` (or `~/.config/gcloud-switch/`).

## License

MIT — see [LICENSE](LICENSE).
