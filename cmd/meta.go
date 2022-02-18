package cmd

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/b4b4r07/afx/pkg/config"
	"github.com/b4b4r07/afx/pkg/env"
	"github.com/b4b4r07/afx/pkg/errors"
	"github.com/b4b4r07/afx/pkg/helpers/shell"
	"github.com/b4b4r07/afx/pkg/state"
	"github.com/fatih/color"
	"github.com/mitchellh/cli"
)

type meta struct {
	Env       *env.Config
	Packages  []config.Package
	AppConfig *config.AppConfig
	State     *state.State

	UI cli.Ui
}

func (m *meta) init(args []string) error {
	m.UI = &cli.ColoredUi{
		Ui: &cli.BasicUi{
			Reader:      os.Stdin,
			Writer:      os.Stdout,
			ErrorWriter: os.Stderr,
		},
		OutputColor: cli.UiColor{Bold: false},
		InfoColor:   cli.UiColor{Code: int(color.FgWhite), Bold: true},
		ErrorColor:  cli.UiColorRed,
		WarnColor:   cli.UiColorYellow,
	}

	root := filepath.Join(os.Getenv("HOME"), ".afx")
	cfgRoot := filepath.Join(os.Getenv("HOME"), ".config", "afx")
	cache := filepath.Join(root, "cache.json")

	files, err := config.WalkDir(cfgRoot)
	if err != nil {
		return errors.Wrapf(err, "%s: failed to walk dir", cfgRoot)
	}

	var pkgs []config.Package
	app := &config.DefaultAppConfig
	for _, file := range files {
		cfg, err := config.Read(file)
		if err != nil {
			return errors.Wrapf(err, "%s: failed to read config", file)
		}
		parsed, err := cfg.Parse()
		if err != nil {
			return errors.Wrapf(err, "%s: failed to parse config", file)
		}
		pkgs = append(pkgs, parsed...)

		if cfg.AppConfig != nil {
			app = cfg.AppConfig
		}
	}

	m.AppConfig = app

	if err := config.Validate(pkgs); err != nil {
		return errors.Wrap(err, "failed to validate packages")
	}

	pkgs, err = config.Sort(pkgs)
	if err != nil {
		return errors.Wrap(err, "failed to resolve dependencies between packages")
	}

	m.Packages = pkgs

	m.Env = env.New(cache)
	m.Env.Add(env.Variables{
		"AFX_CONFIG_PATH":  env.Variable{Value: cfgRoot},
		"AFX_LOG":          env.Variable{},
		"AFX_LOG_PATH":     env.Variable{},
		"AFX_COMMAND_PATH": env.Variable{Default: filepath.Join(os.Getenv("HOME"), "bin")},
		"AFX_SHELL":        env.Variable{Default: m.AppConfig.Shell},
		"AFX_SUDO_PASSWORD": env.Variable{
			Input: env.Input{
				When:    config.HasSudoInCommandBuildSteps(m.Packages),
				Message: "Please enter sudo command password",
				Help:    "Some packages build steps requires sudo command",
			},
		},
		"GITHUB_TOKEN": env.Variable{
			Input: env.Input{
				When:    config.HasGitHubReleaseBlock(m.Packages),
				Message: "Please type your GITHUB_TOKEN",
				Help:    "To fetch GitHub Releases, GitHub token is required",
			},
		},
	})

	log.Printf("[DEBUG] mkdir %s\n", root)
	os.MkdirAll(root, os.ModePerm)

	log.Printf("[DEBUG] mkdir %s\n", os.Getenv("AFX_COMMAND_PATH"))
	os.MkdirAll(os.Getenv("AFX_COMMAND_PATH"), os.ModePerm)

	s, err := state.Open(filepath.Join(root, "state.json"), m.Packages)
	if err != nil {
		return errors.Wrap(err, "faield to open state file")
	}
	m.State = s

	log.Printf("[INFO] state additions: (%d) %#v",
		len(s.Additions), getNameInPackages(s.Additions))
	log.Printf("[INFO] state readditions: (%d) %#v",
		len(s.Readditions), getNameInPackages(s.Readditions))
	log.Printf("[INFO] state deletions: (%d) %#v",
		len(s.Deletions), getNameInResources(s.Deletions))
	log.Printf("[INFO] state changes: (%d) %#v",
		len(s.Changes), getNameInPackages(s.Changes))
	log.Printf("[INFO] state unchanges: (%d) []string{...skip...}", len(s.NoChanges))

	return nil
}

func (m *meta) Select() (config.Package, error) {
	var stdin, stdout bytes.Buffer

	cmd := shell.Shell{
		Stdin:   &stdin,
		Stdout:  &stdout,
		Stderr:  os.Stderr,
		Command: m.AppConfig.Filter.Command,
		Args:    m.AppConfig.Filter.Args,
		Env:     m.AppConfig.Filter.Env,
	}

	for _, pkg := range m.Packages {
		fmt.Fprintln(&stdin, pkg.GetName())
	}

	if err := cmd.Run(context.Background()); err != nil {
		return nil, err
	}

	search := func(name string) config.Package {
		for _, pkg := range m.Packages {
			if pkg.GetName() == name {
				return pkg
			}
		}
		return nil
	}

	for _, line := range strings.Split(stdout.String(), "\n") {
		if pkg := search(line); pkg != nil {
			return pkg, nil
		}
	}

	return nil, errors.New("pkg not found")
}

func getNameInPackages(pkgs []config.Package) []string {
	var keys []string
	for _, pkg := range pkgs {
		keys = append(keys, pkg.GetName())
	}
	return keys
}

func getNameInResources(resources []state.Resource) []string {
	var keys []string
	for _, resource := range resources {
		keys = append(keys, resource.Name)
	}
	return keys
}
