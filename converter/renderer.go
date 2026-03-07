package converter

import (
	"math"

	"github.com/go-pdf/fpdf"
)

type Renderer struct {
	pdf       *fpdf.Fpdf
	transform Transform
}

func NewRenderer(paper PaperSize, landscape bool, margin float64) *Renderer {
	orient := "P"
	if landscape {
		orient = "L"
	}
	// Always pass the original paper dimensions — fpdf swaps them when orient="L"
	pdf := fpdf.NewCustom(&fpdf.InitType{
		OrientationStr: orient,
		UnitStr:        "mm",
		Size:           fpdf.SizeType{Wd: paper.Width, Ht: paper.Height},
	})
	pdf.SetMargins(margin, margin, margin)
	pdf.SetAutoPageBreak(false, 0)

	// Add a UTF-8 capable font for proper Unicode text rendering (e.g. Hungarian ő, ű)
	pdf.SetFontLocation("/usr/share/fonts/truetype/dejavu")
	pdf.AddUTF8Font("DejaVu", "", "DejaVuSans.ttf")
	pdf.AddUTF8Font("DejaVu", "B", "DejaVuSans-Bold.ttf")
	pdf.AddUTF8Font("DejaVu", "I", "DejaVuSans-Oblique.ttf")
	pdf.AddUTF8Font("DejaVu", "BI", "DejaVuSans-BoldOblique.ttf")

	pdf.AddPage()
	return &Renderer{pdf: pdf}
}

func (r *Renderer) SetTransform(t Transform) {
	r.transform = t
}

func (r *Renderer) SetStyle(color RGB, lineWidthMM float64) {
	r.pdf.SetDrawColor(int(color.R), int(color.G), int(color.B))
	r.pdf.SetLineWidth(lineWidthMM)
}

func (r *Renderer) DrawLine(x1, y1, x2, y2 float64) {
	r.pdf.Line(
		r.transform.X(x1), r.transform.Y(y1),
		r.transform.X(x2), r.transform.Y(y2),
	)
}

func (r *Renderer) DrawCircle(cx, cy, radius float64) {
	r.pdf.Circle(
		r.transform.X(cx), r.transform.Y(cy),
		r.transform.Dist(radius),
		"D",
	)
}

func (r *Renderer) DrawArc(cx, cy, radius, startAngleDeg, endAngleDeg float64) {
	px := r.transform.X(cx)
	py := r.transform.Y(cy)
	pr := r.transform.Dist(radius)

	// fpdf.Arc internally handles Y-flip: fpdf 90° = UP on screen,
	// same as DXF convention. So DXF angles pass through directly.
	pdfStart := math.Mod(startAngleDeg, 360)
	if pdfStart < 0 {
		pdfStart += 360
	}
	pdfEnd := math.Mod(endAngleDeg, 360)
	if pdfEnd < 0 {
		pdfEnd += 360
	}
	if pdfEnd <= pdfStart {
		pdfEnd += 360
	}

	r.pdf.Arc(px, py, pr, pr, 0, pdfStart, pdfEnd, "D")
}

func (r *Renderer) DrawEllipse(cx, cy, majorX, majorY, minorRatio, startAngleRad, endAngleRad float64) {
	majorLen := math.Sqrt(majorX*majorX + majorY*majorY)
	minorLen := majorLen * minorRatio
	rotDeg := math.Atan2(majorY, majorX) * 180 / math.Pi

	rx := r.transform.Dist(majorLen)
	ry := r.transform.Dist(minorLen)
	px := r.transform.X(cx)
	py := r.transform.Y(cy)

	isFullEllipse := math.Abs(endAngleRad-startAngleRad-2*math.Pi) < 0.001 ||
		(startAngleRad == 0 && endAngleRad == 0)

	if isFullEllipse {
		r.pdf.Ellipse(px, py, rx, ry, -rotDeg, "D")
	} else {
		startDeg := startAngleRad * 180 / math.Pi
		endDeg := endAngleRad * 180 / math.Pi
		pdfStart := math.Mod(startDeg, 360)
		if pdfStart < 0 {
			pdfStart += 360
		}
		pdfEnd := math.Mod(endDeg, 360)
		if pdfEnd < 0 {
			pdfEnd += 360
		}
		if pdfEnd <= pdfStart {
			pdfEnd += 360
		}
		r.pdf.Arc(px, py, rx, ry, -rotDeg, pdfStart, pdfEnd, "D")
	}
}

