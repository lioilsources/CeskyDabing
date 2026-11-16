package pipeline

import (
	"math"
	"testing"

	"github.com/lioilsources/CeskyDabing/internal/media"
)

func TestPickRefSegments(t *testing.T) {
	own := []RefSegment{
		{Start: 0, End: 2, SNR: 40},    // too short
		{Start: 10, End: 20, SNR: 30},  // overlaps another speaker
		{Start: 30, End: 40, SNR: 20},  // 10 s
		{Start: 50, End: 62, SNR: 25},  // 12 s, best clean SNR
		{Start: 70, End: 90, SNR: 10},  // cut to fill up to 30 s
		{Start: 100, End: 104, SNR: 5}, // not needed any more
	}
	others := [][2]float64{{19, 21}}
	got := PickRefSegments(own, others, 3, 25, 30)
	want := []RefSegment{{30, 40, 20}, {50, 62, 25}, {70, 78, 10}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestJoinRefLoudness(t *testing.T) {
	a := &media.Audio{Rate: 1000, Channels: 1, Samples: make([]float32, 5000)}
	for i := range a.Samples {
		a.Samples[i] = float32(0.01 * math.Sin(float64(i)))
	}
	ref := JoinRef(a, []RefSegment{{Start: 0, End: 1}, {Start: 2, End: 3}})
	if ref.Frames() != 2000+150 {
		t.Fatalf("frames %d", ref.Frames())
	}
	if db := 20 * math.Log10(media.RMS(ref.Samples)); math.Abs(db+20) > 0.1 {
		t.Fatalf("loudness %.1f dBFS", db)
	}
	for _, x := range ref.Samples {
		if math.Abs(float64(x)) > refPeak+1e-6 {
			t.Fatalf("peak %v over the limit", x)
		}
	}
}
