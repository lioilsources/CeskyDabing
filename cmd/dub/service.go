package main

import (
	_ "embed"
	"os"
	"path/filepath"
	"text/template"

	"github.com/spf13/cobra"
)

//go:embed launchd.plist.tmpl
var launchdTmpl string

const launchdLabel = "io.github.lioilsources.dub"

// serviceCmd renders the launchd agent for `dub serve` (M4). Paths are filled in at
// runtime so no home directory ends up in the repo.
func (a *app) serviceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "service", Short: "Služba launchd pro dub serve (M4)"}
	cmd.AddCommand(&cobra.Command{
		Use:   "plist",
		Short: "Vypíše launchd plist; ulož ho do ~/Library/LaunchAgents/" + launchdLabel + ".plist",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			bin, err := os.Executable()
			if err != nil {
				return err
			}
			if bin, err = filepath.EvalSymlinks(bin); err != nil {
				return err
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			t := template.Must(template.New("plist").Parse(launchdTmpl))
			return t.Execute(os.Stdout, map[string]string{
				"Label":  launchdLabel,
				"Binary": bin,
				"Log":    filepath.Join(home, "Library", "Logs", "dub.log"),
			})
		},
	})
	return cmd
}
