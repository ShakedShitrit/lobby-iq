package presence

import (
	"strings"

	"github.com/ShakedShitrit/lobby-iq/internal/rankicon"
)

// rankNames is indexed by tier number, so the position in this slice is the
// value that identifies a tier's art.
var rankNames = []string{
	"Unranked",
	"Bronze I", "Bronze II", "Bronze III",
	"Silver I", "Silver II", "Silver III",
	"Gold I", "Gold II", "Gold III",
	"Platinum I", "Platinum II", "Platinum III",
	"Diamond I", "Diamond II", "Diamond III",
	"Champion I", "Champion II", "Champion III",
	"Grand Champion I", "Grand Champion II", "Grand Champion III",
	"Supersonic Legend",
}

// rankTiers maps the spellings a config file might use to the tier's first
// division. Matching takes the longest name that fits, so "gc2" can't be read
// as a Gold and "ssl" can't be read as a Silver.
var rankTiers = []struct {
	names     []string
	base      int
	divisions int
}{
	{[]string{"unranked", "none"}, 0, 0},
	{[]string{"bronze", "b"}, 1, 3},
	{[]string{"silver", "s"}, 4, 3},
	{[]string{"gold", "g"}, 7, 3},
	{[]string{"platinum", "plat", "p"}, 10, 3},
	{[]string{"diamond", "dia", "d"}, 13, 3},
	{[]string{"champion", "champ", "c"}, 16, 3},
	{[]string{"grandchampion", "grandchamp", "gchamp", "gc"}, 19, 3},
	{[]string{"supersoniclegend", "ssl"}, 22, 0},
}

// divisionSuffixes are what may follow a tier name, in either numeral style.
var divisionSuffixes = map[string]int{
	"": 1, "1": 1, "i": 1,
	"2": 2, "ii": 2,
	"3": 3, "iii": 3,
}

// parseRank turns a hand-written rank into its tier number and canonical name.
// It is forgiving on purpose - "Champion I", "champion 1", "champ1" and "c1"
// all mean the same thing, and a config file is written by hand.
func parseRank(s string) (tier int, name string, ok bool) {
	key := strings.ToLower(strings.TrimSpace(s))
	for _, drop := range []string{" ", "-", "_", ".", "div", "division"} {
		key = strings.ReplaceAll(key, drop, "")
	}
	if key == "" {
		return 0, "", false
	}

	// Longest match first, so a shorter alias can't shadow a longer one that
	// also fits.
	best := -1
	bestLen := 0
	for _, tierDef := range rankTiers {
		for _, alias := range tierDef.names {
			if len(alias) <= bestLen || !strings.HasPrefix(key, alias) {
				continue
			}
			suffix := key[len(alias):]
			div, valid := divisionSuffixes[suffix]
			if !valid {
				continue
			}
			if tierDef.divisions == 0 {
				// Unranked and SSL have no divisions; anything but a bare
				// name (or a redundant "I") is a typo.
				if div != 1 {
					continue
				}
				best, bestLen = tierDef.base, len(alias)
				continue
			}
			if div > tierDef.divisions {
				continue
			}
			best, bestLen = tierDef.base+div-1, len(alias)
		}
	}

	if best < 0 || best >= len(rankNames) {
		return 0, "", false
	}
	return best, rankNames[best], true
}

// rankIcon is the art for a tier, handed straight to Discord, which fetches
// and hosts its own copy.
//
// The address is defined in internal/rankicon rather than here because the
// desktop table shows the same badges and needs the bytes; one definition of
// where the art lives keeps the two from drifting apart.
func rankIcon(tier int) string {
	return rankicon.URL(tier)
}
