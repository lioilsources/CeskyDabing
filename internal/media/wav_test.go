package media

import (
	"math"
	"path/filepath"
	"testing"
)

func TestWAVRoundTrip(t *testing.T) {
	a := &Audio{Rate: 8000, Channels: 2}
	for i := range 800 {
		v := float32(0.5 * math.Sin(float64(i)/10))
		a.Samples = append(a.Samples, v, -v)
	}
	path := filepath.Join(t.TempDir(), "new", "dir", "x.wav") // directory does not exist yet
	if err := WriteWAV(path, a); err != nil {
		t.Fatal(err)
	}
	b, err := ReadWAV(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.Rate != 8000 || b.Channels != 2 || b.Frames() != 800 {
		t.Fatalf("%d Hz, %d ch, %d frames", b.Rate, b.Channels, b.Frames())
	}
	if d := math.Abs(float64(b.Samples[21] - a.Samples[21])); d > 1e-4 {
		t.Fatalf("sample differs by %v", d)
	}
	m := b.Mono()
	if m.Channels != 1 || math.Abs(float64(m.Samples[5])) > 1e-4 {
		t.Fatalf("mono of opposite channels must cancel: %v", m.Samples[5])
	}
	if got := len(b.Slice(0.01, 0.02)); got != 2*80 {
		t.Fatalf("slice %d samples", got)
	}
}
