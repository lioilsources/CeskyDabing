package media

import "testing"

func TestPickAudio(t *testing.T) {
	a := func(lang string, def int) Stream {
		return Stream{CodecType: "audio", Tags: map[string]string{"language": lang}, Disposition: map[string]int{"default": def}}
	}
	v := Stream{CodecType: "video"}
	cases := []struct {
		streams []Stream
		want    int
	}{
		{[]Stream{v, a("cze", 1), a("eng", 0)}, 1},
		{[]Stream{v, a("fra", 0), a("ger", 1)}, 1},
		{[]Stream{v, a("", 0)}, 0},
		{[]Stream{v}, -1},
	}
	for i, c := range cases {
		if got, _ := pickAudio(c.streams); got != c.want {
			t.Errorf("případ %d: %d, chci %d", i, got, c.want)
		}
	}
}

func TestParseLoudness(t *testing.T) {
	out := `[Parsed_ebur128_0 @ 0x1] Summary:

  Integrated loudness:
    I:         -23.4 LUFS
    Threshold: -33.6 LUFS

  Loudness range:
    LRA:         5.1 LU`
	if v, err := ParseLoudness(out); err != nil || v != -23.4 {
		t.Fatalf("%v %v", v, err)
	}
	if _, err := ParseLoudness("nothing"); err == nil {
		t.Fatal("missing summary must fail")
	}
}
