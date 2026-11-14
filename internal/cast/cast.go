// Package cast keeps the characters of a series consistent across episodes
// (00-DABING_PLAN §4.2). series/<name>/cast.yaml is the source of truth; the DB
// is a cache. A diarized speaker matches a known character when the cosine of
// its embedding to the character centroid reaches Threshold; otherwise a new
// UNKNOWN_n character is created so a run never stops, and reported.
package cast

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Threshold of a known character (00-DABING_PLAN §4.2).
const Threshold = 0.75

const UnknownPrefix = "UNKNOWN_"

type Cast struct {
	Characters []*Character `yaml:"characters"`
}

type Character struct {
	Name string `yaml:"name"`
	// Strategy A = cross-lingual clone of the original, B = Czech catalog voice, C = hybrid.
	Strategy string  `yaml:"strategy"`
	Voice    string  `yaml:"voice,omitempty"` // catalog voice for B/C, e.g. cz_m_042
	Ref      string  `yaml:"ref,omitempty"`   // reference clip relative to series/<name>/
	Speed    float64 `yaml:"speed,omitempty"`
	Gender   string  `yaml:"gender,omitempty"`
	AgeGroup string  `yaml:"age_group,omitempty"`
	Notes    string  `yaml:"notes,omitempty"`

	F0Mean float64 `yaml:"f0_mean,omitempty"`
	F0Std  float64 `yaml:"f0_std,omitempty"`
	// Episodes already folded into the centroid; rerunning one does not count twice.
	Episodes []string `yaml:"episodes,omitempty"`
	// Centroid is the speaker embedding mean, little-endian float32 in base64.
	Centroid string `yaml:"centroid,omitempty"`

	vec []float32
}

// Vec returns the decoded centroid.
func (c *Character) Vec() []float32 { return c.vec }

func Load(path string) (*Cast, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Cast{}, nil
	} else if err != nil {
		return nil, err
	}
	var c Cast
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	for _, ch := range c.Characters {
		if ch.Name == "" || seen[ch.Name] {
			return nil, fmt.Errorf("%s: postava bez jména nebo duplicitní jméno %q", path, ch.Name)
		}
		seen[ch.Name] = true
		if ch.vec, err = FromBase64(ch.Centroid); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", path, ch.Name, err)
		}
	}
	return &c, nil
}

// Save writes cast.yaml atomically.
func (c *Cast) Save(path string) error {
	for _, ch := range c.Characters {
		ch.Centroid = Base64(ch.vec)
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	header := []byte("# Postavy seriálu. Jména, strategii (A/B/C), hlas a poznámky můžeš upravit;\n" +
		"# centroid a episodes spravuje dub. Nová postava se jmenuje UNKNOWN_n.\n")
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, append(header, b...), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Speaker is one diarized speaker of an episode.
type Speaker struct {
	ID        string
	Embedding []float32
	F0Mean    float64
	F0Std     float64
}

// Assignment of one speaker to a character.
type Assignment struct {
	Speaker   string  `json:"speaker"`
	Character string  `json:"character"`
	Score     float64 `json:"score"` // cosine to the centroid before the update; 0 for new
	New       bool    `json:"new"`
}

// Identify assigns every speaker of an episode to a character, creating
// UNKNOWN_n for unmatched ones, and folds the episode into the centroids (once
// per episode). Speakers without an embedding become new characters too.
func (c *Cast) Identify(episode string, speakers []Speaker) []Assignment {
	var out []Assignment
	for _, sp := range speakers {
		best, score := c.best(sp.Embedding)
		a := Assignment{Speaker: sp.ID, Score: score}
		if best == nil || score < Threshold {
			best = &Character{Name: c.nextUnknown(), Strategy: "A"}
			c.Characters = append(c.Characters, best)
			a.New, a.Score = true, 0
		}
		a.Character = best.Name
		out = append(out, a)
		if !slices.Contains(best.Episodes, episode) && len(sp.Embedding) > 0 {
			n := len(best.Episodes)
			best.vec = RunningMean(best.vec, n, sp.Embedding)
			if sp.F0Mean > 0 {
				best.F0Mean = (best.F0Mean*float64(n) + sp.F0Mean) / float64(n+1)
				best.F0Std = (best.F0Std*float64(n) + sp.F0Std) / float64(n+1)
			}
			best.Episodes = append(best.Episodes, episode)
		}
	}
	return out
}

func (c *Cast) best(emb []float32) (*Character, float64) {
	var best *Character
	score := -1.0
	for _, ch := range c.Characters {
		if s := Cosine(ch.vec, emb); s > score {
			best, score = ch, s
		}
	}
	return best, score
}

func (c *Cast) nextUnknown() string {
	n := 0
	for _, ch := range c.Characters {
		if rest, ok := strings.CutPrefix(ch.Name, UnknownPrefix); ok {
			if k, err := strconv.Atoi(rest); err == nil && k > n {
				n = k
			}
		}
	}
	return UnknownPrefix + strconv.Itoa(n+1)
}
