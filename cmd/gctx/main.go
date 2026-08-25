// Command gctx switches the active gcloud configuration, kubectx-style.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/kostyay/gcloud-switch/internal/env"
	"github.com/kostyay/gcloud-switch/internal/fuzzyfinder"
	"github.com/kostyay/gcloud-switch/internal/gcloud"
	"github.com/urfave/cli/v3"
)

func main() {
	if err := app().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "gctx: "+err.Error())
		os.Exit(1)
	}
}

func app() *cli.Command {
	return &cli.Command{
		Name:      "gctx",
		Usage:     "switch gcloud configurations",
		ArgsUsage: "[configuration|-]",
		Version:   version(),
		Description: "Fuzzy-pick a configuration to activate, or pass a name to switch " +
			"directly. Use '-' to switch to the previous configuration.",
		HideHelpCommand: true,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "current",
				Aliases: []string{"c"},
				Usage:   "print the active configuration and exit",
			},
		},
		Commands: []*cli.Command{
			{
				Name:      "rename",
				Usage:     "rename a configuration",
				ArgsUsage: "<old> <new>",
				Action:    renameAction,
			},
			{
				Name:      "login-config",
				Usage:     "set or clear a configuration's workforce login config file",
				ArgsUsage: "<configuration> <path>",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "clear",
						Usage: "remove the login config file instead of setting it",
					},
				},
				Action: loginConfigAction,
			},
		},
		Action: action,
	}
}

func renameAction(_ context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() != 2 {
		return fmt.Errorf("rename requires exactly two arguments: <old> <new>")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	oldName, newName := args.Get(0), args.Get(1)
	if err := client.Rename(oldName, newName); err != nil {
		return err
	}
	fmt.Printf("Renamed %q to %q.\n", oldName, newName)
	return nil
}

func loginConfigAction(_ context.Context, cmd *cli.Command) error {
	client, err := newClient()
	if err != nil {
		return err
	}
	args := cmd.Args()
	name := args.First()
	if name == "" {
		return fmt.Errorf("login-config requires a configuration name")
	}
	if cmd.Bool("clear") {
		if err := client.ClearLoginConfig(name); err != nil {
			return err
		}
		fmt.Printf("Cleared login config file for %q.\n", name)
		return nil
	}
	if args.Len() != 2 {
		return fmt.Errorf("login-config requires <configuration> <path> (or --clear)")
	}
	if err := client.SetLoginConfig(name, args.Get(1)); err != nil {
		return err
	}
	fmt.Printf("Set login config file for %q.\n", name)
	return nil
}

func action(ctx context.Context, cmd *cli.Command) error {
	client, err := newClient()
	if err != nil {
		return err
	}

	if cmd.Bool("current") {
		cur, err := client.Current()
		if err != nil {
			return err
		}
		fmt.Println(cur)
		return nil
	}

	args := cmd.Args()
	if args.Len() == 0 {
		return pick(ctx, client)
	}

	name := args.First()
	if name == "-" {
		if name, err = client.Previous(); err != nil {
			return err
		}
	}
	return activate(ctx, client, name)
}

func newClient() (*gcloud.Client, error) {
	root, err := env.GcloudRoot()
	if err != nil {
		return nil, err
	}
	statePath, err := env.PreviousStatePath()
	if err != nil {
		return nil, err
	}
	kubeCachePath, err := env.KubeAuthCachePath()
	if err != nil {
		return nil, err
	}
	return gcloud.New(root, statePath, kubeCachePath), nil
}

func activate(ctx context.Context, client *gcloud.Client, name string) error {
	if err := client.Switch(name); err != nil {
		return err
	}
	fmt.Printf("Switched to %q.\n", name)
	if w := client.LoginConfigWarning(name); w != "" {
		fmt.Fprintln(os.Stderr, w)
	}
	offerLogin(client.VerifyAuth(ctx, name))
	return nil
}

// offerLogin explains why a login is needed and, on confirmation, runs the
// login command. It is a no-op when the plan is nil (credentials fine).
func offerLogin(plan *gcloud.ReloginPlan) {
	if plan == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "\ngctx: %s.\n", plan.Reason())

	if missing := plan.MissingLoginConfig(); missing != "" {
		fmt.Fprintf(os.Stderr, "Its login config file is missing: %s\n", missing)
		fmt.Fprintf(os.Stderr, "Point the config at a valid login config, then log in:\n  %s\n", plan.FixLoginConfigCommand())
		return
	}

	fmt.Fprintf(os.Stderr, "Log in with: %s\n", plan.Command())
	if !confirm("Log in now?") {
		return
	}
	if err := plan.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "gctx: "+err.Error())
	}
}

