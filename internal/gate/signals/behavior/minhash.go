package behavior

import (
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// MinHash sketches of post text (similarity job). A sketch is MinHashK
// minimum hash values over the text's word shingles; the share of equal
// positions of two sketches estimates the Jaccard similarity of their
// shingle sets. Only sketches leave this file's callers' stack frames: the
// text itself is never stored.

const (
	// MinHashK is the sketch length (positions).
	MinHashK = 64
	// Bands × Rows = MinHashK: LSH bucketing finds candidate pairs whose
	// sketches agree on every row of at least one band.
	Bands = 16
	Rows  = MinHashK / Bands
	// ShingleWords is the shingle size in words.
	ShingleWords = 3
)

// Sketch is a MinHash sketch.
type Sketch [MinHashK]uint64

// words lowercases s and splits it on anything that is not a letter or digit.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

// shingles returns the hashes of the ShingleWords-word shingles of texts
// (a text shorter than that is one shingle).
func shingles(texts []string) map[uint64]struct{} {
	out := map[uint64]struct{}{}
	for _, t := range texts {
		w := words(t)
		if len(w) == 0 {
			continue
		}
		n := max(len(w)-ShingleWords+1, 1)
		for i := range n {
			h := fnv.New64a()
			for j := i; j < min(i+ShingleWords, len(w)); j++ {
				_, _ = h.Write([]byte(w[j]))
				_, _ = h.Write([]byte{0})
			}
			out[h.Sum64()] = struct{}{}
		}
	}
	return out
}

// mix is the i-th hash function over a shingle hash (splitmix64 of x ⊕ seed).
func mix(x uint64, i int) uint64 {
	z := x ^ (uint64(i+1) * 0x9e3779b97f4a7c15) //nolint:gosec // i < MinHashK
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// SketchOf returns the MinHash sketch of texts and whether it has any shingle.
func SketchOf(texts []string) (Sketch, bool) {
	var s Sketch
	for i := range s {
		s[i] = math.MaxUint64
	}
	sh := shingles(texts)
	for x := range sh {
		for i := range s {
			if h := mix(x, i); h < s[i] {
				s[i] = h
			}
		}
	}
	return s, len(sh) > 0
}

// Jaccard estimates the Jaccard similarity of two sketches.
func Jaccard(a, b Sketch) float64 {
	eq := 0
	for i := range a {
		if a[i] == b[i] {
			eq++
		}
	}
	return float64(eq) / MinHashK
}

// band returns the bucket key of band k of s.
func (s Sketch) band(k int) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	for _, v := range s[k*Rows : (k+1)*Rows] {
		for i := range buf {
			buf[i] = byte(v >> (8 * i))
		}
		_, _ = h.Write(buf[:])
	}
	return h.Sum64()
}