func (r *Renderer) DrawPolyline(points [][2]float64, closed bool) {
	if len(points) < 2 {
		return
	}
	for i := 1; i < len(points); i++ {
		r.pdf.Line(
			r.transform.X(points[i-1][0]), r.transform.Y(points[i-1][1]),
			r.transform.X(points[i][0]), r.transform.Y(points[i][1]),
		)
	}
	if closed && len(points) > 2 {
		last := points[len(points)-1]
		first := points[0]
		r.pdf.Line(
			r.transform.X(last[0]), r.transform.Y(last[1]),
			r.transform.X(first[0]), r.transform.Y(first[1]),
		)
	}
}

// DrawBulgeArc draws the arc segment between two polyline vertices connected by a bulge value.
func (r *Renderer) DrawBulgeArc(x1, y1, x2, y2, bulge float64) {
	if math.Abs(bulge) < 1e-10 {
		r.DrawLine(x1, y1, x2, y2)
		return
	}

	// Calculate arc from bulge
	dx := x2 - x1
	dy := y2 - y1
	chordLen := math.Sqrt(dx*dx + dy*dy)
	sagitta := math.Abs(bulge) * chordLen / 2
	radius := (chordLen*chordLen/4 + sagitta*sagitta) / (2 * sagitta)

	// Center of chord
	mx := (x1 + x2) / 2
	my := (y1 + y2) / 2

	// Perpendicular direction
	px := -dy / chordLen
	py := dx / chordLen

	// Distance from chord midpoint to arc center
	d := radius - sagitta
	if bulge < 0 {
		d = -d
	}

	cx := mx + px*d
	cy := my + py*d

	// Start and end angles
	startAngle := math.Atan2(y1-cy, x1-cx) * 180 / math.Pi
	endAngle := math.Atan2(y2-cy, x2-cx) * 180 / math.Pi

	if bulge > 0 {
		// Counter-clockwise
		if endAngle < startAngle {
			endAngle += 360
		}
	} else {
		// Clockwise: swap
		startAngle, endAngle = endAngle, startAngle
		if endAngle < startAngle {
			endAngle += 360
		}
	}

	r.DrawArc(cx, cy, radius, startAngle, endAngle)
}

func (r *Renderer) DrawSolid(x1, y1, x2, y2, x3, y3, x4, y4 float64) {
	pts := []fpdf.PointType{
		{X: r.transform.X(x1), Y: r.transform.Y(y1)},
		{X: r.transform.X(x2), Y: r.transform.Y(y2)},
		// DXF SOLID has swapped 3rd and 4th corners
		{X: r.transform.X(x4), Y: r.transform.Y(y4)},
		{X: r.transform.X(x3), Y: r.transform.Y(y3)},
	}
	r.pdf.Polygon(pts, "F")
}

func (r *Renderer) DrawPoint(x, y float64) {
	px := r.transform.X(x)
	py := r.transform.Y(y)
	r.pdf.Circle(px, py, 0.2, "F")
}

func (r *Renderer) DrawText(x, y float64, text string, heightMM, rotationDeg float64) {
	px := r.transform.X(x)
	py := r.transform.Y(y)
	scaledH := r.transform.Dist(heightMM)

	if scaledH < 0.5 {
		scaledH = 0.5
	}

	r.pdf.SetFont("DejaVu", "", scaledH*2.83465) // mm to points
	r.pdf.SetTextColor(0, 0, 0)

	if math.Abs(rotationDeg) > 0.01 {
		r.pdf.TransformBegin()
		r.pdf.TransformRotate(-rotationDeg, px, py)
		r.pdf.Text(px, py, text)
		r.pdf.TransformEnd()
	} else {
		r.pdf.Text(px, py, text)
	}
}

