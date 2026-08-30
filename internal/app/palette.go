package app

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// palette is every colour the window draws with, for one theme variant.
//
// Colours are set explicitly rather than taken from the theme's foreground
// because the theme's normal foreground has rendered indistinguishable from
// the background in this environment. Naming them here means following the
// OS's light/dark setting without depending on that.
type palette struct {
	Text   color.Color
	Muted  color.Color
	Danger color.Color
	Ok     color.Color

	// RowBlue and RowOrange tint a row by team, faintly enough to read text
	// over. They are what makes the two teams read as blocks rather than as
	// six rows with a coloured dot.
	RowBlue   color.Color
	RowOrange color.Color

	// BadgeBackdrop sits behind rank art. Rocket League draws those badges for
	// a dark in-game background, so on a light window they wash out; this is
	// the dark ground they expect. Transparent where the window is already
	// dark and they need no help.
	BadgeBackdrop color.Color

	// Tier is the rank colour by Rocket League's tier number, 0 unranked to 22
	// Supersonic Legend, used to tint the MMR figure so relative skill reads
	// without decoding the badge.
	Tier []color.Color
}

// lightPalette is for a light window. The tier colours are the darker end of
// each rank's hue so they stay legible on white.
var lightPalette = palette{
	Text:          color.NRGBA{R: 0x1a, G: 0x1a, B: 0x1a, A: 0xff},
	Muted:         color.NRGBA{R: 0x60, G: 0x60, B: 0x60, A: 0xff},
	Danger:        color.NRGBA{R: 0xd9, G: 0x2b, B: 0x2b, A: 0xff},
	Ok:            color.NRGBA{R: 0x1b, G: 0x8a, B: 0x3a, A: 0xff},
	RowBlue:       color.NRGBA{R: 0x2d, G: 0x6e, B: 0xdc, A: 0x1c},
	RowOrange:     color.NRGBA{R: 0xe0, G: 0x7b, B: 0x20, A: 0x1c},
	BadgeBackdrop: color.NRGBA{R: 0x2b, G: 0x30, B: 0x3a, A: 0xff},
	Tier: tierColors(
		color.NRGBA{R: 0x8a, G: 0x5a, B: 0x2b, A: 0xff}, // bronze
		color.NRGBA{R: 0x5f, G: 0x6b, B: 0x7a, A: 0xff}, // silver
		color.NRGBA{R: 0x9a, G: 0x7a, B: 0x12, A: 0xff}, // gold
		color.NRGBA{R: 0x1f, G: 0x7f, B: 0x8b, A: 0xff}, // platinum
		color.NRGBA{R: 0x2f, G: 0x56, B: 0xb0, A: 0xff}, // diamond
		color.NRGBA{R: 0x70, G: 0x38, B: 0xa8, A: 0xff}, // champion
		color.NRGBA{R: 0xb3, G: 0x32, B: 0x3c, A: 0xff}, // grand champion
		color.NRGBA{R: 0xb8, G: 0x3f, B: 0x86, A: 0xff}, // supersonic legend
	),
}

// darkPalette is for a dark window. The same hues, lightened, since they now
// have to carry on a dark ground.
var darkPalette = palette{
	Text:          color.NRGBA{R: 0xe8, G: 0xea, B: 0xed, A: 0xff},
	Muted:         color.NRGBA{R: 0x9a, G: 0xa1, B: 0xac, A: 0xff},
	Danger:        color.NRGBA{R: 0xf2, G: 0x70, B: 0x70, A: 0xff},
	Ok:            color.NRGBA{R: 0x4a, G: 0xc9, B: 0x74, A: 0xff},
	RowBlue:       color.NRGBA{R: 0x4d, G: 0x8d, B: 0xff, A: 0x2b},
	RowOrange:     color.NRGBA{R: 0xff, G: 0x9d, B: 0x3d, A: 0x2b},
	BadgeBackdrop: color.Transparent,
	Tier: tierColors(
		color.NRGBA{R: 0xcf, G: 0x8b, B: 0x52, A: 0xff}, // bronze
		color.NRGBA{R: 0xb6, G: 0xc2, B: 0xd0, A: 0xff}, // silver
		color.NRGBA{R: 0xe8, G: 0xc3, B: 0x4a, A: 0xff}, // gold
		color.NRGBA{R: 0x6f, G: 0xd3, B: 0xe0, A: 0xff}, // platinum
		color.NRGBA{R: 0x7a, G: 0x9d, B: 0xf0, A: 0xff}, // diamond
		color.NRGBA{R: 0xb5, G: 0x7a, B: 0xe8, A: 0xff}, // champion
		color.NRGBA{R: 0xf2, G: 0x70, B: 0x7a, A: 0xff}, // grand champion
		color.NRGBA{R: 0xf5, G: 0x8a, B: 0xc4, A: 0xff}, // supersonic legend
	),
}

// tierColors expands one colour per rank into one per tier.
//
// Rocket League's tier numbers run 0 unranked, then three divisions of each
// rank, up to Supersonic Legend at 22 - which has no divisions. Taking a
// colour per rank and repeating it keeps the two in step: a table written out
// tier by tier would be 23 entries that have to be counted by hand.
func tierColors(ranks ...color.Color) []color.Color {
	out := make([]color.Color, 0, 23)
	out = append(out, color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}) // unranked
	for i, c := range ranks {
		// Supersonic Legend, the last one, is a single tier.
		reps := 3
		if i == len(ranks)-1 {
			reps = 1
		}
		for r := 0; r < reps; r++ {
			out = append(out, c)
		}
	}
	return out
}

// TierColor is the colour for a tier, falling back to muted for anything
// outside the table - which covers unranked and any tier a future season adds
// before this does.
func (p palette) TierColor(tier int) color.Color {
	if tier <= 0 || tier >= len(p.Tier) {
		return p.Muted
	}
	return p.Tier[tier]
}

// RowTint is the background for a player's row.
//
// Teams are told apart by tint rather than by the dot alone, which is a lot
// easier to read at a glance mid-match.
func (p palette) RowTint(team string) color.Color {
	// Matched case-insensitively, as teamImportance already does: the name
	// comes from the game and is not ours to depend on the casing of.
	switch strings.ToLower(team) {
	case "blue":
		return p.RowBlue
	case "orange":
		return p.RowOrange
	default:
		return color.Transparent
	}
}

// paletteFor picks the palette matching a theme variant.
func paletteFor(v fyne.ThemeVariant) palette {
	if v == theme.VariantLight {
		return lightPalette
	}
	return darkPalette
}
