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
	"github.com/kostyay/gcloud-switch/internal/gcloud"
	"github.com/ktr0731/go-fuzzyfinder"
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
		Action: action,
	}
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
	return gcloud.New(root, statePath), nil
}

func activate(ctx context.Context, client *gcloud.Client, name string) error {
	if err := client.Switch(name); err != nil {
		return err
	}
	fmt.Printf("Switched to %q.\n", name)
	offerRelogin(client.VerifyAuth(ctx, name))
	return nil
}

// offerRelogin warns about expired credentials and, on confirmation, runs the
// login command. It is a no-op when the plan is nil (credentials fine).
func offerRelogin(plan *gcloud.ReloginPlan) {
	if plan == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "\nCredentials for %q (%s) are expired.\n", plan.Config.Name, plan.Config.Account)

	if missing := plan.MissingLoginConfig(); missing != "" {
		fmt.Fprintf(os.Stderr, "Its login config file is missing: %s\n", missing)
		fmt.Fprintf(os.Stderr, "Point the config at a valid login config, then re-login:\n  %s\n", plan.FixLoginConfigCommand())
		return
	}

	fmt.Fprintf(os.Stderr, "Re-login with: %s\n", plan.Command())
	if !confirm("Re-login now?") {
		return
	}
	if err := plan.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "gctx: "+err.Error())
	}
}

// pick runs the interactive fuzzy picker over all configurations.
func pick(ctx context.Context, client *gcloud.Client) error {
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

	idx, err := fuzzyfinder.Find(
		configs,
		func(i int) string { return itemLabel(configs[i], current) },
		fuzzyfinder.WithPreviewWindow(func(i, _, _ int) string {
			if i < 0 {
				return ""
			}
			return preview(configs[i])
		}),
	)
	if err != nil {
		if errors.Is(err, fuzzyfinder.ErrAbort) {
			return nil
		}
		return fmt.Errorf("select configuration: %w", err)
	}
	return activate(ctx, client, configs[idx].Name)
}

func itemLabel(c gcloud.Config, current string) string {
	if c.Name == current {
		return c.Name + " (active)"
	}
	return c.Name
}

func preview(c gcloud.Config) string {
	return fmt.Sprintf("configuration: %s\n\naccount: %s\nproject: %s\nregion:  %s",
		c.Name, orDash(c.Account), orDash(c.Project), orDash(c.Region))
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
