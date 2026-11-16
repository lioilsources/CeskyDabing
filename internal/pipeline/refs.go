package pipeline

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/lioilsources/CeskyDabing/internal/cast"
	"github.com/lioilsources/CeskyDabing/internal/media"
	"github.com/lioilsources/CeskyDabing/internal/store"
	"github.com/lioilsources/CeskyDabing/internal/store/db"
)

const FileRefs = "refs.json"

// Reference clip rules (00-DABING_PLAN §4.3).
const (
	RefMinSegmentS = 3.0  // only segments at least this long
	RefTargetS     = 25.0 // stop collecting at this length
	RefMaxS        = 30.0 // never longer
	RefWarnS       = 6.0  // below this the clone will be weak; reported
	refGapS        = 0.15 // silence between the joined segments
	refRMSdB       = -20.0
	refPeak        = 0.95
)

// RefSegment is a candidate span of one speaker.
type RefSegment struct {
	Start, End float64
	SNR        float64 // dB, vocals vs background in the span
}

// PickRefSegments chooses the cleanest spans: at least minLen long, no overlap
// with any span in others, best SNR first, until target seconds (the last one
// is cut so the total stays at most maxLen). The result is in time order.
func PickRefSegments(own []RefSegment, others [][2]float64, minLen, target, maxLen float64) []RefSegment {
	var cand []RefSegment
	for _, s := range own {
		if s.End-s.Start < minLen || overlaps(s, others) {
			continue
		}
		cand = append(cand, s)
	}
	slices.SortStableFunc(cand, func(a, b RefSegment) int { return cmp.Compare(b.SNR, a.SNR) })
	var out []RefSegment
	total := 0.0
	for _, s := range cand {
		if total >= target {
			break
		}
		if rest := maxLen - total; s.End-s.Start > rest {
			s.End = s.Start + rest
		}
		out = append(out, s)
		total += s.End - s.Start
	}
	slices.SortFunc(out, func(a, b RefSegment) int { return cmp.Compare(a.Start, b.Start) })
	return out
}

func overlaps(s RefSegment, others [][2]float64) bool {
	for _, o := range others {
		if s.Start < o[1] && o[0] < s.End {
			return true
		}
	}
	return false
}

// JoinRef concatenates the spans of mono audio with short pauses and brings the
// result to a fixed loudness (RMS) without clipping.
func JoinRef(a *media.Audio, segs []RefSegment) *media.Audio {
	gap := make([]float32, int(refGapS*float64(a.Rate)))
	var out []float32
	for i, s := range segs {
		if i > 0 {
			out = append(out, gap...)
		}
		out = append(out, a.Slice(s.Start, s.End)...)
	}
	if rms := media.RMS(out); rms > 0 {
		gain := math.Pow(10, refRMSdB/20) / rms
		var peak float64
		for _, x := range out {
			peak = max(peak, math.Abs(float64(x)))
		}
		gain = min(gain, refPeak/peak)
		for i := range out {
			out[i] = float32(float64(out[i]) * gain)
		}
	}
	return &media.Audio{Rate: a.Rate, Channels: 1, Samples: out}
}

func snr(vocals, background []float32) float64 {
	v, b := media.RMS(vocals), media.RMS(background)
	if b < 1e-6 {
		b = 1e-6
	}
	if v < 1e-9 {
		return -120
	}
	return 20 * math.Log10(v/b)
}

// RefResult reports one character's reference in refs.json.
type RefResult struct {
	Character string  `json:"character"`
	Path      string  `json:"path"` // relative to series/<name>/
	Seconds   float64 `json:"seconds"`
	Segments  int     `json:"segments"`
	Kept      bool    `json:"kept"` // an existing reference was kept
}

// Refs: per character of the episode a 20–30 s reference from vocals.wav into
// series/<name>/refs/<character>.wav. An existing reference is kept so the
// voice stays the same across episodes; delete the file to build a new one.
type Refs struct {
	Store *store.Store
	Root  string
	Log   *slog.Logger
}

var reUnsafe = regexp.MustCompile(`[^\p{L}\p{N}_-]+`)

func refFile(character string) string {
	return filepath.Join("refs", reUnsafe.ReplaceAllString(character, "_")+".wav")
}

func (s Refs) Run(ctx context.Context, ep *Episode) (Outputs, error) {
	segs, err := s.Store.ListSegments(ctx, ep.ID)
	if err != nil {
		return nil, err
	}
	chars, err := s.Store.ListCharacters(ctx, ep.SeriesID)
	if err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for _, c := range chars {
		names[c.ID] = c.Name
	}
	vocals, err := media.ReadWAV(filepath.Join(ep.WorkDir, FileVocals))
	if err != nil {
		return nil, err
	}
	background, err := media.ReadWAV(filepath.Join(ep.WorkDir, FileBackground))
	if err != nil {
		return nil, err
	}
	vocals, background = vocals.Mono(), background.Mono()

	byChar := map[string][]RefSegment{}
	spans := map[string][][2]float64{}
	for _, sg := range segs {
		if sg.CharacterID == nil {
			continue
		}
		name := names[*sg.CharacterID]
		spans[name] = append(spans[name], [2]float64{sg.StartS, sg.EndS})
		byChar[name] = append(byChar[name], RefSegment{
			Start: sg.StartS, End: sg.EndS,
			SNR: snr(vocals.Slice(sg.StartS, sg.EndS), background.Slice(sg.StartS, sg.EndS)),
		})
	}

	seriesDir := SeriesDir(s.Root, ep.Series)
	castPath := filepath.Join(seriesDir, FileCast)
	c, err := cast.Load(castPath)
	if err != nil {
		return nil, err
	}
	var results []RefResult
	for _, name := range slices.Sorted(maps.Keys(byChar)) {
		var others [][2]float64
		for other, sp := range spans {
			if other != name {
				others = append(others, sp...)
			}
		}
		rel := refFile(name)
		res := RefResult{Character: name, Path: rel}
		if _, err := os.Stat(filepath.Join(seriesDir, rel)); err == nil {
			res.Kept = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		} else {
			picked := PickRefSegments(byChar[name], others, RefMinSegmentS, RefTargetS, RefMaxS)
			if len(picked) == 0 {
				s.Log.Warn("postava nemá žádný čistý úsek ≥ 3 s, reference zatím není", "character", name)
				continue
			}
			ref := JoinRef(vocals, picked)
			if err := media.WriteWAV(filepath.Join(seriesDir, rel), ref); err != nil {
				return nil, err
			}
			res.Seconds = float64(ref.Frames()) / float64(ref.Rate)
			res.Segments = len(picked)
			if res.Seconds < RefWarnS {
				s.Log.Warn("krátká reference, klon hlasu bude slabý", "character", name, "s", math.Round(res.Seconds*10)/10)
			}
		}
		for _, ch := range c.Characters {
			if ch.Name == name && ch.Ref == "" {
				ch.Ref = rel
				if err := s.Store.SetCharacterRef(ctx, db.SetCharacterRefParams{RefPath: rel, SeriesID: ep.SeriesID, Name: name}); err != nil {
					return nil, err
				}
			}
		}
		results = append(results, res)
	}
	if err := c.Save(castPath); err != nil {
		return nil, err
	}
	if err := media.WriteJSON(filepath.Join(ep.WorkDir, FileRefs), results); err != nil {
		return nil, fmt.Errorf("refs.json: %w", err)
	}
	return Outputs{"refs": FileRefs}, nil
}
