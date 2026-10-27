package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/lioilsources/CeskyDabing/internal/pipeline"
)

func (a *app) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [serial] [díl...]",
		Short: "Stav kroků pipeline",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := cmd.Context()
			var series []string
			if len(args) > 0 {
				series = args[:1]
			} else {
				all, err := st.ListSeries(ctx)
				if err != nil {
					return err
				}
				for _, s := range all {
					series = append(series, s.Slug)
				}
			}
			runner := &pipeline.Runner{Store: st, Log: a.log}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			defer tw.Flush()
			for _, name := range series {
				var slugs []string
				if len(args) > 1 {
					slugs = args[1:]
				}
				eps, err := a.episodes(ctx, st, name, slugs)
				if err != nil {
					return err
				}
				for _, ep := range eps {
					steps, err := runner.Status(ctx, ep)
					if err != nil {
						return err
					}
					fmt.Fprintf(tw, "%s/%s\t%s\n", ep.Series, ep.Slug, ep.InputPath)
					for _, s := range steps {
						when := ""
						if s.FinishedAt != nil {
							when = *s.FinishedAt
						} else if s.StartedAt != nil {
							when = *s.StartedAt
						}
						fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", s.Step, s.Status, when, s.Error)
					}
				}
			}
			return nil
		},
	}
}
