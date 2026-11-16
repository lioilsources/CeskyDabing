package cast

import (
	"path/filepath"
	"testing"
)

func TestIdentifyAcrossEpisodes(t *testing.T) {
	c := &Cast{}
	dad := []float32{1, 0, 0}
	kid := []float32{0, 1, 0}

	a := c.Identify("s01e01", []Speaker{{ID: "SPK_00", Embedding: dad, F0Mean: 110}, {ID: "SPK_01", Embedding: kid, F0Mean: 300}})
	if !a[0].New || !a[1].New || a[0].Character != "UNKNOWN_1" || a[1].Character != "UNKNOWN_2" {
		t.Fatalf("first episode: %+v", a)
	}
	c.Characters[0].Name = "Bandit" // the user renames in cast.yaml

	// Next episode: speakers come in another order, slightly different embeddings.
	a = c.Identify("s01e02", []Speaker{{ID: "SPK_00", Embedding: []float32{0.1, 1, 0}}, {ID: "SPK_01", Embedding: []float32{1, 0.1, 0}, F0Mean: 130}})
	if a[0].Character != "UNKNOWN_2" || a[1].Character != "Bandit" || a[0].New || a[1].New {
		t.Fatalf("second episode: %+v", a)
	}
	if got := c.Characters[0].F0Mean; got != 120 {
		t.Fatalf("running F0 mean %v", got)
	}

	// A stranger becomes UNKNOWN_3; rerunning an episode does not move centroids.
	before := Base64(c.Characters[0].vec)
	a = c.Identify("s01e02", []Speaker{{ID: "SPK_02", Embedding: []float32{0, 0, 1}}, {ID: "SPK_01", Embedding: []float32{1, 1, 0}}})
	if a[0].Character != "UNKNOWN_3" || !a[0].New {
		t.Fatalf("stranger: %+v", a)
	}
	if Base64(c.Characters[0].vec) != before {
		t.Fatal("rerun of an episode moved the centroid")
	}
}

func TestSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "series", "x", "cast.yaml")
	c := &Cast{}
	c.Identify("e1", []Speaker{{ID: "SPK_00", Embedding: []float32{3, 4}, F0Mean: 100, F0Std: 10}})
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ch := got.Characters[0]
	if ch.Name != "UNKNOWN_1" || ch.Strategy != "A" || len(ch.Vec()) != 2 || Cosine(ch.Vec(), []float32{3, 4}) < 0.9999 {
		t.Fatalf("%+v %v", ch, ch.Vec())
	}
	if missing, err := Load(filepath.Join(t.TempDir(), "none.yaml")); err != nil || len(missing.Characters) != 0 {
		t.Fatalf("missing file: %v %v", missing, err)
	}
}

func TestVectors(t *testing.T) {
	v := []float32{0.5, -2, 3}
	back, err := FromBlob(Blob(v))
	if err != nil || Cosine(back, v) < 0.99999 || back[1] != -2 {
		t.Fatalf("%v %v", back, err)
	}
	if Cosine([]float32{1}, []float32{1, 2}) != 0 {
		t.Fatal("different lengths must give 0")
	}
}
