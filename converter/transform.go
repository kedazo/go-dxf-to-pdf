package converter

import "math"

type BBox struct {
	MinX, MinY, MaxX, MaxY float64
}

func (b *BBox) Expand(x, y float64) {
	if x < b.MinX {
		b.MinX = x
	}
	if y < b.MinY {
		b.MinY = y
	}
	if x > b.MaxX {
		b.MaxX = x
	}
	if y > b.MaxY {
		b.MaxY = y
	}
}

func (b *BBox) Width() float64  { return b.MaxX - b.MinX }
func (b *BBox) Height() float64 { return b.MaxY - b.MinY }

func NewBBox() BBox {
	return BBox{
		MinX: math.MaxFloat64, MinY: math.MaxFloat64,
		MaxX: -math.MaxFloat64, MaxY: -math.MaxFloat64,
	}
}

type Alignment int

const (
	AlignCenter Alignment = iota
	AlignBottomLeft
	AlignTopLeft
)

func ParseAlignment(s string) Alignment {
	switch s {
	case "bottom-left":
		return AlignBottomLeft
	case "top-left":
		return AlignTopLeft
	default:
		return AlignCenter
	}
}

// Transform converts DXF world coordinates to PDF page coordinates.
// DXF: Y-up, arbitrary coordinates.
// PDF: Y-down, origin at top-left, units in mm.
type Transform struct {
	Scale      float64   // drawing scale factor (e.g. 0.01 for 1:100)
	OffsetX    float64   // mm offset on page
	OffsetY    float64   // mm offset on page
	BBox       BBox      // DXF bounding box
	PageWidth  float64   // printable area width in mm
	PageHeight float64   // printable area height in mm
	MarginLeft float64
	MarginTop  float64
}

func NewTransform(bbox BBox, scale float64, paper PaperSize, margin float64, align Alignment, landscape bool) Transform {
	pw := paper.Width
	ph := paper.Height
	if landscape {
		pw, ph = ph, pw
	}

	printW := pw - 2*margin
	printH := ph - 2*margin

	// Scaled drawing size in mm
	drawW := bbox.Width() * scale
	drawH := bbox.Height() * scale

	var offX, offY float64
	switch align {
	case AlignCenter:
		offX = (printW - drawW) / 2
		offY = (printH - drawH) / 2
	case AlignBottomLeft:
		offX = 0
		offY = printH - drawH
	case AlignTopLeft:
		offX = 0
		offY = 0
	}

	return Transform{
		Scale:      scale,
		OffsetX:    offX,
		OffsetY:    offY,
		BBox:       bbox,
		PageWidth:  pw,
		PageHeight: ph,
		MarginLeft: margin,
		MarginTop:  margin,
	}
}

// X converts a DXF X coordinate to PDF X (mm from left edge of page).
func (t *Transform) X(dxfX float64) float64 {
	return t.MarginLeft + t.OffsetX + (dxfX-t.BBox.MinX)*t.Scale
}

// Y converts a DXF Y coordinate to PDF Y (mm from top edge of page).
// Flips Y axis: DXF Y-up -> PDF Y-down.
func (t *Transform) Y(dxfY float64) float64 {
	return t.MarginTop + t.OffsetY + (t.BBox.MaxY-dxfY)*t.Scale
}

// Dist converts a DXF distance to PDF distance in mm.
func (t *Transform) Dist(d float64) float64 {
	return d * t.Scale
}

// ShouldUseLandscape determines if landscape is better for the given bounding box.
func ShouldUseLandscape(bbox BBox, paper PaperSize) bool {
	drawAspect := bbox.Width() / bbox.Height()
	paperLandAspect := math.Max(paper.Width, paper.Height) / math.Min(paper.Width, paper.Height)
	paperPortAspect := math.Min(paper.Width, paper.Height) / math.Max(paper.Width, paper.Height)

	landDiff := math.Abs(drawAspect - paperLandAspect)
	portDiff := math.Abs(drawAspect - paperPortAspect)
	return landDiff < portDiff
}
