package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"

	"github.com/lioilsources/CeskyDabing/internal/store/db"
)

func (a *app) addCmd() *cobra.Command {
	var slug, title string
	cmd := &cobra.Command{
		Use:   "add <serial> <input.mkv>",
		Short: "Přidá díl do seriálu (seriál se založí, pokud neexistuje)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			series, input := slugify(args[0]), args[1]
			input, err := filepath.Abs(input)
			if err != nil {
				return err
			}
			if _, err := os.Stat(input); err != nil {
				return err
			}
			if slug == "" {
				slug = episodeSlug(input)
			}
			if title == "" {
				title = args[0]
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := cmd.Context()
			s, err := st.UpsertSeries(ctx, db.UpsertSeriesParams{Slug: series, Title: title})
			if err != nil {
				return err
			}
			if _, err := st.GetEpisode(ctx, db.GetEpisodeParams{SeriesID: s.ID, Slug: slug}); err == nil {
				return fmt.Errorf("díl %s/%s už existuje", series, slug)
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			workDir := a.cfg.WorkDir(series, slug)
			if err := os.MkdirAll(workDir, 0o755); err != nil {
				return err
			}
			if _, err := st.CreateEpisode(ctx, db.CreateEpisodeParams{
				SeriesID: s.ID, Slug: slug, InputPath: input, WorkDir: workDir,
			}); err != nil {
				return err
			}
			fmt.Printf("přidáno %s/%s → %s\n", series, slug, workDir)
			return nil
		},
	}
	cmd.Flags().StringVar(&slug, "episode", "", "jméno dílu (výchozí SxxEyy z názvu souboru)")
	cmd.Flags().StringVar(&title, "title", "", "název seriálu při založení")
	return cmd
}

var (
	reSxE   = regexp.MustCompile(`(?i)s(\d{1,2})[ ._-]?e(\d{1,3})`)
	reNonID = regexp.MustCompile(`[^a-z0-9]+`)
)

// episodeSlug: "Bluey.S02E07.1080p.mkv" → "s02e07", jinak slug z názvu souboru.
func episodeSlug(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if m := reSxE.FindStringSubmatch(base); m != nil {
		return "s" + pad(m[1], 2) + "e" + pad(m[2], 2)
	}
	return slugify(base)
}

func pad(s string, n int) string {
	for len(s) < n {
		s = "0" + s
	}
	return s
}

// slugify: "Prasátko Pepa" → "prasatko-pepa".
func slugify(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return strings.Trim(reNonID.ReplaceAllString(b.String(), "-"), "-")
}