// DrawMText renders parsed MText segments with per-segment styling.
// Supports: color changes, height changes, underline, multi-line (\P),
// bold/italic (font style), and stacking (rendered inline as num/denom).
func (r *Renderer) DrawMText(x, y float64, segments []MTextSegment, defaultHeightMM, rotationDeg float64) {
	px := r.transform.X(x)
	py := r.transform.Y(y)
	defaultScaledH := r.transform.Dist(defaultHeightMM)
	if defaultScaledH < 0.5 {
		defaultScaledH = 0.5
	}

	if math.Abs(rotationDeg) > 0.01 {
		r.pdf.TransformBegin()
		r.pdf.TransformRotate(-rotationDeg, px, py)
	}

	curX := px
	curY := py
	lineHeight := defaultScaledH * 1.4 // line spacing

	for _, seg := range segments {
		if seg.NewLine {
			curX = px
			curY += lineHeight
			continue
		}
		if seg.Text == "" {
			continue
		}

		// Determine height for this segment
		scaledH := defaultScaledH
		if seg.Style.HeightRelative > 0 {
			scaledH = defaultScaledH * seg.Style.HeightRelative
		} else if seg.Style.Height > 0 {
			scaledH = r.transform.Dist(seg.Style.Height)
		}
		if scaledH < 0.5 {
			scaledH = 0.5
		}

		// Font style
		fontStyle := ""
		if seg.Style.Bold {
			fontStyle += "B"
		}
		if seg.Style.Italic {
			fontStyle += "I"
		}

		ptSize := scaledH * 2.83465 // mm to points
		r.pdf.SetFont("DejaVu", fontStyle, ptSize)

		// Color
		r.pdf.SetTextColor(seg.Style.ColorR, seg.Style.ColorG, seg.Style.ColorB)

		// Render text
		r.pdf.Text(curX, curY, seg.Text)

		// Underline: draw a line under the text
		if seg.Style.Underline {
			textW := r.pdf.GetStringWidth(seg.Text)
			r.pdf.SetDrawColor(seg.Style.ColorR, seg.Style.ColorG, seg.Style.ColorB)
			r.pdf.SetLineWidth(scaledH * 0.05)
			underY := curY + scaledH*0.15
			r.pdf.Line(curX, underY, curX+textW, underY)
		}

		// Strikethrough: draw a line through the middle
		if seg.Style.Strikethrough {
			textW := r.pdf.GetStringWidth(seg.Text)
			r.pdf.SetDrawColor(seg.Style.ColorR, seg.Style.ColorG, seg.Style.ColorB)
			r.pdf.SetLineWidth(scaledH * 0.05)
			strikeY := curY - scaledH*0.25
			r.pdf.Line(curX, strikeY, curX+textW, strikeY)
		}

		// Overstrike: draw a line above the text
		if seg.Style.Overstrike {
			textW := r.pdf.GetStringWidth(seg.Text)
			r.pdf.SetDrawColor(seg.Style.ColorR, seg.Style.ColorG, seg.Style.ColorB)
			r.pdf.SetLineWidth(scaledH * 0.05)
			overY := curY - scaledH*0.7
			r.pdf.Line(curX, overY, curX+textW, overY)
		}

		// Advance cursor
		curX += r.pdf.GetStringWidth(seg.Text)
	}

	if math.Abs(rotationDeg) > 0.01 {
		r.pdf.TransformEnd()
	}
}

