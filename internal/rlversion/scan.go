package rlversion

import (
	"io"
	"regexp"
	"strings"
)

// The version and the feature set sit next to each other in the executable's
// string table, as two consecutive UTF-16 strings:
//
//	"260825.79374.526531\0Update59.1\0"
//
// The version alone appears elsewhere too - it is logged on startup - so it is
// the adjacency that identifies the pair, and the pair is what carries the
// feature set. A lone version is still worth having, and is kept as a fallback.
var (
	versionPattern = regexp.MustCompile(`^[0-9]{6}\.[0-9]{3,7}\.[0-9]{4,8}$`)
	suffixPattern  = regexp.MustCompile(`^Update[0-9][0-9A-Za-z._]*$`)
)

// featurePrefix is the part of the feature set the executable does not spell
// out next to the version: PsyNet wants "PrimeUpdate59_1" where the string
// table holds only "Update59.1". Every Rocket League release since the free to
// play one has used this prefix.
const featurePrefix = "Prime"

// maxRun bounds a candidate string. Both strings being looked for are around
// twenty characters, and a bound keeps a run of megabytes of printable data -
// which a 38MB executable certainly contains - from being accumulated whole.
const maxRun = 64

// chunkSize is how much of the executable is read at a time. The strings are
// two thirds of the way in, so most of the file gets read either way; the point
// is only to avoid holding it all in memory at once.
const chunkSize = 64 << 10

// scan reads a Rocket League executable and returns the version it was built
// as. The boolean reports whether anything was found: a future build that
// stores this differently is an ordinary outcome, not an error.
func scan(r io.Reader) (Version, bool, error) {
	var s scanner
	buf := make([]byte, chunkSize)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s.feed(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return Version{}, false, err
		}
	}
	s.flush()
	return s.result()
}

// scanner walks the file two bytes at a time, collecting runs of UTF-16
// characters that are plain ASCII - which is what a string table entry looks
// like from the outside, without parsing the PE format the entries live in.
//
// Reading the format properly would mean locating .rdata and knowing how the
// compiler laid the literals out, which is more assumption about a binary we do
// not build than the scan below makes.
//
// Characters are paired from the start of the file, which assumes the strings
// sit at even offsets. They do: sections begin at a multiple of the file
// alignment and wide literals are two-byte aligned within them. An executable
// that broke that assumption would yield nothing rather than something wrong,
// and the caller falls back to the version it already had.
type scanner struct {
	off  int64 // offset of the next byte to be fed
	half bool  // the low byte of a character is pending
	lo   byte

	run      []byte // characters of the run in progress
	runStart int64
	overlong bool // the run in progress exceeded maxRun and was dropped

	// prev is the last complete run, kept so the run after it can be tested
	// for being its neighbour. prevEnd is the offset just past its terminator.
	prev    string
	prevEnd int64

	pairVersion string
	pairSuffix  string
	loneVersion string
}

func (s *scanner) feed(b []byte) {
	for _, c := range b {
		if !s.half {
			s.lo = c
			s.half = true
			s.off++
			continue
		}
		s.half = false
		s.off++
		// The pair started one byte back.
		s.char(s.lo, c, s.off-2)
	}
}

// char takes one UTF-16 character. Anything that is not printable ASCII ends
// the run, the NUL terminator included.
func (s *scanner) char(lo, hi byte, start int64) {
	if hi == 0 && lo >= 0x20 && lo <= 0x7e {
		if len(s.run) == 0 && !s.overlong {
			s.runStart = start
		}
		if len(s.run) >= maxRun {
			s.run = s.run[:0]
			s.overlong = true
			return
		}
		s.run = append(s.run, lo)
		return
	}
	s.flush()
}

// flush completes the run in progress.
func (s *scanner) flush() {
	run, overlong := s.run, s.overlong
	s.run, s.overlong = s.run[:0], false
	if overlong || len(run) == 0 {
		return
	}
	s.emit(string(run), s.runStart)
}

func (s *scanner) emit(run string, start int64) {
	switch {
	case versionPattern.MatchString(run):
		if s.loneVersion == "" {
			s.loneVersion = run
		}
	case s.pairVersion == "" && suffixPattern.MatchString(run) &&
		start == s.prevEnd && versionPattern.MatchString(s.prev):
		s.pairVersion, s.pairSuffix = s.prev, run
	}

	s.prev = run
	// Past the characters and the one NUL character that ended them, which is
	// where an adjacent string begins.
	s.prevEnd = start + int64(2*len(run)) + 2
}

func (s *scanner) result() (Version, bool, error) {
	if s.pairVersion != "" {
		return Version{
			Game: s.pairVersion,
			// PsyNet spells the feature set with underscores where the game's
			// own string uses dots: "Update59.1" is "PrimeUpdate59_1".
			FeatureSet: featurePrefix + strings.ReplaceAll(s.pairSuffix, ".", "_"),
		}, true, nil
	}
	if s.loneVersion != "" {
		// The version without the feature set. Worth returning: the version is
		// what changes every patch, while the feature set changes only with a
		// numbered update, so the caller's existing feature set is likely still
		// right and is left in place.
		return Version{Game: s.loneVersion}, true, nil
	}
	return Version{}, false, nil
}
