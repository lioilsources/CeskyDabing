// Package media obaluje ffmpeg/ffprobe z Homebrew.
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// SampleRate pracovního audia celé pipeline.
const SampleRate = 48000

type FFmpeg struct {
	FFmpeg  string
	FFprobe string
}

// Info je výstup ffprobe zúžený na to, co pipeline potřebuje; Raw drží celé JSON.
type Info struct {
	Duration    float64         `json:"duration_s"`
	AudioStream int             `json:"audio_stream"` // index v rámci audio stop (0:a:N)
	AudioLang   string          `json:"audio_lang"`
	Streams     []Stream        `json:"streams"`
	Raw         json.RawMessage `json:"ffprobe"`
}

type Stream struct {
	Index       int               `json:"index"`
	CodecType   string            `json:"codec_type"`
	CodecName   string            `json:"codec_name"`
	Channels    int               `json:"channels,omitempty"`
	SampleRate  string            `json:"sample_rate,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	Disposition map[string]int    `json:"disposition,omitempty"`
}

func (f FFmpeg) Probe(ctx context.Context, input string) (*Info, error) {
	out, err := run(ctx, f.FFprobe, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", input)
	if err != nil {
		return nil, err
	}
	var p struct {
		Streams []Stream `json:"streams"`
		Format  struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, fmt.Errorf("ffprobe json: %w", err)
	}
	info := &Info{Streams: p.Streams, Raw: out, AudioStream: -1}
	info.Duration, _ = strconv.ParseFloat(p.Format.Duration, 64)
	info.AudioStream, info.AudioLang = pickAudio(p.Streams)
	if info.AudioStream < 0 {
		return nil, fmt.Errorf("%s: žádná audio stopa", input)
	}
	return info, nil
}

// pickAudio vybere zdrojovou stopu: první anglická, jinak výchozí, jinak první.
// Vrací index mezi audio stopami (pro -map 0:a:N).
func pickAudio(streams []Stream) (int, string) {
	eng, def, first := -1, -1, -1
	var langs []string
	n := 0
	for _, s := range streams {
		if s.CodecType != "audio" {
			continue
		}
		lang := s.Tags["language"]
		langs = append(langs, lang)
		if first < 0 {
			first = n
		}
		if eng < 0 && (lang == "eng" || lang == "en") {
			eng = n
		}
		if def < 0 && s.Disposition["default"] == 1 {
			def = n
		}
		n++
	}
	for _, i := range []int{eng, def, first} {
		if i >= 0 {
			return i, langs[i]
		}
	}
	return -1, ""
}

// ExtractAudio vytáhne zvolenou stopu jako stereo PCM WAV 48 kHz.
// Zapisuje přes dočasný soubor, takže nedokončený výstup nikdy nevypadá hotově.
func (f FFmpeg) ExtractAudio(ctx context.Context, input string, audioStream int, out string) error {
	tmp := tmpName(out)
	defer os.Remove(tmp)
	_, err := run(ctx, f.FFmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", input,
		"-map", fmt.Sprintf("0:a:%d", audioStream),
		"-ac", "2", "-ar", strconv.Itoa(SampleRate), "-c:a", "pcm_s16le",
		"-f", "wav", tmp)
	if err != nil {
		return err
	}
	return os.Rename(tmp, out)
}

// Duration vrátí délku souboru v sekundách.
func (f FFmpeg) Duration(ctx context.Context, path string) (float64, error) {
	out, err := run(ctx, f.FFprobe, "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}

// WriteJSON zapíše v atomicky (dočasný soubor + rename).
func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := tmpName(path)
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func tmpName(path string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
}

func run(ctx context.Context, bin string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", filepath.Base(bin), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
