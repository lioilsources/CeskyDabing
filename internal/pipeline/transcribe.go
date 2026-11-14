package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lioilsources/CeskyDabing/internal/media"
	"github.com/lioilsources/CeskyDabing/internal/storage"
	"github.com/lioilsources/CeskyDabing/internal/store"
	"github.com/lioilsources/CeskyDabing/internal/store/db"
	"github.com/lioilsources/CeskyDabing/internal/workers/asr"
)

const (
	FileSegments   = "segments.json"
	FileTranscribe = "transcribe.json"
)

// Merge thresholds for short replies (00-DABING_PLAN §10: min segment length).
const (
	MinSegmentS = 0.4 // shorter segments are merged into a neighbour
	MaxMergeGap = 0.5 // only across a pause at most this long, same speaker
)

// Transcribe: vocals.wav → segments.json (words, speakers, embeddings) via the
// asr worker, then the segments go to the segments table.
type Transcribe struct {
	Worker     *asr.Worker
	NewStorage func() (storage.Storage, error)
	Store      *store.Store
	Language   string
}

func (s Transcribe) Run(ctx context.Context, ep *Episode) (Outputs, error) {
	st, err := s.NewStorage()
	if err != nil {
		return nil, err
	}
	in, err := st.Input(ctx, filepath.Join(ep.WorkDir, FileVocals))
	if err != nil {
		return nil, err
	}
	outDir, err := st.OutputDir(ctx, ep.WorkDir)
	if err != nil {
		return nil, err
	}
	req := asr.TranscribeRequest{AudioPath: in, OutputDir: outDir}
	if s.Language != "" {
		req.Language = &s.Language
	}
	res, err := logJob(ctx, s.Store, ep, "transcribe", "asr", req, func() (*asr.TranscribeResult, error) {
		return s.Worker.Transcribe(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	local := filepath.Join(ep.WorkDir, FileSegments)
	if err := st.Fetch(ctx, res.SegmentsPath, local); err != nil {
		return nil, err
	}
	tr, err := ReadTranscript(local)
	if err != nil {
		return nil, err
	}
	segs := MergeShort(tr.Segments, MinSegmentS, MaxMergeGap)
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.DeleteSegments(ctx, ep.ID); err != nil {
			return err
		}
		for i, sg := range segs {
			if err := q.InsertSegment(ctx, db.InsertSegmentParams{
				EpisodeID: ep.ID, Idx: int64(i), StartS: sg.Start, EndS: sg.End,
				Speaker: sg.Speaker, TextEn: sg.Text,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("segments: %w", err)
	}
	if err := media.WriteJSON(filepath.Join(ep.WorkDir, FileTranscribe), res); err != nil {
		return nil, err
	}
	if err := setModelVersion(ctx, s.Store, ep, "transcribe", res.Model); err != nil {
		return nil, err
	}
	return Outputs{"segments": FileSegments, "meta": FileTranscribe}, nil
}

func ReadTranscript(path string) (*asr.Transcript, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tr asr.Transcript
	if err := json.Unmarshal(b, &tr); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return &tr, nil
}

// MergeShort folds segments shorter than minLen into the previous (or else the
// next) segment of the same speaker when the pause between them is at most
// maxGap. Short segments that have no such neighbour are kept: speech is never
// dropped. Empty texts are removed.
func MergeShort(in []asr.Segment, minLen, maxGap float64) []asr.Segment {
	var out []asr.Segment
	for _, s := range in {
		s.Text = strings.TrimSpace(s.Text)
		if s.Text == "" {
			continue
		}
		if n := len(out); n > 0 {
			prev := &out[n-1]
			short := s.End-s.Start < minLen || prev.End-prev.Start < minLen
			if short && prev.Speaker == s.Speaker && s.Start-prev.End <= maxGap {
				prev.End = max(prev.End, s.End)
				prev.Text += " " + s.Text
				if s.Words != nil {
					words := slices.Concat(derefWords(prev.Words), *s.Words) // never write into the input
					prev.Words = &words
				}
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

func derefWords(w *[]asr.Word) []asr.Word {
	if w == nil {
		return nil
	}
	return *w
}
