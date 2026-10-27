package storage

import (
	"context"
	"testing"
)

func TestSharedRemap(t *testing.T) {
	s := Shared{LocalRoot: "/Volumes/dabing", RemoteRoot: "/mnt/dabing"}
	got, err := s.Input(context.Background(), "/Volumes/dabing/work/x/e1/audio.wav")
	if err != nil || got != "/mnt/dabing/work/x/e1/audio.wav" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := s.Input(context.Background(), "/Volumes/dabing-other/a.wav"); err == nil {
		t.Fatal("cesta mimo kořen musí selhat")
	}
	if _, err := s.Input(context.Background(), "/Volumes/dabing/../etc/passwd"); err == nil {
		t.Fatal("únik z kořene musí selhat")
	}
}
