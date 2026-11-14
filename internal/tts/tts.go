// Package tts synthesizes Czech lines in a character's cloned voice.
//
// The M1 backend is the audio service of AiStack (XTTS-v2 behind
// /v1/audio/tts): a character's reference is stored there once as a voice,
// then every line refers to it by id. The service refuses to clone a voice
// without a rights declaration; dub never invents one — it comes from config.
package tts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

type Request struct {
	Text     string
	Language string
	VoiceID  string // from EnsureVoice
	Speed    float64
	Seed     *int
}

type TTS interface {
	// EnsureVoice stores the reference once and returns its voice id. A changed
	// reference file gets a new id.
	EnsureVoice(ctx context.Context, v Voice) (string, error)
	// Synthesize returns a WAV.
	Synthesize(ctx context.Context, r Request) ([]byte, error)
}

// Voice is a character reference to clone.
type Voice struct {
	Series, Character string
	RefPath           string
	Language          string
}

var reVoiceID = regexp.MustCompile(`[^a-z0-9]+`)

// VoiceID is "dub-<series>-<character>-<ref hash>", at most 48 chars of
// [a-z0-9_-] (the audio service's rule).
func VoiceID(series, character string, ref []byte) string {
	sum := sha256.Sum256(ref)
	part := func(s string, n int) string {
		s = strings.Trim(reVoiceID.ReplaceAllString(strings.ToLower(s), "-"), "-")
		if len(s) > n {
			s = strings.TrimRight(s[:n], "-")
		}
		return s
	}
	return "dub-" + part(series, 16) + "-" + part(character, 16) + "-" + hex.EncodeToString(sum[:4])
}
