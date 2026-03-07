package converter

import "math"

type TileGrid struct {
	Cols, Rows int
	TileW      float64 // printable width per tile in mm
	TileH      float64 // printable height per tile in mm
}

func ComputeTileGrid(drawingW, drawingH, printableW, printableH float64) TileGrid {
	cols := int(math.Ceil(drawingW / printableW))
	rows := int(math.Ceil(drawingH / printableH))
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return TileGrid{
		Cols:  cols,
		Rows:  rows,
		TileW: printableW,
		TileH: printableH,
	}
}

// DrawCropMarks draws small cross-hair marks at the corners of the printable area.
func DrawCropMarks(r *Renderer, margin, pageW, pageH float64) {
	const markLen = 5.0
	const markWidth = 0.1

	r.pdf.SetDrawColor(0, 0, 0)
	r.pdf.SetLineWidth(markWidth)

	// Top-left
	r.pdf.Line(margin-markLen, margin, margin, margin)
	r.pdf.Line(margin, margin-markLen, margin, margin)

	// Top-right
	right := pageW - margin
	r.pdf.Line(right, margin-markLen, right, margin)
	r.pdf.Line(right, margin, right+markLen, margin)

	// Bottom-left
	bottom := pageH - margin
	r.pdf.Line(margin-markLen, bottom, margin, bottom)
	r.pdf.Line(margin, bottom, margin, bottom+markLen)

	// Bottom-right
	r.pdf.Line(right, bottom, right+markLen, bottom)
	r.pdf.Line(right, bottom, right, bottom+markLen)
}
