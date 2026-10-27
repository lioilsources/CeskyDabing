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
