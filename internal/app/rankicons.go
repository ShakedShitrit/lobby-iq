package app

import (
	"fmt"
	"sync"

	"fyne.io/fyne/v2"

	"github.com/ShakedShitrit/lobby-iq/internal/rankicon"
)

// rankIconSize is the badge's drawn size. It matches the table's row height
// closely enough that a row with a badge is the same height as one without,
// which keeps the rows aligned while a lobby is still being read.
const rankIconSize = 28

// guiIcons wraps the embedded badge art in Fyne resources.
//
// The wrapping is memoised even though the bytes are already in memory,
// because a fyne.Resource is what the toolkit compares to decide whether an
// image changed: handing it a freshly built resource on every redraw would
// make it re-upload the same texture for every cell, every frame.
type guiIcons struct {
	mu  sync.Mutex
	res map[int]fyne.Resource
}

func newGUIIcons() *guiIcons {
	return &guiIcons{res: map[int]fyne.Resource{}}
}

// Get returns the badge for a tier. have is false for a tier with no art -
// unranked, or anything past Supersonic Legend - and the caller should render
// that as text.
func (g *guiIcons) Get(tier int) (fyne.Resource, bool) {
	if g == nil || tier <= 0 {
		return nil, false
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if res, ok := g.res[tier]; ok {
		return res, true
	}
	b, ok := rankicon.Get(tier)
	if !ok {
		return nil, false
	}
	res := fyne.NewStaticResource(fmt.Sprintf("rank-%02d.png", tier), b)
	g.res[tier] = res
	return res, true
}