func (r *Renderer) DrawSpline(controlPoints [][2]float64, degree int, knots []float64) {
	// Approximate spline by evaluating points along the curve
	if len(controlPoints) < 2 {
		return
	}

	// Simple fallback: connect control points as polyline for now
	// TODO: implement proper B-spline to bezier conversion
	numSamples := len(controlPoints) * 10
	points := evaluateBSpline(controlPoints, degree, knots, numSamples)

	for i := 1; i < len(points); i++ {
		r.pdf.Line(
			r.transform.X(points[i-1][0]), r.transform.Y(points[i-1][1]),
			r.transform.X(points[i][0]), r.transform.Y(points[i][1]),
		)
	}
}

// DrawDebugBBox draws a red dashed rectangle around the drawing bounding box.
func (r *Renderer) DrawDebugBBox(bbox BBox) {
	r.pdf.SetDrawColor(255, 0, 0)
	r.pdf.SetLineWidth(0.3)

	x1 := r.transform.X(bbox.MinX)
	y1 := r.transform.Y(bbox.MaxY) // top of drawing (DXF MaxY = PDF top)
	x2 := r.transform.X(bbox.MaxX)
	y2 := r.transform.Y(bbox.MinY) // bottom of drawing

	// Draw rectangle
	r.pdf.Line(x1, y1, x2, y1) // top
	r.pdf.Line(x2, y1, x2, y2) // right
	r.pdf.Line(x2, y2, x1, y2) // bottom
	r.pdf.Line(x1, y2, x1, y1) // left

	// Draw diagonals to make it obvious
	r.pdf.Line(x1, y1, x2, y2)
	r.pdf.Line(x1, y2, x2, y1)
}

func (r *Renderer) AddPage() {
	r.pdf.AddPage()
}

func (r *Renderer) SetClipRect(x, y, w, h float64) {
	r.pdf.ClipRect(x, y, w, h, false)
}

func (r *Renderer) ClipEnd() {
	r.pdf.ClipEnd()
}

func (r *Renderer) Save(path string) error {
	return r.pdf.OutputFileAndClose(path)
}

func (r *Renderer) Pdf() *fpdf.Fpdf {
	return r.pdf
}

// evaluateBSpline evaluates a B-spline curve at numSamples points.
func evaluateBSpline(controlPoints [][2]float64, degree int, knots []float64, numSamples int) [][2]float64 {
	n := len(controlPoints) - 1
	p := degree

	if len(knots) < n+p+2 {
		// Fallback: just return control points
		return controlPoints
	}

	tMin := knots[p]
	tMax := knots[n+1]

	points := make([][2]float64, 0, numSamples)
	for i := 0; i < numSamples; i++ {
		t := tMin + (tMax-tMin)*float64(i)/float64(numSamples-1)
		x, y := deBoor(controlPoints, p, knots, t)
		points = append(points, [2]float64{x, y})
	}
	return points
}

// deBoor evaluates a B-spline at parameter t using De Boor's algorithm.
func deBoor(controlPoints [][2]float64, p int, knots []float64, t float64) (float64, float64) {
	n := len(controlPoints)

	// Find knot span
	k := p
	for k < n && k+1 < len(knots) && knots[k+1] <= t {
		k++
	}
	if k >= n {
		k = n - 1
	}

	// Copy relevant control points
	d := make([][2]float64, p+1)
	for j := 0; j <= p; j++ {
		idx := k - p + j
		if idx < 0 {
			idx = 0
		}
		if idx >= n {
			idx = n - 1
		}
		d[j] = controlPoints[idx]
	}

	for r := 1; r <= p; r++ {
		for j := p; j >= r; j-- {
			kj := k - p + j
			denom := knots[kj+p-r+1] - knots[kj]
			if math.Abs(denom) < 1e-10 {
				continue
			}
			alpha := (t - knots[kj]) / denom
			d[j][0] = (1-alpha)*d[j-1][0] + alpha*d[j][0]
			d[j][1] = (1-alpha)*d[j-1][1] + alpha*d[j][1]
		}
	}

	return d[p][0], d[p][1]
}
