package app

import (
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"github.com/ShakedShitrit/lobby-iq/assets"
)

// brandEmblemSize is the header badge's drawn size, chosen to stand as tall as
// the two lines of text beside it so the header reads as one block rather than
// as a picture with a caption.
const brandEmblemSize = 44

var (
	brandOnce sync.Once
	brandRes  fyne.Resource
)

// brandEmblem is the app's mark as a Fyne resource, or nil if it could not be
// read.
//
// Memoised for the same reason the rank badges are: Fyne compares resource
// identity to decide whether an image changed, so a fresh resource each time
// would re-upload the same texture. There is only one of these, but the window
// icon and the header both want it.
//
// go:embed makes the read a compile-time guarantee, so the nil case is
// unreachable in a binary that built. It is handled rather than panicked on
// because a missing decoration is not worth taking the app down for.
func brandEmblem() fyne.Resource {
	brandOnce.Do(func() {
		b, err := assets.Brand.ReadFile(assets.EmblemFile)
		if err != nil {
			return
		}
		brandRes = fyne.NewStaticResource("lobbyiq-emblem.png", b)
	})
	return brandRes
}

// newBrandEmblem is the header badge, or nil when there is no art to draw -
// which callers must treat as "leave it out", not as an empty gap.
func newBrandEmblem() *canvas.Image {
	res := brandEmblem()
	if res == nil {
		return nil
	}
	img := canvas.NewImageFromResource(res)
	// Contain rather than Stretch: the source is square, but that is a
	// property of today's art file and not something to bake into the layout.
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(brandEmblemSize, brandEmblemSize))
	return img
}
