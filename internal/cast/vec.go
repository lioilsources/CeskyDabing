package cast

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
)

// Cosine similarity of two vectors; 0 when the lengths differ or one is zero.
func Cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// Normalize returns v scaled to unit length (a copy).
func Normalize(v []float32) []float32 {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	out := make([]float32, len(v))
	if n == 0 {
		return out
	}
	n = math.Sqrt(n)
	for i, x := range v {
		out[i] = float32(float64(x) / n)
	}
	return out
}

// RunningMean folds v into the mean of n vectors and renormalizes.
func RunningMean(mean []float32, n int, v []float32) []float32 {
	if n == 0 || len(mean) != len(v) {
		return Normalize(v)
	}
	out := make([]float32, len(v))
	for i := range v {
		out[i] = (mean[i]*float32(n) + v[i]) / float32(n+1)
	}
	return Normalize(out)
}

// Blob encodes a vector as little-endian float32 (the DB format).
func Blob(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

func FromBlob(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("embedding: %d bajtů není násobek 4", len(b))
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v, nil
}

// Base64 is the cast.yaml format of a vector (Blob in base64).
func Base64(v []float32) string { return base64.StdEncoding.EncodeToString(Blob(v)) }

func FromBase64(s string) ([]float32, error) {
	if s == "" {
		return nil, nil
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("embedding: %w", err)
	}
	return FromBlob(b)
}
