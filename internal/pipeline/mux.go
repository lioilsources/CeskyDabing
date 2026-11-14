package pipeline

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lioilsources/CeskyDabing/internal/media"
	"github.com/lioilsources/CeskyDabing/internal/store"
	"github.com/lioilsources/CeskyDabing/internal/store/db"
)

const (
	FileSRT = "cs.srt"
	FileMux = "mux.json"
)

// SRTTime formats seconds as 00:01:02,345.
func SRTTime(s float64) string {
	ms := int64(math.Round(max(s, 0) * 1000))
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// SRT renders the Czech lines as subtitles (empty translations are skipped).
func SRT(segs []db.Segment) string {
	var b strings.Builder
	n := 0
	for _, sg := range segs {
		text := strings.TrimSpace(sg.TextCs)
		if text == "" {
			continue
		}
		n++
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", n, SRTTime(sg.StartS), SRTTime(sg.EndS), text)
	}
	return b.String()
}

// MuxArgs is the ffmpeg command of 00-DABING_PLAN §7: video copied, the Czech
// track first and default, the source track (0:a:origStream) second, existing
// subtitles kept and cs.srt added as a Czech subtitle track.
func MuxArgs(input, csWav, srt, out string, origStream int, origLang string, subStreams int) []string {
	if origLang == "" {
		origLang = "eng"
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", input, "-i", csWav, "-i", srt,
		"-map", "0:v", "-map", "1:a", "-map", fmt.Sprintf("0:a:%d", origStream), "-map", "0:s?", "-map", "2:s",
		"-c:v", "copy", "-c:s", "copy", "-c:s:" + fmt.Sprint(subStreams), "srt",
		"-c:a", "aac", "-b:a", "192k", "-c:a:1", "copy",
		"-metadata:s:a:0", "language=ces", "-metadata:s:a:0", "title=Český AI dabing", "-disposition:a:0", "default",
		"-metadata:s:a:1", "language=" + origLang, "-disposition:a:1", "0",
		"-metadata:s:s:" + fmt.Sprint(subStreams), "language=ces",
		"-metadata:s:s:" + fmt.Sprint(subStreams), "title=Čeština (AI)",
		"-f", "matroska", out,
	}
	return args
}

// Mux: original video + cs.wav + cs.srt → done/<series>/<name>.cs.mkv.
type Mux struct {
	Store *store.Store
	FF    media.FFmpeg
	Root  string
}

func (s Mux) Run(ctx context.Context, ep *Episode) (Outputs, error) {
	segs, err := s.Store.ListSegments(ctx, ep.ID)
	if err != nil {
		return nil, err
	}
	srt := filepath.Join(ep.WorkDir, FileSRT)
	tmpSRT := filepath.Join(ep.WorkDir, "."+FileSRT+".tmp")
	if err := os.WriteFile(tmpSRT, []byte(SRT(segs)), 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpSRT, srt); err != nil {
		return nil, err
	}
	info, err := s.FF.Probe(ctx, ep.InputPath)
	if err != nil {
		return nil, err
	}
	subs := 0
	for _, st := range info.Streams {
		if st.CodecType == "subtitle" {
			subs++
		}
	}
	base := strings.TrimSuffix(filepath.Base(ep.InputPath), filepath.Ext(ep.InputPath))
	out := filepath.Join(s.Root, "done", ep.Series, base+".cs.mkv")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return nil, err
	}
	tmp := filepath.Join(filepath.Dir(out), "."+filepath.Base(out)+".tmp")
	defer os.Remove(tmp)
	cmd := exec.CommandContext(ctx, s.FF.FFmpeg, MuxArgs(ep.InputPath, filepath.Join(ep.WorkDir, FileCSWav), srt, tmp, info.AudioStream, langCode(info.AudioLang), subs)...)
	if b, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg mux: %w: %s", err, strings.TrimSpace(string(b)))
	}
	if err := os.Rename(tmp, out); err != nil {
		return nil, err
	}
	if err := media.WriteJSON(filepath.Join(ep.WorkDir, FileMux), map[string]string{"output": out}); err != nil {
		return nil, err
	}
	return Outputs{"srt": FileSRT, "mux": FileMux}, nil
}

// langCode keeps an ISO 639-2 tag from the source; "en" becomes "eng".
func langCode(l string) string {
	switch l {
	case "", "und":
		return "eng"
	case "en":
		return "eng"
	}
	return l
}
