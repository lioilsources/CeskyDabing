package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lioilsources/CeskyDabing/internal/media"
	"github.com/lioilsources/CeskyDabing/internal/storage"
	"github.com/lioilsources/CeskyDabing/internal/store"
	"github.com/lioilsources/CeskyDabing/internal/store/db"
	"github.com/lioilsources/CeskyDabing/internal/workers/separate"
)

// Pevná jména souborů v pracovním adresáři dílu.
const (
	FileAudio      = "audio.wav"
	FileInfo       = "info.json"
	FileVocals     = "vocals.wav"
	FileBackground = "background.wav"
	FileSeparate   = "separate.json"
)

// Extract: mkv → audio.wav (48 kHz) + info.json (ffprobe).
type Extract struct {
	FF media.FFmpeg
}

func (s Extract) Run(ctx context.Context, ep *Episode) (Outputs, error) {
	info, err := s.FF.Probe(ctx, ep.InputPath)
	if err != nil {
		return nil, err
	}
	if err := s.FF.ExtractAudio(ctx, ep.InputPath, info.AudioStream, filepath.Join(ep.WorkDir, FileAudio)); err != nil {
		return nil, err
	}
	if err := media.WriteJSON(filepath.Join(ep.WorkDir, FileInfo), info); err != nil {
		return nil, err
	}
	return Outputs{"audio": FileAudio, "info": FileInfo}, nil
}

// Separate: audio.wav → vocals.wav + background.wav přes worker separate.
type Separate struct {
	Worker     *separate.Worker
	NewStorage func() (storage.Storage, error) // nová instance per úloha (upload si drží stav)
	Store      *store.Store
	Model      string
	KeepStems  bool
}

func (s Separate) Run(ctx context.Context, ep *Episode) (Outputs, error) {
	st, err := s.NewStorage()
	if err != nil {
		return nil, err
	}
	in, err := st.Input(ctx, filepath.Join(ep.WorkDir, FileAudio))
	if err != nil {
		return nil, err
	}
	outDir, err := st.OutputDir(ctx, ep.WorkDir)
	if err != nil {
		return nil, err
	}
	req := separate.SeparateRequest{InputPath: in, OutputDir: outDir, KeepStems: &s.KeepStems}
	if s.Model != "" {
		req.Model = &s.Model
	}
	res, err := logJob(ctx, s.Store, ep, "separate", "separate", req, func() (*separate.SeparateResult, error) {
		return s.Worker.Separate(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	outs := Outputs{"vocals": FileVocals, "background": FileBackground, "meta": FileSeparate}
	fetch := map[string]string{res.VocalsPath: FileVocals, res.BackgroundPath: FileBackground}
	if res.Stems != nil {
		for name, p := range *res.Stems {
			rel := filepath.Join("stems", name+".wav")
			fetch[p] = rel
			outs["stem_"+name] = rel
		}
		if err := os.MkdirAll(filepath.Join(ep.WorkDir, "stems"), 0o755); err != nil {
			return nil, err
		}
	}
	for remote, rel := range fetch {
		if err := st.Fetch(ctx, remote, filepath.Join(ep.WorkDir, rel)); err != nil {
			return nil, err
		}
	}
	if err := media.WriteJSON(filepath.Join(ep.WorkDir, FileSeparate), res); err != nil {
		return nil, err
	}
	if err := setModelVersion(ctx, s.Store, ep, "separate", res.Model); err != nil {
		return nil, err
	}
	return outs, nil
}

// logJob zapíše volání workeru do tabulky jobs (trvání, chyby).
func logJob[T any](ctx context.Context, st *store.Store, ep *Episode, step, worker string, req any, call func() (T, error)) (T, error) {
	reqJSON, _ := json.Marshal(req)
	id, err := st.StartJob(ctx, db.StartJobParams{
		EpisodeID: &ep.ID, Step: step, Worker: worker, Request: string(reqJSON),
	})
	if err != nil {
		var zero T
		return zero, err
	}
	t0 := time.Now()
	res, callErr := call()
	p := db.FinishJobParams{ID: id, Status: "ok", Response: "{}", DurationMs: ptr(time.Since(t0).Milliseconds())}
	if callErr != nil {
		p.Status, p.Error = "error", callErr.Error()
	} else if b, err := json.Marshal(res); err == nil {
		p.Response = string(b)
	}
	if err := st.FinishJob(context.WithoutCancel(ctx), p); err != nil && callErr == nil {
		return res, err
	}
	return res, callErr
}

func setModelVersion(ctx context.Context, st *store.Store, ep *Episode, step, version string) error {
	versions := map[string]string{}
	_ = json.Unmarshal([]byte(ep.ModelVersions), &versions)
	versions[step] = version
	b, err := json.Marshal(versions)
	if err != nil {
		return err
	}
	ep.ModelVersions = string(b)
	if err := st.SetModelVersions(ctx, db.SetModelVersionsParams{ModelVersions: ep.ModelVersions, ID: ep.ID}); err != nil {
		return fmt.Errorf("model_versions: %w", err)
	}
	return nil
}

func ptr[T any](v T) *T { return &v }
