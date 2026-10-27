package main

import "testing"

func TestEpisodeSlug(t *testing.T) {
	for in, want := range map[string]string{
		"/x/Bluey.S02E07.1080p.mkv":        "s02e07",
		"/x/bluey s1e3.mkv":                "s01e03",
		"/x/Prasátko Pepa - Bláto.mkv":     "prasatko-pepa-blato",
		"/x/Paw.Patrol.S10E101.WEB-DL.mkv": "s10e101",
	} {
		if got := episodeSlug(in); got != want {
			t.Errorf("%s: %s, chci %s", in, got, want)
		}
	}
}
