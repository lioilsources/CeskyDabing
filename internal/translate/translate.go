package translate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// CharsPerSecond of spoken Czech used for the length limit (00-DABING_PLAN §5).
const CharsPerSecond = 14.0

// Batching: a scene ends at a pause longer than SceneGapS or after MaxBatch lines.
const (
	SceneGapS    = 4.0
	MaxBatch     = 25
	ContextLines = 4
)

// Line is one segment to translate.
type Line struct {
	ID        int     `json:"id"`
	Start     float64 `json:"-"`
	End       float64 `json:"-"`
	Character string  `json:"postava"`
	EN        string  `json:"en"`
	MaxChars  int     `json:"max_znaku"`
}

// MaxChars of a slot: its duration × CharsPerSecond, at least 8.
func MaxChars(start, end float64) int {
	return max(8, int(math.Round((end-start)*CharsPerSecond)))
}

type Term struct {
	EN   string `yaml:"en"`
	CS   string `yaml:"cs"`
	Note string `yaml:"note,omitempty"`
}

// Glossary is series/<name>/glossary.yaml: names, places and catchphrases that
// must stay the same across episodes.
type Glossary struct {
	Terms []Term `yaml:"terms"`
}

func LoadGlossary(path string) (*Glossary, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Glossary{}, nil
	} else if err != nil {
		return nil, err
	}
	var g Glossary
	if err := yaml.Unmarshal(b, &g); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &g, nil
}

type Translator struct {
	LLM      LLM
	Glossary *Glossary
	// Series description for the prompt, e.g. the title.
	Series string
	// CheckModel rejects an answer from an unexpected model (silent fallback).
	CheckModel func(model string) error
}

const style = `Překládáš dětský animovaný seriál z angličtiny do češtiny pro dabing.
Pravidla:
- přirozená mluvená čeština, jak ji používá český dabing; krátké věty, žádný knižní styl;
- tykání mezi dětmi a v rodině, vykání k cizím dospělým, pokud kontext neříká jinak;
- jména a výrazy z glosáře přesně podle glosáře;
- každá replika se musí vejít do max_znaku (včetně mezer), raději kratší; když je to nutné, zkracuj obsah, ne srozumitelnost;
- citoslovce a smích přeber přirozeně (např. "Hehe", "Jé");
- nepřidávej poznámky ani vysvětlivky.
Odpověz jen JSON objektem {"lines":[{"id":<id>,"cs":"<překlad>"}]} se všemi id ze zadání.`

// Scenes splits lines into translation batches.
func Scenes(lines []Line) [][]Line {
	var out [][]Line
	var cur []Line
	for i, l := range lines {
		if len(cur) > 0 && (len(cur) >= MaxBatch || l.Start-lines[i-1].End > SceneGapS) {
			out = append(out, cur)
			cur = nil
		}
		cur = append(cur, l)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// Translate returns Czech text per line ID.
func (t *Translator) Translate(ctx context.Context, lines []Line) (map[int]string, error) {
	out := map[int]string{}
	var prev []string // last translated lines, as context for the next scene
	for _, scene := range Scenes(lines) {
		got, err := t.scene(ctx, scene, prev)
		if err != nil {
			return out, err
		}
		for _, l := range scene {
			out[l.ID] = got[l.ID]
			prev = append(prev, fmt.Sprintf("%s: %s", l.Character, got[l.ID]))
		}
		if len(prev) > ContextLines {
			prev = prev[len(prev)-ContextLines:]
		}
	}
	return out, nil
}

func (t *Translator) scene(ctx context.Context, scene []Line, prev []string) (map[int]string, error) {
	task, _ := json.MarshalIndent(scene, "", " ")
	var user strings.Builder
	if t.Series != "" {
		fmt.Fprintf(&user, "Seriál: %s\n\n", t.Series)
	}
	if g := t.glossaryText(); g != "" {
		fmt.Fprintf(&user, "Glosář (en → cs):\n%s\n\n", g)
	}
	if len(prev) > 0 {
		fmt.Fprintf(&user, "Předchozí repliky (už přeložené, jen pro kontext):\n%s\n\n", strings.Join(prev, "\n"))
	}
	fmt.Fprintf(&user, "Repliky k překladu:\n%s", task)
	msgs := []Message{{Role: "system", Content: style}, {Role: "user", Content: user.String()}}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		got, err := t.ask(ctx, msgs, scene)
		if err == nil {
			return got, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

// ask runs one LLM call and validates that every line has a non-empty answer.
func (t *Translator) ask(ctx context.Context, msgs []Message, lines []Line) (map[int]string, error) {
	text, model, err := t.LLM.Complete(ctx, msgs)
	if err == nil && t.CheckModel != nil {
		err = t.CheckModel(model)
	}
	if err != nil {
		return nil, err
	}
	var ans struct {
		Lines []struct {
			ID int    `json:"id"`
			CS string `json:"cs"`
		} `json:"lines"`
	}
	if err := json.Unmarshal([]byte(extractJSON(text)), &ans); err != nil {
		return nil, fmt.Errorf("llm: odpověď není JSON: %w", err)
	}
	got := map[int]string{}
	for _, l := range ans.Lines {
		got[l.ID] = strings.TrimSpace(l.CS)
	}
	for _, l := range lines {
		if got[l.ID] == "" {
			return nil, fmt.Errorf("llm: chybí překlad repliky %d", l.ID)
		}
	}
	return got, nil
}

// Shorten asks for a shorter version of one line (the fit loop, §5).
func (t *Translator) Shorten(ctx context.Context, l Line, current string) (string, error) {
	msgs := []Message{
		{Role: "system", Content: style},
		{Role: "user", Content: fmt.Sprintf(
			"Tahle česká replika je při namluvení delší než její čas. Zkrať ji na nejvýš %d znaků, "+
				"zachovej smysl a tón.\nOriginál: %s\nPostava: %s\nSoučasný překlad (%d znaků): %s\n"+
				`Odpověz jen JSON {"lines":[{"id":%d,"cs":"<kratší verze>"}]}.`,
			l.MaxChars, l.EN, l.Character, utf8.RuneCountInString(current), current, l.ID)},
	}
	got, err := t.ask(ctx, msgs, []Line{l})
	if err != nil {
		return "", err
	}
	return got[l.ID], nil
}

func (t *Translator) glossaryText() string {
	if t.Glossary == nil {
		return ""
	}
	var b strings.Builder
	for _, term := range t.Glossary.Terms {
		fmt.Fprintf(&b, "- %s → %s", term.EN, term.CS)
		if term.Note != "" {
			fmt.Fprintf(&b, " (%s)", term.Note)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
