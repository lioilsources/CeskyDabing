package media

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

// Audio is PCM audio as float32 samples in [-1, 1], interleaved by channel.
type Audio struct {
	Rate     int
	Channels int
	Samples  []float32
}

// Frames is the number of sample frames (samples per channel).
func (a *Audio) Frames() int { return len(a.Samples) / max(a.Channels, 1) }

// Mono averages the channels.
func (a *Audio) Mono() *Audio {
	if a.Channels == 1 {
		return a
	}
	n := a.Frames()
	out := make([]float32, n)
	for i := range n {
		var s float32
		for c := range a.Channels {
			s += a.Samples[i*a.Channels+c]
		}
		out[i] = s / float32(a.Channels)
	}
	return &Audio{Rate: a.Rate, Channels: 1, Samples: out}
}

// Slice returns frames [from, to) in seconds, clamped to the audio.
func (a *Audio) Slice(from, to float64) []float32 {
	i := min(max(int(from*float64(a.Rate)), 0), a.Frames())
	j := min(max(int(to*float64(a.Rate)), i), a.Frames())
	return a.Samples[i*a.Channels : j*a.Channels]
}

// RMS of samples.
func RMS(s []float32) float64 {
	if len(s) == 0 {
		return 0
	}
	var sum float64
	for _, x := range s {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum / float64(len(s)))
}

// ReadWAV reads 16-bit PCM WAV (what ffmpeg and the workers write).
func ReadWAV(path string) (*Audio, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return nil, fmt.Errorf("%s: není WAV", path)
	}
	var a Audio
	var bits int
	for {
		var ch [8]byte
		if _, err := io.ReadFull(r, ch[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, fmt.Errorf("%s: chybí data", path)
			}
			return nil, err
		}
		id, size := string(ch[0:4]), int64(binary.LittleEndian.Uint32(ch[4:8]))
		switch id {
		case "fmt ":
			b := make([]byte, size)
			if _, err := io.ReadFull(r, b); err != nil {
				return nil, err
			}
			format := binary.LittleEndian.Uint16(b[0:2])
			a.Channels = int(binary.LittleEndian.Uint16(b[2:4]))
			a.Rate = int(binary.LittleEndian.Uint32(b[4:8]))
			bits = int(binary.LittleEndian.Uint16(b[14:16]))
			if (format != 1 && format != 0xFFFE) || bits != 16 {
				return nil, fmt.Errorf("%s: podporovaný je jen 16bit PCM (format %d, %d bitů)", path, format, bits)
			}
		case "data":
			if a.Channels == 0 {
				return nil, fmt.Errorf("%s: data před fmt", path)
			}
			if size == 0xFFFFFFFF || size == 0 { // streamed WAV: read to the end
				size = math.MaxInt64
			}
			raw, err := io.ReadAll(io.LimitReader(r, size))
			if err != nil {
				return nil, err
			}
			a.Samples = make([]float32, len(raw)/2)
			for i := range a.Samples {
				a.Samples[i] = float32(int16(binary.LittleEndian.Uint16(raw[2*i:]))) / 32768
			}
			return &a, nil
		default:
			if _, err := r.Discard(int(size + size%2)); err != nil {
				return nil, err
			}
		}
		if id == "fmt " && size%2 == 1 {
			r.Discard(1)
		}
	}
}

// WriteWAV writes 16-bit PCM atomically (temp file + rename), creating the directory.
func WriteWAV(path string, a *Audio) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := tmpName(path)
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	w := bufio.NewWriter(f)
	data := uint32(2 * len(a.Samples))
	hdr := make([]byte, 44)
	copy(hdr[0:], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:], 36+data)
	copy(hdr[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(hdr[16:], 16)
	binary.LittleEndian.PutUint16(hdr[20:], 1)
	binary.LittleEndian.PutUint16(hdr[22:], uint16(a.Channels))
	binary.LittleEndian.PutUint32(hdr[24:], uint32(a.Rate))
	binary.LittleEndian.PutUint32(hdr[28:], uint32(a.Rate*a.Channels*2))
	binary.LittleEndian.PutUint16(hdr[32:], uint16(a.Channels*2))
	binary.LittleEndian.PutUint16(hdr[34:], 16)
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], data)
	w.Write(hdr)
	buf := make([]byte, 2)
	for _, s := range a.Samples {
		v := math.Round(float64(max(min(s, 1), -1)) * 32767)
		binary.LittleEndian.PutUint16(buf, uint16(int16(v)))
		w.Write(buf)
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
