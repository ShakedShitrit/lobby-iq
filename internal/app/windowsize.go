package app

import (
	"fyne.io/fyne/v2"
)

// tableChrome is the width the window needs beyond the columns themselves:
// container padding, the card's border, and the vertical scrollbar.
//
// It is a measured constant rather than a derived one because Fyne offers no
// way to ask for it - and asking the content is no help either. widget.Table
// scrolls, so its MinSize is a small default viewport rather than the sum of
// its columns; a window sized from Content().MinSize() would open too narrow
// to show the table it exists for.
//
// Measured by sizing a window by hand until nothing truncated: 1067 points of
// client width against 954 points of columns, with every column present.
const tableChrome = 113

// minWindowHeight is the height that shows a full 3v3 lobby, its header row,
// and the surrounding controls without scrolling.
//
// Height is a floor rather than a fit: the table scrolls, so being a little
// short costs a scroll, whereas being too narrow truncates a column outright
// and cannot be recovered without resizing.
const minWindowHeight = 520

// preferredWindowSize is the size that shows everything the window currently
// contains.
//
// The width follows the columns, so a column added or hidden moves the window
// with it - the rating columns appear only when signed in, and the window
// should not open 180 points wider than it needs on a run without them.
func preferredWindowSize(content fyne.CanvasObject, cols []string) fyne.Size {
	size := content.MinSize()

	// The header and footer report their widths honestly, so MinSize covers
	// them; only the table has to be worked out.
	if want := tableWidth(cols); want > size.Width {
		size.Width = want
	}
	if size.Height < minWindowHeight {
		size.Height = minWindowHeight
	}
	return size
}

// tableWidth is the client width needed to show every column in full.
func tableWidth(cols []string) float32 {
	var total float32
	for _, name := range cols {
		total += guiColumnWidth(name)
	}
	return total + tableChrome
}
