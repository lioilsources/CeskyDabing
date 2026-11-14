package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/lioilsources/CeskyDabing/internal/media"
	"github.com/lioilsources/CeskyDabing/internal/store"
	"github.com/lioilsources/CeskyDabing/internal/store/db"
	"github.com/lioilsources/CeskyDabing/internal/translate"
)

const (
	FileTranslate = "translate.json"
	FileGlossary  = "glossary.yaml"
)

// Translate: EN segments → Czech with a length limit per slot, through the LLM.
type Translate struct {
	Store *store.Store
	Root  string
	LLM   translate.LLM
}

// TranslatedLine is one entry of translate.json.
type TranslatedLine struct {
	Idx       int    `json:"idx"`
	Character string `json:"character"`
	EN        string `json:"en"`
	CS        string `json:"cs"`
	MaxChars  int    `json:"max_chars"`
	Chars     int    `json:"chars"`
}

// NewTranslator builds the translator of a series (glossary, model check).
func NewTranslator(root, series string, llm translate.LLM) (*translate.Translator, error) {
	g, err := translate.LoadGlossary(filepath.Join(SeriesDir(root, series), FileGlossary))
	if err != nil {
		return nil, err
	}
	return &translate.Translator{LLM: llm, Glossary: g, Series: series, CheckModel: RejectFallback}, nil
}

// RejectFallback fails an answer from the small fallback model the gateway
// silently switches to when the requested one is down.
func RejectFallback(model string) error {
	if strings.Contains(strings.ToLower(model), "fallback") {
		return fmt.Errorf("llm odpověděl záložním modelem %q, požadovaný model neběží (služba v brokeru?)", model)
	}
	return nil
}

func (s Translate) Run(ctx context.Context, ep *Episode) (Outputs, error) {
	segs, err := s.Store.ListSegmentsWithCharacter(ctx, ep.ID)
	if err != nil {
		return nil, err
	}
	tr, err := NewTranslator(s.Root, ep.Series, loggedLLM{LLM: s.LLM, store: s.Store, ep: ep, step: "translate"})
	if err != nil {
		return nil, err
	}
	var lines []translate.Line
	for _, sg := range segs {
		if sg.IsSong != 0 {
			continue // songs keep the original with subtitles (§5)
		}
		lines = append(lines, translate.Line{
			ID: int(sg.Idx), Start: sg.StartS, End: sg.EndS, Character: orUnknown(sg.CharacterName),
			EN: sg.TextEn, MaxChars: translate.MaxChars(sg.StartS, sg.EndS),
		})
	}
	cs, err := tr.Translate(ctx, lines)
	if err != nil {
		return nil, err
	}
	out := make([]TranslatedLine, 0, len(lines))
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
		for _, l := range lines {
			if err := q.SetSegmentCS(ctx, db.SetSegmentCSParams{TextCs: cs[l.ID], EpisodeID: ep.ID, Idx: int64(l.ID)}); err != nil {
				return err
			}
			out = append(out, TranslatedLine{Idx: l.ID, Character: l.Character, EN: l.EN, CS: cs[l.ID],
				MaxChars: l.MaxChars, Chars: utf8.RuneCountInString(cs[l.ID])})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := media.WriteJSON(filepath.Join(ep.WorkDir, FileTranslate), out); err != nil {
		return nil, err
	}
	return Outputs{"translate": FileTranslate}, nil
}

func orUnknown(name string) string {
	if name == "" {
		return "neznámá postava"
	}
	return name
}

// loggedLLM records every LLM call in the jobs table.
type loggedLLM struct {
	translate.LLM
	store *store.Store
	ep    *Episode
	step  string
}

type llmAnswer struct {
	Model string `json:"model"`
	Text  string `json:"text"`
}

func (l loggedLLM) Complete(ctx context.Context, msgs []translate.Message) (string, string, error) {
	res, err := logJob(ctx, l.store, l.ep, l.step, "llm", msgs, func() (llmAnswer, error) {
		text, model, err := l.LLM.Complete(ctx, msgs)
		return llmAnswer{Model: model, Text: text}, err
	})
	return res.Text, res.Model, err
}
