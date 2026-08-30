package app

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2/theme"

	"github.com/ShakedShitrit/lobby-iq/internal/rankicon"
)

// A tier without a colour would fall back to muted and read as unranked, so
// the table has to cover every tier the badges do.
func TestEveryTierHasAColour(t *testing.T) {
	for _, p := range []struct {
		name string
		pal  palette
	}{{"light", lightPalette}, {"dark", darkPalette}} {
		if got := len(p.pal.Tier); got != rankicon.MaxTier+1 {
			t.Errorf("%s: %d tier colours, want %d (0..%d)",
				p.name, got, rankicon.MaxTier+1, rankicon.MaxTier)
		}
		for tier := 1; tier <= rankicon.MaxTier; tier++ {
			if p.pal.TierColor(tier) == p.pal.Muted {
				t.Errorf("%s: tier %d has no colour of its own", p.name, tier)
			}
		}
	}
}

// Divisions of one rank share a colour; different ranks must not, or the tint
// says nothing.
func TestTierColoursGroupByRank(t *testing.T) {
	pal := lightPalette
	// Champion I, II, III are tiers 16-18 and are one rank.
	if pal.TierColor(16) != pal.TierColor(18) {
		t.Error("Champion I and III should share a colour")
	}
	// Champion III and Grand Champion I are different ranks.
	if pal.TierColor(18) == pal.TierColor(19) {
		t.Error("Champion and Grand Champion should not share a colour")
	}
	// Supersonic Legend is the last tier and has no divisions.
	if pal.TierColor(rankicon.MaxTier) == pal.TierColor(rankicon.MaxTier-1) {
		t.Error("Supersonic Legend should not share Grand Champion III's colour")
	}
}

// Anything off the end must not panic or index out of range - unranked is the
// common case, and a future season could add a tier before this does.
func TestTierColourOutOfRange(t *testing.T) {
	for _, tier := range []int{-5, 0, rankicon.MaxTier + 1, 1000} {
		if got := lightPalette.TierColor(tier); got != lightPalette.Muted {
			t.Errorf("TierColor(%d) = %v; want muted", tier, got)
		}
	}
}

func TestRowTintIsCaseInsensitive(t *testing.T) {
	pal := lightPalette
	if pal.RowTint("Blue") != pal.RowBlue || pal.RowTint("blue") != pal.RowBlue {
		t.Error("blue rows should tint blue whatever the casing")
	}
	if pal.RowTint("ORANGE") != pal.RowOrange {
		t.Error("orange rows should tint orange whatever the casing")
	}
	if pal.RowTint("Team 3") != color.Transparent {
		t.Error("an unknown team should not be tinted")
	}
}

// The badge backdrop exists only to give light windows the dark ground the
// rank art expects; a dark window already has one and must not get a block of
// colour behind every badge.
func TestBadgeBackdropOnlyInLight(t *testing.T) {
	if lightPalette.BadgeBackdrop == color.Transparent {
		t.Error("the light palette needs a backdrop or badges wash out")
	}
	if darkPalette.BadgeBackdrop != color.Transparent {
		t.Error("the dark palette should not draw a backdrop")
	}
}

func TestPaletteForVariant(t *testing.T) {
	if paletteFor(theme.VariantLight).Text != lightPalette.Text {
		t.Error("light variant should select the light palette")
	}
	if paletteFor(theme.VariantDark).Text != darkPalette.Text {
		t.Error("dark variant should select the dark palette")
	}
}
