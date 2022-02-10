package cmd

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/b4b4r07/afx/pkg/config"
	"github.com/b4b4r07/afx/pkg/errors"
	"github.com/b4b4r07/afx/pkg/templates"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

type installCmd struct {
	meta
}

var (
	// installLong is long description of fmt command
	installLong = templates.LongDesc(``)

	// installExample is examples for fmt command
	installExample = templates.Examples(`
		afx install [args...]

		By default, install tries to install all packages which are newly
		added to config file.
		If any args are given, tries to install only them.
	`)
)

// newInstallCmd creates a new fmt command
func newInstallCmd() *cobra.Command {
	c := &installCmd{}

	installCmd := &cobra.Command{
		Use:                   "install",
		Short:                 "Resume installation from paused part (idempotency)",
		Long:                  installLong,
		Example:               installExample,
		Aliases:               []string{"i"},
		DisableFlagsInUseLine: true,
		SilenceUsage:          true,
		SilenceErrors:         true,
		Args:                  cobra.MinimumNArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := c.meta.init(args); err != nil {
				return err
			}
			if c.parseErr != nil {
				return c.parseErr
			}
			c.Env.Ask(
				"AFX_SUDO_PASSWORD",
				"GITHUB_TOKEN",
			)
			return c.run(args)
		},
	}

	return installCmd
}

func (c *installCmd) run(args []string) error {
	eg := errgroup.Group{}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// TODO: Check if this process does not matter other concerns
	pkgs := append(c.State.Additions, c.State.Readditions...)
	if len(pkgs) == 0 {
		// TODO: improve message
		log.Printf("[INFO] No packages to install")
		return nil
	}

	// not install all new packages. Instead just only install
	// given packages when not installed yet.
	var given []config.Package
	for _, arg := range args {
		pkg, err := c.getFromAdditions(arg)
		if err != nil {
			// no hit in additions
			continue
		}
		given = append(given, pkg)
	}
	if len(given) > 0 {
		pkgs = given
	}

	progress := config.NewProgress(pkgs)
	completion := make(chan config.Status)

	go func() {
		progress.Print(completion)
	}()

	log.Printf("[DEBUG] start to run each pkg.Install()")
	results := make(chan installResult)
	for _, pkg := range pkgs {
		pkg := pkg
		eg.Go(func() error {
			err := pkg.Install(ctx, completion)
			switch err {
			case nil:
				c.State.Add(pkg)
			default:
				log.Printf("[DEBUG] uninstall %q because installation failed", pkg.GetName())
				pkg.Uninstall(ctx)
			}
			select {
			case results <- installResult{Package: pkg, Error: err}:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}

	go func() {
		eg.Wait()
		close(results)
	}()

	var exit errors.Errors
	for result := range results {
		exit.Append(result.Error)
	}
	if err := eg.Wait(); err != nil {
		log.Printf("[ERROR] failed to install: %s\n", err)
		exit.Append(err)
	}

	defer func(err error) {
		if err != nil {
			c.Env.Refresh()
		}
	}(exit.ErrorOrNil())

	return exit.ErrorOrNil()
}

func (c *installCmd) getFromAdditions(name string) (config.Package, error) {
	pkgs := append(c.State.Additions, c.State.Readditions...)

	for _, pkg := range pkgs {
		if pkg.GetName() == name {
			return pkg, nil
		}
	}

	return nil, errors.New("not found")
}

type installResult struct {
	Package config.Package
	Error   error
}