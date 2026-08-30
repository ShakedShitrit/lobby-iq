// Package rankicon supplies Rocket League's rank badge art.
//
// The badges ship with the binary. There are 22 of them and they do not
// change, so downloading them at runtime would mean a network dependency, a
// cache directory, and a first match where the ranks show as text while the
// art arrives - all to fetch the same 255KB every install. They are embedded
// instead, and a lookup is a map read that cannot fail.
//
// The art lives in assets/ranks and comes from tracker.gg's CDN. Two sets are
// needed to cover every rank: the s4 set
// stops at Champion III, and Grand Champion I upwards only exist in the s15
// one. URL still builds those addresses, because the Discord card hands a URL
// to Discord and lets it fetch its own copy rather than uploading bytes.
package rankicon

import (
	"fmt"
	"path"
	"sync"

	"go.uber.org/zap"

	"github.com/ShakedShitrit/lobby-iq/assets"
)

// assetDir is the badges' directory within the embedded asset tree.
const assetDir = "ranks"

// iconURL is tracker.gg's rank art, indexed by Rocket League's own tier
// number: 0 unranked, three divisions per tier, up to Supersonic Legend at 22.
const iconURL = "https://trackercdn.com/cdn/tracker.gg/rocket-league/ranks/s4-%d.png"

// topTierURL covers Grand Champion I and above, which the s4 set has no art
// for. Verified against the CDN: neither set covers the other's range.
const topTierURL = "https://trackercdn.com/cdn/tracker.gg/rocket-league/ranks/s15rank%d.png"

// TopTier is the first tier served by topTierURL.
const TopTier = 19

// MaxTier is Supersonic Legend, the highest tier that has art.
const MaxTier = 22

// URL is the address of a tier's badge on the CDN.
//
// Used for Discord, which fetches and hosts its own copy. Anything drawing the
// badge itself should use Get instead, which needs no network.
func URL(tier int) string {
	if tier >= TopTier {
		return fmt.Sprintf(topTierURL, tier)
	}
	return fmt.Sprintf(iconURL, tier)
}

var (
	once   sync.Once
	byTier map[int][]byte
)

// Get returns a tier's badge.
//
// Unranked (tier 0) is reported as absent: it has no badge worth showing, and
// a caller should render that state as text.
func Get(tier int) ([]byte, bool) {
	once.Do(load)
	b, ok := byTier[tier]
	return b, ok
}

// Has reports whether a tier has art, without reading it.
func Has(tier int) bool {
	once.Do(load)
	_, ok := byTier[tier]
	return ok
}

// load reads the embedded badges once.
//
// A missing or unreadable badge is logged and skipped rather than panicking:
// the cost is one rank rendering as text, which the caller already handles,
// and that is not worth taking the application down for.
func load() {
	byTier = make(map[int][]byte, MaxTier)
	for tier := 1; tier <= MaxTier; tier++ {
		name := path.Join(assetDir, fmt.Sprintf("tier-%02d.png", tier))
		b, err := assets.Ranks.ReadFile(name)
		if err != nil {
			zap.L().Warn("rankicon: missing badge art",
				zap.Int("tier", tier), zap.String("file", name), zap.Error(err))
			continue
		}
		byTier[tier] = b
	}
}
