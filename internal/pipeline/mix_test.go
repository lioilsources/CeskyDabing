package pipeline

import (
	"math"
	"strings"
	"testing"

	"github.com/lioilsources/CeskyDabing/internal/media"
	"github.com/lioilsources/CeskyDabing/internal/store/db"
)

func TestMixDub(t *testing.T) {
	bg := &media.Audio{Rate: 10, Channels: 2, Samples: make([]float32, 2*20)} // 2 s of silence
	for i := range bg.Samples {
		bg.Samples[i] = 0.1
	}
	vocals := &media.Audio{Rate: 10, Channels: 1, Samples: make([]float32, 20)}
	for i := range vocals.Samples {
		vocals.Samples[i] = 0.5
	}
	out := MixDub(bg, vocals, []Placement{{Start: 0.2, Samples: []float32{1, 1}}}, [][2]float64{{1.5, 9}})
	at := func(frame, ch int) float32 { return out.Samples[frame*2+ch] }
	if at(2, 0) != 1.1 || at(2, 1) != 1.1 || at(3, 1) != 1.1 || at(4, 0) != 0.1 {
		t.Fatalf("dub not placed on both channels: %v", out.Samples[:12])
	}
	if at(15, 0) != 0.6 || at(19, 1) != 0.6 || at(14, 0) != 0.1 {
		t.Fatalf("original vocals not kept in the span: %v", out.Samples[28:40])
	}
}

func TestGainFor(t *testing.T) {
	if g, lim := GainFor(-26, -20, 0.1); lim || math.Abs(20*math.Log10(g)-6) > 1e-9 {
		t.Fatalf("%v %v", g, lim)
	}
	if g, lim := GainFor(-26, -20, 0.9); !lim || math.Abs(g*0.9-mixPeak) > 1e-9 {
		t.Fatalf("peak limit: %v %v", g, lim)
	}
	if g, _ := GainFor(math.Inf(-1), -20, 0); g != 1 {
		t.Fatal("silence must not be amplified")
	}
}

func TestSRT(t *testing.T) {
	got := SRT([]db.Segment{
		{StartS: 1.5, EndS: 3.25, TextCs: "Ahoj!"},
		{StartS: 4, EndS: 5},
		{StartS: 3725.0049, EndS: 3726, TextCs: " Jdeme. "},
	})
	want := "1\n00:00:01,500 --> 00:00:03,250\nAhoj!\n\n2\n01:02:05,005 --> 01:02:06,000\nJdeme.\n\n"
	if got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func TestMuxArgs(t *testing.T) {
	a := strings.Join(MuxArgs("in.mkv", "cs.wav", "cs.srt", "out.mkv", 1, "eng", 2), " ")
	for _, want := range []string{"-map 0:a:1", "-c:v copy", "-c:s:2 srt", "-metadata:s:a:0 language=ces", "-disposition:a:0 default", "-metadata:s:a:1 language=eng", "-metadata:s:s:2 language=ces"} {
		if !strings.Contains(a, want) {
			t.Errorf("missing %q in %s", want, a)
		}
	}
}
