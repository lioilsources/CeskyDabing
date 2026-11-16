package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/lioilsources/CeskyDabing/internal/media"
	"github.com/lioilsources/CeskyDabing/internal/store"
	"github.com/lioilsources/CeskyDabing/internal/store/db"
	"github.com/lioilsources/CeskyDabing/internal/tts"
)

const (
	FileSynthesize = "synthesize.json"
	DirTTS         = "tts"
)

// Synthesize: Czech text + the character's reference → one WAV per segment.
// Segments are synthesized by a small worker pool; a segment whose file for
// the same text and voice already exists is kept, so an interrupted run
// continues where it stopped.
type Synthesize struct {
	Store    *store.Store
	Root     string
	TTS      tts.TTS
	FF       media.FFmpeg
	Parallel int
	Log      *slog.Logger
}

// SynthLine is one entry of synthesize.json.
type SynthLine struct {
	Idx       int     `json:"idx"`
	Character string  `json:"character"`
	Path      string  `json:"path,omitempty"`
	Duration  float64 `json:"duration_s,omitempty"`
	Slot      float64 `json:"slot_s"`
	Skipped   string  `json:"skipped,omitempty"`
}

// TTSFile names a segment's WAV after its text and voice, so a changed
// translation or reference gets a new file.
func TTSFile(idx int, text, voiceID string) string {
	sum := sha256.Sum256([]byte(voiceID + "\x00" + text))
	return filepath.Join(DirTTS, fmt.Sprintf("%04d-%s.wav", idx, hex.EncodeToString(sum[:4])))
}

func (s Synthesize) Run(ctx context.Context, ep *Episode) (Outputs, error) {
	segs, err := s.Store.ListSegmentsForTTS(ctx, ep.ID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(ep.WorkDir, DirTTS), 0o755); err != nil {
		return nil, err
	}
	voices, err := s.voices(ctx, ep, segs)
	if err != nil {
		return nil, err
	}

	lines := make([]SynthLine, len(segs))
	var mu sync.Mutex // the store has one connection; serialize writes
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(s.Parallel, 1))
	for i, sg := range segs {
		line := SynthLine{Idx: int(sg.Idx), Character: sg.CharacterName, Slot: sg.EndS - sg.StartS}
		voice, hasVoice := voices[sg.CharacterName]
		switch {
		case sg.IsSong != 0:
			line.Skipped = "song"
		case sg.TextCs == "":
			line.Skipped = "no translation"
		case !hasVoice:
			line.Skipped = "no reference"
		}
		if line.Skipped != "" {
			lines[i] = line
			continue
		}
		rel := TTSFile(line.Idx, sg.TextCs, voice)
		line.Path = rel
		abs := filepath.Join(ep.WorkDir, rel)
		if sg.TtsPath == rel {
			if _, err := os.Stat(abs); err == nil {
				d, err := s.duration(ctx, abs)
				if err != nil {
					return nil, err
				}
				line.Duration = d
				lines[i] = line
				continue
			}
		}
		g.Go(func() error {
			req := tts.Request{Text: sg.TextCs, Language: "cs", VoiceID: voice, Speed: sg.Speed}
			wav, err := s.TTS.Synthesize(gctx, req)
			if err != nil {
				return fmt.Errorf("segment %d: %w", line.Idx, err)
			}
			tmp := filepath.Join(filepath.Dir(abs), "."+filepath.Base(abs)+".tmp")
			if err := os.WriteFile(tmp, wav, 0o644); err != nil {
				return err
			}
			if err := os.Rename(tmp, abs); err != nil {
				return err
			}
			d, err := s.duration(gctx, abs)
			if err != nil {
				return fmt.Errorf("segment %d: %w", line.Idx, err)
			}
			line.Duration = d
			mu.Lock()
			defer mu.Unlock()
			lines[i] = line
			return s.Store.SetSegmentTTS(gctx, db.SetSegmentTTSParams{TtsPath: rel, TtsDuration: &d, EpisodeID: ep.ID, Idx: sg.Idx})
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	skipped := map[string]int{}
	for _, l := range lines {
		if l.Skipped != "" {
			skipped[l.Skipped]++
		}
	}
	if n := skipped["no reference"]; n > 0 {
		s.Log.Warn("repliky bez reference hlasu zůstanou bez dabingu", "count", n)
	}
	if err := media.WriteJSON(filepath.Join(ep.WorkDir, FileSynthesize), lines); err != nil {
		return nil, err
	}
	return Outputs{"synthesize": FileSynthesize}, nil
}

// duration reads 16-bit PCM WAV in Go and asks ffprobe only for other formats.
func (s Synthesize) duration(ctx context.Context, path string) (float64, error) {
	if a, err := media.ReadWAV(path); err == nil {
		return float64(a.Frames()) / float64(a.Rate), nil
	}
	return s.FF.Duration(ctx, path)
}

// voices stores each character's reference in the TTS service once.
func (s Synthesize) voices(ctx context.Context, ep *Episode, segs []db.ListSegmentsForTTSRow) (map[string]string, error) {
	out := map[string]string{}
	for _, sg := range segs {
		if _, done := out[sg.CharacterName]; done || sg.RefPath == "" {
			continue
		}
		id, err := s.TTS.EnsureVoice(ctx, tts.Voice{
			Series: ep.Series, Character: sg.CharacterName, Language: "cs",
			RefPath: filepath.Join(SeriesDir(s.Root, ep.Series), sg.RefPath),
		})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", sg.CharacterName, err)
		}
		out[sg.CharacterName] = id
	}
	return out, nil
}
