// Package assets embeds the art that ships with LobbyIQ.
//
// It exists because go:embed can only reach files at or below the directory of
// the package declaring it, and the art lives here, beside the Discord badges
// it belongs with - rather than being scattered into whichever internal
// package happens to draw it.
//
// Not everything in this directory is embedded. assets/discord holds art that
// is uploaded to a Discord application once, by hand, and referenced by key;
// only what the binary draws itself is compiled in.
package assets

import "embed"

// Ranks holds Rocket League's rank badges, named ranks/tier-NN.png by tier
// number, 01 through 22. Read them through internal/rankicon rather than
// reaching in here, so the tier-to-file mapping stays in one place.
//
//go:embed ranks/*.png
var Ranks embed.FS