// pick runs the interactive fuzzy picker over all configurations.
func pick(ctx context.Context, client *gcloud.Client) error {
	for {
		configs, err := client.List()
		if err != nil {
			return err
		}
		current, err := client.Current()
		if err != nil {
			return err
		}

		if len(configs) == 1 {
			fmt.Printf("only one configuration: %s (active)\n", configs[0].Name)
			return nil
		}

		needsLogin := client.LoginRequired(ctx, configs)
		idx, err := fuzzyfinder.Find(
			configs,
			func(i int) string { return itemLabel(configs[i], current, needsLogin[i]) },
			fuzzyfinder.WithHeader("[enter] switch  [i] info  [r] re-login  [esc] cancel"),
			fuzzyfinder.WithHotkey('r'),
			fuzzyfinder.WithPreviewWindow(func(i, _, _ int) string {
				if i < 0 {
					return ""
				}
				return preview(configs[i])
			}),
		)
		if errors.Is(err, fuzzyfinder.ErrHotkey) {
			reopen, err := relogin(client, configs[idx].Name)
			if err != nil {
				return err
			}
			if !reopen {
				return nil
			}
			continue
		}
		if err != nil {
			if errors.Is(err, fuzzyfinder.ErrAbort) {
				return nil
			}
			return fmt.Errorf("select configuration: %w", err)
		}
		return activate(ctx, client, configs[idx].Name)
	}
}

// relogin runs an explicitly requested login for the highlighted configuration.
// It reports whether the picker should reopen afterward.
func relogin(client *gcloud.Client, name string) (bool, error) {
	plan, err := client.LoginPlan(name)
	if err != nil {
		return false, err
	}
	if missing := plan.MissingLoginConfig(); missing != "" {
		fmt.Fprintf(os.Stderr, "gctx: login config file is missing: %s\n", missing)
		fmt.Fprintf(os.Stderr, "Point the config at a valid login config, then try again:\n  %s\n", plan.FixLoginConfigCommand())
		return false, nil
	}
	fmt.Fprintf(os.Stderr, "Logging in with: %s\n", plan.Command())
	if err := plan.Run(); err != nil {
		return false, fmt.Errorf("login to %q: %w", name, err)
	}
	return true, nil
}

func itemLabel(c gcloud.Config, current string, needsLogin bool) string {
	label := c.Name
	if c.Name == current {
		label += " (active)"
	}
	if needsLogin {
		label += " (login required)"
	}
	return label
}

func preview(c gcloud.Config) string {
	s := fmt.Sprintf("configuration: %s\n\naccount: %s\nproject: %s\nregion:  %s",
		c.Name, orDash(c.Account), orDash(c.Project), orDash(c.Region))
	if c.LoginConfigFile != "" {
		s += fmt.Sprintf("\nlogin config: %s", c.LoginConfigFile)
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// confirm asks a yes/no question on stderr and reads the answer from stdin.
// The default (empty input) is no.
func confirm(prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// versionString is stamped by goreleaser via -ldflags "-X main.versionString=...".
var versionString string

// version reports the release version stamped into the binary, falling back to
// the module version, or "dev" for local builds.
func version() string {
	if versionString != "" {
		return versionString
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "dev"
}
