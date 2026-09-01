package rlversion

import (
	"bytes"
	"strings"
	"testing"
	"testing/iotest"
)

// utf16 encodes s the way the game's string table holds it, with the
// terminator, so tests describe what is in the file rather than raw bytes.
func utf16(s string) []byte {
	b := make([]byte, 0, 2*len(s)+2)
	for _, r := range s {
		b = append(b, byte(r), byte(r>>8))
	}
	return append(b, 0, 0)
}

func join(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// pad is the run of zeroes that separates unrelated strings, and which is what
// tells the version next to its feature set apart from the version on its own.
func pad(n int) []byte { return make([]byte, n) }

func TestScanFindsVersionAndFeatureSet(t *testing.T) {
	// The shape the real executable has: the version is mentioned once on its
	// own, and once immediately before the feature set.
	content := join(
		[]byte("some non-utf16 noise \xff\xfe\x01"),
		utf16("Encountered unknown property: %s in %s"),
		utf16("260825.79374.526531"),
		pad(24),
		utf16("DestinationObject"),
		pad(120),
		utf16("260825.79374.526531"),
		utf16("Update59.1"),
		pad(64),
	)

	v, ok, err := scan(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !ok {
		t.Fatal("scan found nothing")
	}
	if v.Game != "260825.79374.526531" {
		t.Errorf("Game = %q, want 260825.79374.526531", v.Game)
	}
	if v.FeatureSet != "PrimeUpdate59_1" {
		t.Errorf("FeatureSet = %q, want PrimeUpdate59_1", v.FeatureSet)
	}
}

// The file is read in chunks, and the strings wanted are around twenty
// characters two thirds of the way into a 38MB file, so landing across a chunk
// boundary is a matter of when rather than whether. Feeding a byte at a time
// puts a boundary between every pair of bytes at once.
func TestScanAcrossReadBoundaries(t *testing.T) {
	content := join(
		pad(4),
		utf16("260825.79374.526531"),
		utf16("Update59.1"),
	)

	v, ok, err := scan(iotest.OneByteReader(bytes.NewReader(content)))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !ok || v.Game != "260825.79374.526531" || v.FeatureSet != "PrimeUpdate59_1" {
		t.Fatalf("scan = %+v, %v; want the version and feature set", v, ok)
	}
}

// A version with no feature set beside it is still worth having: the version is
// what changes every patch, and the caller keeps its own feature set.
func TestScanVersionWithoutFeatureSet(t *testing.T) {
	content := join(
		utf16("260825.79374.526531"),
		pad(16),
		utf16("Update59.1"),
	)

	v, ok, err := scan(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !ok {
		t.Fatal("scan found nothing")
	}
	if v.Game != "260825.79374.526531" {
		t.Errorf("Game = %q, want the version", v.Game)
	}
	if v.FeatureSet != "" {
		t.Errorf("FeatureSet = %q, want empty: the strings were not adjacent", v.FeatureSet)
	}
}

func TestScanFindsNothing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content []byte
	}{
		{"empty", nil},
		{"no version", join(utf16("PrimeUpdate59_1"), utf16("Update59.1"))},
		{"ascii not utf16", []byte("260825.79374.526531\x00Update59.1\x00")},
		{"wrong shape", join(utf16("1.0.10897.0"), utf16("Update59.1"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, ok, err := scan(bytes.NewReader(tc.content))
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if ok {
				t.Errorf("scan = %+v, want nothing found", v)
			}
		})
	}
}

// A run of printable characters longer than any string being looked for is
// dropped rather than accumulated, and must not disturb the strings after it.
func TestScanSkipsOverlongRuns(t *testing.T) {
	content := join(
		utf16(strings.Repeat("A", 4*maxRun)),
		utf16("260825.79374.526531"),
		utf16("Update59.1"),
	)

	v, ok, err := scan(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !ok || v.Game != "260825.79374.526531" || v.FeatureSet != "PrimeUpdate59_1" {
		t.Fatalf("scan = %+v, %v; want the version and feature set", v, ok)
	}
}
