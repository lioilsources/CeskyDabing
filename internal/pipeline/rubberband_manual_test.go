//go:build manual

package pipeline

import (
	"context"
	"os"
	"testing"

	"github.com/lioilsources/CeskyDabing/internal/media"
)

// go test -tags manual -run TestRubberbandCLI ./internal/pipeline/ (needs ffmpeg + rubberband)
func TestRubberbandCLI(t *testing.T) {
	in := os.Getenv("RB_IN")
	out := t.TempDir() + "/out.wav"
	if err := (Rubberband{FFmpeg: "ffmpeg", Rubberband: "rubberband"}).Stretch(context.Background(), in, out, 0.8); err != nil {
		t.Fatal(err)
	}
	a, err := media.ReadWAV(out)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d Hz, %d ch, %.3f s", a.Rate, a.Channels, float64(a.Frames())/float64(a.Rate))
}
