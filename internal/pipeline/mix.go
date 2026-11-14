package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"

	"github.com/lioilsources/CeskyDabing/internal/media"
	"github.com/lioilsources/CeskyDabing/internal/store"
)

const (
	FileCSWav = "cs.wav"
	FileMix   = "mix.json"
	mixPeak   = 0.99
)

// Placement is a mono clip placed on the timeline at Start seconds.
type Placement struct {
	Start   float64
	Samples []float32
}

// MixDub lays the dub clips over the background (any channel count, the dub
// goes to every channel) and, for spans that have no dub (songs, lines
// without a reference), puts the original vocals back so nothing goes silent.
// It mixes into background in place: an episode track is ~0.5 GB of floats.
func MixDub(background, vocals *media.Audio, dub []Placement, keepOriginal [][2]float64) *media.Audio {
	ch := background.Channels
	out := background
	frames := out.Frames()
	for _, p := range dub {
		at := int(math.Round(p.Start * float64(out.Rate)))
		for i, x := range p.Samples {
			f := at + i
			if f < 0 || f >= frames {
				continue
			}
			for c := range ch {
				out.Samples[f*ch+c] += x
			}
		}
	}
	if vocals != nil {
		for _, span := range keepOriginal {
			from := max(0, int(span[0]*float64(out.Rate)))
			to := min(frames, int(span[1]*float64(out.Rate)), vocals.Frames())
			for f := from; f < to; f++ {
				for c := range ch {
					out.Samples[f*ch+c] += vocals.Samples[f*vocals.Channels+min(c, vocals.Channels-1)]
				}
			}
		}
	}
	return out
}

// GainFor returns the linear gain that brings loudness from current to target
// LUFS without pushing the peak over mixPeak, and whether the peak limited it.
func GainFor(current, target, peak float64) (float64, bool) {
	if math.IsInf(current, -1) || math.IsInf(target, -1) {
		return 1, false
	}
	g := math.Pow(10, (target-current)/20)
	if peak > 0 && g*peak > mixPeak {
		return mixPeak / peak, true
	}
	return g, false
}

// MixReport is mix.json.
type MixReport struct {
	OriginalLUFS float64 `json:"original_lufs"`
	MixLUFS      float64 `json:"mix_lufs"`
	GainDB       float64 `json:"gain_db"`
	PeakLimited  bool    `json:"peak_limited"`
	Dubbed       int     `json:"dubbed_lines"`
	Original     int     `json:"original_lines"` // spans that kept the original voice
}

// Mix: fit/*.wav on the timeline + background.wav → cs.wav at the loudness
// of the original audio.
type Mix struct {
	Store *store.Store
	FF    media.FFmpeg
	Log   *slog.Logger
}

func (s Mix) Run(ctx context.Context, ep *Episode) (Outputs, error) {
	var fit FitSummary
	b, err := os.ReadFile(filepath.Join(ep.WorkDir, FileFit))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &fit); err != nil {
		return nil, fmt.Errorf("%s: %w", FileFit, err)
	}
	dubbed := map[int]string{}
	for _, it := range fit.Items {
		dubbed[it.Idx] = it.Path
	}
	segs, err := s.Store.ListSegments(ctx, ep.ID)
	if err != nil {
		return nil, err
	}
	background, err := media.ReadWAV(filepath.Join(ep.WorkDir, FileBackground))
	if err != nil {
		return nil, err
	}
	vocals, err := media.ReadWAV(filepath.Join(ep.WorkDir, FileVocals))
	if err != nil {
		return nil, err
	}
	vocals = vocals.Mono() // only short spans are needed; halve the memory
	var dub []Placement
	var keep [][2]float64
	for _, sg := range segs {
		rel, ok := dubbed[int(sg.Idx)]
		if !ok {
			keep = append(keep, [2]float64{sg.StartS, sg.EndS})
			continue
		}
		clip, err := media.ReadWAV(filepath.Join(ep.WorkDir, rel))
		if err != nil {
			return nil, err
		}
		if clip.Rate != background.Rate {
			return nil, fmt.Errorf("%s: %d Hz, pozadí má %d Hz", rel, clip.Rate, background.Rate)
		}
		dub = append(dub, Placement{Start: sg.StartS, Samples: clip.Mono().Samples})
	}
	mixed := MixDub(background, vocals, dub, keep)

	out := filepath.Join(ep.WorkDir, FileCSWav)
	if err := media.WriteWAV(out, mixed); err != nil {
		return nil, err
	}
	orig, err := s.FF.Loudness(ctx, filepath.Join(ep.WorkDir, FileAudio))
	if err != nil {
		return nil, err
	}
	cur, err := s.FF.Loudness(ctx, out)
	if err != nil {
		return nil, err
	}
	var peak float64
	for _, x := range mixed.Samples {
		peak = max(peak, math.Abs(float64(x)))
	}
	gain, limited := GainFor(cur, orig, peak)
	for i := range mixed.Samples {
		mixed.Samples[i] *= float32(gain)
	}
	if err := media.WriteWAV(out, mixed); err != nil {
		return nil, err
	}
	if limited {
		s.Log.Warn("hlasitost dabingu omezená špičkou, je tišší než originál", "gain_db", math.Round(20*math.Log10(gain)*10)/10)
	}
	rep := MixReport{OriginalLUFS: orig, MixLUFS: cur, GainDB: 20 * math.Log10(gain), PeakLimited: limited,
		Dubbed: len(dub), Original: len(keep)}
	if err := media.WriteJSON(filepath.Join(ep.WorkDir, FileMix), rep); err != nil {
		return nil, err
	}
	return Outputs{"cs": FileCSWav, "mix": FileMix}, nil
}
