package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/lioilsources/CeskyDabing/internal/cast"
	"github.com/lioilsources/CeskyDabing/internal/media"
	"github.com/lioilsources/CeskyDabing/internal/store"
	"github.com/lioilsources/CeskyDabing/internal/store/db"
)

const (
	FileIdentify = "identify.json"
	FileCast     = "cast.yaml"
)

// SeriesDir is series/<name>/ under the storage root.
func SeriesDir(root, series string) string { return filepath.Join(root, "series", series) }

// Identify: diarized speakers → characters of the series (cast.yaml), then the
// DB cache of characters and segments.character_id follow the file.
type Identify struct {
	Store *store.Store
	Root  string
	Log   *slog.Logger
}

func (s Identify) Run(ctx context.Context, ep *Episode) (Outputs, error) {
	tr, err := ReadTranscript(filepath.Join(ep.WorkDir, FileSegments))
	if err != nil {
		return nil, err
	}
	castPath := filepath.Join(SeriesDir(s.Root, ep.Series), FileCast)
	c, err := cast.Load(castPath)
	if err != nil {
		return nil, err
	}
	speakers := make([]cast.Speaker, 0, len(tr.Speakers))
	for _, sp := range tr.Speakers {
		speakers = append(speakers, cast.Speaker{
			ID: sp.Id, Embedding: sp.Embedding, F0Mean: deref(sp.F0Mean), F0Std: deref(sp.F0Std),
		})
	}
	assigned := c.Identify(ep.Slug, speakers)
	for _, a := range assigned {
		if a.New {
			s.Log.Warn("nová postava, potvrď nebo přejmenuj v cast.yaml", "speaker", a.Speaker, "character", a.Character)
		}
	}
	if err := c.Save(castPath); err != nil {
		return nil, err
	}
	if err := s.sync(ctx, ep, c, assigned); err != nil {
		return nil, fmt.Errorf("characters: %w", err)
	}
	if err := media.WriteJSON(filepath.Join(ep.WorkDir, FileIdentify), assigned); err != nil {
		return nil, err
	}
	return Outputs{"identify": FileIdentify}, nil
}

// sync makes the characters table match cast.yaml and links the episode's segments.
func (s Identify) sync(ctx context.Context, ep *Episode, c *cast.Cast, assigned []cast.Assignment) error {
	return s.Store.Tx(ctx, func(q *db.Queries) error {
		ids := map[string]int64{}
		for _, ch := range c.Characters {
			strategy, speed := ch.Strategy, ch.Speed
			if strategy == "" {
				strategy = "A"
			}
			if speed == 0 {
				speed = 1
			}
			id, err := q.UpsertCharacter(ctx, db.UpsertCharacterParams{
				SeriesID: ep.SeriesID, Name: ch.Name, Centroid: cast.Blob(ch.Vec()),
				CentroidCount: int64(len(ch.Episodes)), F0Mean: nonZero(ch.F0Mean), F0Std: nonZero(ch.F0Std),
				Gender: ch.Gender, AgeGroup: ch.AgeGroup, Strategy: strategy, RefPath: ch.Ref,
				Speed: speed, Notes: ch.Notes,
			})
			if err != nil {
				return fmt.Errorf("%s: %w", ch.Name, err)
			}
			ids[ch.Name] = id
		}
		// Characters removed or renamed in cast.yaml leave the cache.
		rows, err := q.ListCharacters(ctx, ep.SeriesID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if _, keep := ids[r.Name]; keep {
				continue
			}
			if err := q.UnlinkCharacterSegments(ctx, &r.ID); err != nil {
				return err
			}
			if err := q.DeleteCharacter(ctx, r.ID); err != nil {
				return err
			}
		}
		for _, a := range assigned {
			id := ids[a.Character]
			if err := q.SetSegmentsCharacter(ctx, db.SetSegmentsCharacterParams{
				CharacterID: &id, EpisodeID: ep.ID, Speaker: a.Speaker,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func nonZero(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}
