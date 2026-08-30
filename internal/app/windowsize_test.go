package app

import "testing"

// The chrome constant was measured against a window sized by hand until
// nothing truncated. This pins that measurement to the layout that produced
// it: if a column's width changes, or one is added, the expected number here
// has to be revisited deliberately rather than the window quietly opening too
// narrow.
func TestTableWidthMatchesTheMeasuredWindow(t *testing.T) {
	const measured = 1067

	got := tableWidth(guiColumnsFor(true))
	if got != measured {
		t.Errorf("tableWidth(with ratings) = %v; want %v, the width measured "+
			"from a window sized by hand to fit every column", got, measured)
	}
}

// Without a sign-in there are no rating columns, and the window should not
// open wide enough for columns it will not draw.
func TestTableWidthShrinksWithoutRatingColumns(t *testing.T) {
	with := tableWidth(guiColumnsFor(true))
	without := tableWidth(guiColumnsFor(false))

	if without >= with {
		t.Errorf("without ratings = %v, with = %v; want the narrower layout "+
			"to ask for less", without, with)
	}
	// The two rating columns are the only difference, so the gap is exactly
	// their combined width.
	wantGap := guiColumnWidth(colRank) + guiColumnWidth(colMMR)
	if gap := with - without; gap != wantGap {
		t.Errorf("gap = %v; want %v (RANK + MMR)", gap, wantGap)
	}
}

// Every column has to contribute, or a column added later would be invisible
// to the sizing and truncate on first run.
func TestTableWidthCountsEveryColumn(t *testing.T) {
	cols := guiColumnsFor(true)
	base := tableWidth(cols)

	for i := range cols {
		shorter := append(append([]string{}, cols[:i]...), cols[i+1:]...)
		if got := tableWidth(shorter); got >= base {
			t.Errorf("dropping %q did not narrow the window: %v vs %v",
				cols[i], got, base)
		}
	}
}
