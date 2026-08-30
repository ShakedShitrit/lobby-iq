package rlstats

import (
	"regexp"
	"strings"
	"unicode"
)

// The Stats API reports the arena as its internal map name, e.g.
// "Labs_4v4_Arena15_Blackout_P" or "EuroStadium_Night_P". These strip the
// boilerplate that carries no meaning for a reader.
var (
	arenaNoise   = regexp.MustCompile(`(?i)^(labs|park|stadium|wasteland|underwater|hoops|throwback|beach)$`)
	arenaSizeTag = regexp.MustCompile(`(?i)^\d+v\d+$`)
	arenaIndexed = regexp.MustCompile(`(?i)^arena\d*$`)
)

// PrettyArena turns an internal map name into something readable, e.g.
// "Labs_4v4_Arena15_Blackout_P" -> "Blackout". It is a best-effort cleanup
// rather than a lookup table of official names: Psyonix ships new maps and
// variants regularly, and a stale table would render them worse than these
// rules do. Unrecognisable input is returned unchanged.
func PrettyArena(arena string) string {
	arena = strings.TrimSpace(arena)
	if arena == "" {
		return ""
	}

	// Drop Unreal's persistent-level suffix first, so it can't stand in as
	// the "something more specific" the noise rule below looks for.
	trimmed := strings.TrimSuffix(arena, "_P")

	parts := strings.Split(trimmed, "_")
	kept := make([]string, 0, len(parts))
	for i, part := range parts {
		if part == "" || arenaSizeTag.MatchString(part) || arenaIndexed.MatchString(part) {
			continue
		}
		// Leading generic words are a category, not the map's identity - but
		// only drop them while something more specific still follows.
		if len(kept) == 0 && arenaNoise.MatchString(part) && i < len(parts)-1 {
			continue
		}
		kept = append(kept, splitCamel(part))
	}

	if len(kept) == 0 {
		return arena
	}
	return strings.Join(kept, " ")
}

// splitCamel inserts spaces at lower-to-upper transitions, so "EuroStadium"
// reads as "Euro Stadium".
func splitCamel(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) && !unicode.IsUpper(runes[i-1]) {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}
