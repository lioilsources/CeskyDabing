package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lioilsources/CeskyDabing/internal/media"
	"github.com/lioilsources/CeskyDabing/internal/store/db"
	"github.com/lioilsources/CeskyDabing/internal/translate"
)

const (
	FileFit = "fit.json"
	DirFit  = "fit"
)

// Fit loop rules (00-DABING_PLAN §5).
const (
	FitShortenRatio = 1.2  // longer than this × slot → ask the LLM to shorten
	FitMaxShorten   = 2    // at most this many shortenings per line
	FitMaxStretch   = 1.2  // rubberband speeds up at most this much (±20 %)
	FitOverflowS    = 1.0  // a line may run into the silence after it by this much
	fitGapS         = 0.05 // keep this far from the next line
)

// FitSlot is the time a line may take: its own span, extended into the
// silence that follows, up to FitOverflowS and never into the next line.
func FitSlot(start, end, nextStart float64) float64 {
	limit := end + FitOverflowS
	if nextStart > 0 {
		limit = min(limit, nextStart-fitGapS)
	}
	return max(end, limit) - start
}

// FitAction is what to do with one line.
type FitAction int

const (
	FitKeep FitAction = iota
	FitShorten
	FitStretch
)

// DecideFit picks the action for a line of duration d in slot, after iter
// shortenings, and the rubberband time factor (1 = untouched).
func DecideFit(d, slot float64, iter int) (FitAction, float64) {
	if slot <= 0 || d <= slot {
		return FitKeep, 1
	}
	if d > FitShortenRatio*slot && iter < FitMaxShorten {
		return FitShorten, 1
	}
	return FitStretch, max(slot/d, 1/FitMaxStretch)
}

// Stretcher writes in as 48 kHz mono 16-bit WAV to out, time-stretched by
// factor (1 = only converted).
type Stretcher interface {
	Stretch(ctx context.Context, in, out string, factor float64) error
}

// Rubberband converts with ffmpeg and stretches with the rubberband CLI.
type Rubberband struct {
	FFmpeg, Rubberband string
}

func (r Rubberband) Stretch(ctx context.Context, in, out string, factor float64) error {
	tmp := filepath.Join(filepath.Dir(out), "."+filepath.Base(out)+".tmp.wav")
	defer os.Remove(tmp)
	conv := exec.CommandContext(ctx, r.FFmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", in, "-ar", strconv.Itoa(media.SampleRate), "-ac", "1", "-c:a", "pcm_s16le", tmp)
	if b, err := conv.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(string(b)))
	}
	if factor > 0.999 {
		return os.Rename(tmp, out)
	}
	tmpOut := filepath.Join(filepath.Dir(out), "."+filepath.Base(out)+".rb.wav")
	defer os.Remove(tmpOut)
	rb := exec.CommandContext(ctx, r.Rubberband, "--fine", "--quiet", "--time", strconv.FormatFloat(factor, 'f', 4, 64), tmp, tmpOut)
	if b, err := rb.CombinedOutput(); err != nil {
		return fmt.Errorf("rubberband: %w: %s", err, strings.TrimSpace(string(b)))
	}
	return os.Rename(tmpOut, out)
}

// FitLine is one entry of fit.json.
type FitLine struct {
	Idx       int     `json:"idx"`
	Slot      float64 `json:"slot_s"`
	TTS       float64 `json:"tts_s"`
	Shortened int     `json:"shortened"`
	Stretch   float64 `json:"stretch"` // time factor, 1 = untouched
	Overflow  float64 `json:"overflow_s,omitempty"`
	Path      string  `json:"path"`
}

// FitSummary heads fit.json; StretchedOver10 feeds the quality metric (§9).
type FitSummary struct {
	Lines           int       `json:"lines"`
	Shortened       int       `json:"shortened"`
	StretchedOver10 int       `json:"stretched_over_10pct"`
	Overflowing     int       `json:"overflowing"`
	Items           []FitLine `json:"items"`
}

// Fit: TTS length vs. slot → shorten via the LLM (at most twice, re-synthesizing)
// → rubberband by at most 20 %. Output fit/NNNN.wav, 48 kHz mono, per line.
type Fit struct {
	Synth   Synthesize
	LLM     translate.LLM
	Stretch Stretcher
	Log     *slog.Logger
}

func FitFile(idx int) string { return filepath.Join(DirFit, fmt.Sprintf("%04d.wav", idx)) }

