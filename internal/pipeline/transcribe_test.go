package pipeline

import (
	"testing"

	"github.com/lioilsources/CeskyDabing/internal/workers/asr"
)

func seg(start, end float64, spk, text string) asr.Segment {
	return asr.Segment{Start: start, End: end, Speaker: spk, Text: text}
}

func TestMergeShort(t *testing.T) {
	in := []asr.Segment{
		seg(0.0, 2.0, "SPK_00", "Hello there."),
		seg(2.1, 2.3, "SPK_00", "Hi!"),    // short, same speaker, small gap → merged back
		seg(2.4, 2.6, "SPK_01", "Yes."),   // short but other speaker → kept
		seg(5.0, 5.2, "SPK_01", "No."),    // short, gap too big → kept
		seg(5.3, 7.0, "SPK_01", "Maybe."), // previous one is short → merged into it
		seg(7.5, 8.0, "SPK_01", "   "),    // empty → dropped
	}
	got := MergeShort(in, 0.4, 0.5)
	want := []asr.Segment{
		seg(0.0, 2.3, "SPK_00", "Hello there. Hi!"),
		seg(2.4, 2.6, "SPK_01", "Yes."),
		seg(5.0, 7.0, "SPK_01", "No. Maybe."),
	}
	if len(got) != len(want) {
		t.Fatalf("got %d segments: %+v", len(got), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Start != w.Start || g.End != w.End || g.Speaker != w.Speaker || g.Text != w.Text {
			t.Errorf("%d: got %+v, want %+v", i, g, w)
		}
	}
}

func TestMergeShortKeepsWords(t *testing.T) {
	w := func(s string) *[]asr.Word { return &[]asr.Word{{Word: s}} }
	a, b := seg(0, 2, "SPK_00", "a"), seg(2.1, 2.2, "SPK_00", "b")
	a.Words, b.Words = w("a"), w("b")
	got := MergeShort([]asr.Segment{a, b}, 0.4, 0.5)
	if len(got) != 1 || len(*got[0].Words) != 2 {
		t.Fatalf("%+v", got)
	}
}