func (s Fit) Run(ctx context.Context, ep *Episode) (Outputs, error) {
	st := s.Synth.Store
	segs, err := st.ListSegmentsForTTS(ctx, ep.ID)
	if err != nil {
		return nil, err
	}
	all, err := st.ListSegmentsWithCharacter(ctx, ep.ID)
	if err != nil {
		return nil, err
	}
	en := map[int64]string{}
	for _, sg := range all {
		en[sg.Idx] = sg.TextEn
	}
	if err := os.MkdirAll(filepath.Join(ep.WorkDir, DirFit), 0o755); err != nil {
		return nil, err
	}
	var voices map[string]string
	var tr *translate.Translator
	sum := FitSummary{}
	for i, sg := range segs {
		if sg.TtsPath == "" {
			continue
		}
		next := 0.0
		if i+1 < len(segs) {
			next = segs[i+1].StartS
		}
		slot := FitSlot(sg.StartS, sg.EndS, next)
		text, rel := sg.TextCs, sg.TtsPath
		d, err := s.Synth.duration(ctx, filepath.Join(ep.WorkDir, rel))
		if err != nil {
			return nil, fmt.Errorf("segment %d: %w", sg.Idx, err)
		}
		line := FitLine{Idx: int(sg.Idx), Slot: slot, Path: FitFile(int(sg.Idx))}
		for {
			action, factor := DecideFit(d, slot, line.Shortened)
			if action != FitShorten {
				line.Stretch = factor
				break
			}
			if tr == nil { // lazily: most episodes need few shortenings
				if tr, err = NewTranslator(s.Synth.Root, ep.Series, loggedLLM{LLM: s.LLM, store: st, ep: ep, step: "fit"}); err != nil {
					return nil, err
				}
				if voices, err = s.Synth.voices(ctx, ep, segs); err != nil {
					return nil, err
				}
			}
			short, err := tr.Shorten(ctx, translate.Line{
				ID: int(sg.Idx), Character: orUnknown(sg.CharacterName), EN: en[sg.Idx], MaxChars: translate.MaxChars(0, slot),
			}, text)
			if err != nil {
				return nil, fmt.Errorf("segment %d: zkrácení: %w", sg.Idx, err)
			}
			voice := voices[sg.CharacterName]
			rel = TTSFile(int(sg.Idx), short, voice)
			if d, err = s.Synth.synthOne(ctx, ep.WorkDir, rel, short, voice, sg.Speed); err != nil {
				return nil, fmt.Errorf("segment %d: %w", sg.Idx, err)
			}
			text = short
			line.Shortened++
			if err := st.SetSegmentCS(ctx, db.SetSegmentCSParams{TextCs: text, EpisodeID: ep.ID, Idx: sg.Idx}); err != nil {
				return nil, err
			}
			if err := st.SetSegmentTTS(ctx, db.SetSegmentTTSParams{TtsPath: rel, TtsDuration: &d, EpisodeID: ep.ID, Idx: sg.Idx}); err != nil {
				return nil, err
			}
		}
		line.TTS = d
		if err := s.Stretch.Stretch(ctx, filepath.Join(ep.WorkDir, rel), filepath.Join(ep.WorkDir, line.Path), line.Stretch); err != nil {
			return nil, fmt.Errorf("segment %d: %w", sg.Idx, err)
		}
		if over := d*line.Stretch - slot; over > 0.01 {
			line.Overflow = over
			sum.Overflowing++
		}
		stretch, iter := line.Stretch, int64(line.Shortened)
		if err := st.SetSegmentFit(ctx, db.SetSegmentFitParams{Stretch: &stretch, FitIter: iter, EpisodeID: ep.ID, Idx: sg.Idx}); err != nil {
			return nil, err
		}
		sum.Lines++
		if line.Shortened > 0 {
			sum.Shortened++
		}
		if line.Stretch < 1/1.1 {
			sum.StretchedOver10++
		}
		sum.Items = append(sum.Items, line)
	}
	if sum.Overflowing > 0 {
		s.Log.Warn("repliky delší než slot i po zkrácení a zrychlení", "count", sum.Overflowing)
	}
	if err := media.WriteJSON(filepath.Join(ep.WorkDir, FileFit), sum); err != nil {
		return nil, err
	}
	return Outputs{"fit": FileFit}, nil
}
