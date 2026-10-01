package converter

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers"
	"github.com/tdewolff/canvas/renderers/pdf"
)

type Renderer struct {
	c          *canvas.Canvas
	ctx        *canvas.Context
	pages      []*canvas.Canvas // completed pages (for multi-page/tiling)
	transform  Transform
	paper      PaperSize
	landscape  bool
	margin     float64
	fontFamily *canvas.FontFamily
	pageW      float64
	pageH      float64
	color      color.RGBA // current entity color (used for text)
	capRatio   float64    // font cap height / em size, measured lazily
}

// DefaultFontDir returns the default font directory.
func DefaultFontDir() string {
	if runtime.GOOS == "windows" {
		exe, err := os.Executable()
		if err == nil {
			return filepath.Dir(exe)
		}
		return "."
	}
	return "/usr/share/fonts/truetype/dejavu"
}

func NewRenderer(paper PaperSize, landscape bool, margin float64, fontDir string) *Renderer {
	w, h := paper.Width, paper.Height
	if landscape {
		w, h = h, w
	}

	c := canvas.New(w, h)
	ctx := canvas.NewContext(c)
	ctx.SetCoordSystem(canvas.CartesianIV) // Y-down like fpdf

	if fontDir == "" {
		fontDir = DefaultFontDir()
	}
	family := canvas.NewFontFamily("DejaVu")
	family.LoadFontFile(filepath.Join(fontDir, "DejaVuSans.ttf"), canvas.FontRegular)
	family.LoadFontFile(filepath.Join(fontDir, "DejaVuSans-Bold.ttf"), canvas.FontBold)
	family.LoadFontFile(filepath.Join(fontDir, "DejaVuSans-Oblique.ttf"), canvas.FontItalic)
	family.LoadFontFile(filepath.Join(fontDir, "DejaVuSans-BoldOblique.ttf"), canvas.FontBold|canvas.FontItalic)

	return &Renderer{
		c:          c,
		ctx:        ctx,
		paper:      paper,
		landscape:  landscape,
		margin:     margin,
		fontFamily: family,
		pageW:      w,
		pageH:      h,
	}
}

func (r *Renderer) SetTransform(t Transform) {
	r.transform = t
}

func (r *Renderer) SetStyle(col RGB, lineWidthMM float64) {
	rgba := color.RGBA{col.R, col.G, col.B, 255}
	r.color = rgba
	r.ctx.SetStrokeColor(rgba)
	r.ctx.SetStrokeWidth(lineWidthMM)
	r.ctx.SetFillColor(color.RGBA{0, 0, 0, 0}) // transparent fill by default
}

func (r *Renderer) SetFillColor(col RGB) {
	r.ctx.SetFillColor(color.RGBA{col.R, col.G, col.B, 255})
}

func (r *Renderer) DrawLine(x1, y1, x2, y2 float64) {
	px1 := r.transform.X(x1)
	py1 := r.transform.Y(y1)
	px2 := r.transform.X(x2)
	py2 := r.transform.Y(y2)
	r.drawLine(px1, py1, px2, py2)
}

// drawLine draws a line in page coordinates (used by DrawLine and crop marks).
func (r *Renderer) drawLine(x1, y1, x2, y2 float64) {
	p := &canvas.Path{}
	p.MoveTo(x1, y1)
	p.LineTo(x2, y2)
	r.ctx.DrawPath(0, 0, p)
}

// DrawEllipticArc draws the curve c + u·cos(t) + v·sin(t) for t from t0 to t1
// (radians; t1 < t0 runs clockwise), in DXF world coordinates. u and v are
// conjugate semi-axes, so circles, arcs, ellipses and their images under any
// affine INSERT transform (mirrored, rotated, non-uniformly scaled) all go
// through here.
func (r *Renderer) DrawEllipticArc(cx, cy, ux, uy, vx, vy, t0, t1 float64) {
	sweep := t1 - t0
	numSegs := max(int(math.Ceil(math.Abs(sweep)/(2*math.Pi/180))), 4) // ~2° per segment

	p := &canvas.Path{}
	for i, pt := range ellipticArcPoints(cx, cy, ux, uy, vx, vy, t0, t1, numSegs) {
		px, py := r.transform.X(pt[0]), r.transform.Y(pt[1])
		if i == 0 {
			p.MoveTo(px, py)
		} else {
			p.LineTo(px, py)
		}
	}
	if math.Abs(sweep) >= 2*math.Pi-1e-9 {
		p.Close()
	}
	r.ctx.DrawPath(0, 0, p)
}

func (r *Renderer) DrawPolyline(points [][2]float64, closed bool) {
	if len(points) < 2 {
		return
	}
	p := &canvas.Path{}
	p.MoveTo(r.transform.X(points[0][0]), r.transform.Y(points[0][1]))
	for i := 1; i < len(points); i++ {
		p.LineTo(r.transform.X(points[i][0]), r.transform.Y(points[i][1]))
	}
	if closed && len(points) > 2 {
		p.Close()
	}
	r.ctx.DrawPath(0, 0, p)
}

func (r *Renderer) DrawSolid(x1, y1, x2, y2, x3, y3, x4, y4 float64) {
	p := &canvas.Path{}
	p.MoveTo(r.transform.X(x1), r.transform.Y(y1))
	p.LineTo(r.transform.X(x2), r.transform.Y(y2))
	// DXF SOLID has swapped 3rd and 4th corners
	p.LineTo(r.transform.X(x4), r.transform.Y(y4))
	p.LineTo(r.transform.X(x3), r.transform.Y(y3))
	p.Close()
	r.ctx.DrawPath(0, 0, p)
}

// FillPolygons fills a set of closed loops (DXF world coordinates) as one
// shape with the even-odd rule, so inner loops become holes.
func (r *Renderer) FillPolygons(polys [][][2]float64, col RGB) {
	p := &canvas.Path{}
	for _, poly := range polys {
		if len(poly) < 3 {
			continue
		}
		p.MoveTo(r.transform.X(poly[0][0]), r.transform.Y(poly[0][1]))
		for _, v := range poly[1:] {
			p.LineTo(r.transform.X(v[0]), r.transform.Y(v[1]))
		}
		p.Close()
	}
	if p.Empty() {
		return
	}
	r.ctx.Push()
	r.ctx.SetFillColor(color.RGBA{col.R, col.G, col.B, 255})
	r.ctx.SetStrokeColor(color.RGBA{0, 0, 0, 0})
	r.ctx.SetFillRule(canvas.EvenOdd)
	r.ctx.DrawPath(0, 0, p)
	r.ctx.Pop()
}

func (r *Renderer) DrawPoint(x, y float64) {
	px := r.transform.X(x)
	py := r.transform.Y(y)
	p := canvas.Circle(0.2)
	// Circle path is centered around (rx, ry) with radius 0.2
	r.ctx.Push()
	fillSave := r.ctx.Style.Fill
	r.ctx.SetFillColor(r.ctx.Style.Stroke.Color)
	r.ctx.DrawPath(px-0.2, py-0.2, p)
	r.ctx.Style.Fill = fillSave
	r.ctx.Pop()
}

// textVAlign is the vertical anchor of a text line relative to its insertion
// point (DXF TEXT vertical justification).
type textVAlign int

const (
	vAlignBaseline textVAlign = iota
	vAlignBottom
	vAlignMiddle
	vAlignTop
)

// minTextHeightMM keeps tiny annotation text legible on paper.
const minTextHeightMM = 0.5

// textFace returns a font face whose cap height is heightMM. CAD text height
// is the height of capital letters, not the em size.
func (r *Renderer) textFace(heightMM float64, col color.RGBA, style canvas.FontStyle) *canvas.FontFace {
	if r.capRatio == 0 {
		ref := r.fontFamily.Face(100, canvas.Black, canvas.FontRegular, canvas.FontNormal)
		r.capRatio = 0.729 // DejaVu Sans fallback
		if m := ref.Metrics(); m.CapHeight > 0 && m.LineHeight > 0 {
			r.capRatio = m.CapHeight / (100 / 2.83465)
		}
	}
	ptSize := heightMM / r.capRatio * 2.83465 // mm to points
	return r.fontFamily.Face(ptSize, col, style, canvas.FontNormal)
}

// baselineShift returns how far (in page mm, Y-down) the baseline sits below
// the anchor point for the given vertical alignment.
func baselineShift(capHeight, descent float64, v textVAlign) float64 {
	switch v {
	case vAlignBottom:
		return -descent
	case vAlignMiddle:
		return capHeight / 2
	case vAlignTop:
		return capHeight
	default:
		return 0
	}
}

// DrawText draws a single-line text. hAlign is 0 (left), 0.5 (center) or 1
// (right) of the text width; vAlign selects the vertical anchor.
func (r *Renderer) DrawText(x, y float64, text string, heightMM, rotationDeg, hAlign float64, vAlign textVAlign) {
	if text == "" {
		return
	}
	px := r.transform.X(x)
	py := r.transform.Y(y)
	scaledH := max(r.transform.Dist(heightMM), minTextHeightMM)

	face := r.textFace(scaledH, r.color, canvas.FontRegular)
	textLine := canvas.NewTextLine(face, text, canvas.Left)
	dx := -hAlign * textLine.Bounds().W()
	dy := baselineShift(scaledH, face.Metrics().Descent, vAlign)

	if math.Abs(rotationDeg) > 0.01 {
		r.ctx.Push()
		// DXF angles are CCW in a Y-up world; the page is Y-down, so negate.
		r.ctx.ComposeView(canvas.Identity.RotateAbout(-rotationDeg, px, py))
		r.ctx.DrawText(px+dx, py+dy, textLine)
		r.ctx.Pop()
	} else {
		r.ctx.DrawText(px+dx, py+dy, textLine)
	}
}

// mtextItem is one laid-out MText segment.
type mtextItem struct {
	seg   MTextSegment
	line  *canvas.Text
	width float64
	h     float64 // cap height in page mm
	col   color.RGBA
}

// DrawMText draws multi-line formatted text. attach is the DXF attachment
// point (1..9: top/middle/bottom × left/center/right); lineSpacing is the
// MTEXT line spacing factor (0 = default 1.0).
func (r *Renderer) DrawMText(x, y float64, segments []MTextSegment, defaultHeightMM, rotationDeg float64, attach int, lineSpacing float64) {
	px := r.transform.X(x)
	py := r.transform.Y(y)
	defaultScaledH := max(r.transform.Dist(defaultHeightMM), minTextHeightMM)
	if lineSpacing <= 0 {
		lineSpacing = 1
	}

	// Pass 1: lay out segments into lines and measure them.
	lines := [][]mtextItem{nil}
	for _, seg := range segments {
		if seg.NewLine {
			lines = append(lines, nil)
			continue
		}
		if seg.Text == "" {
			continue
		}

		scaledH := defaultScaledH
		if seg.Style.HeightRelative > 0 {
			scaledH = defaultScaledH * seg.Style.HeightRelative
		} else if seg.Style.Height > 0 {
			scaledH = r.transform.Dist(seg.Style.Height)
		}
		scaledH = max(scaledH, minTextHeightMM)

		var fontStyle canvas.FontStyle
		if seg.Style.Bold && seg.Style.Italic {
			fontStyle = canvas.FontBold | canvas.FontItalic
		} else if seg.Style.Bold {
			fontStyle = canvas.FontBold
		} else if seg.Style.Italic {
			fontStyle = canvas.FontItalic
		} else {
			fontStyle = canvas.FontRegular
		}

		textColor := r.color
		if seg.Style.HasColor {
			textColor = color.RGBA{uint8(seg.Style.ColorR), uint8(seg.Style.ColorG), uint8(seg.Style.ColorB), 255}
		}
		face := r.textFace(scaledH, textColor, fontStyle)
		textLine := canvas.NewTextLine(face, seg.Text, canvas.Left)
		last := len(lines) - 1
		lines[last] = append(lines[last], mtextItem{
			seg: seg, line: textLine, width: textLine.Bounds().W(), h: scaledH, col: textColor,
		})
	}

	// Pass 2: anchor the block according to the attachment point. AutoCAD's
	// default line pitch is 5/3 of the text height.
	if attach < 1 || attach > 9 {
		attach = 1
	}
	hAlign := float64((attach-1)%3) / 2
	advance := defaultScaledH * 5.0 / 3.0 * lineSpacing
	n := float64(len(lines))
	var firstBaseline float64
	switch (attach - 1) / 3 {
	case 0: // top
		firstBaseline = py + defaultScaledH
	case 1: // middle
		total := defaultScaledH + (n-1)*advance
		firstBaseline = py - total/2 + defaultScaledH
	default: // bottom
		firstBaseline = py - (n-1)*advance
	}

	if math.Abs(rotationDeg) > 0.01 {
		r.ctx.Push()
		r.ctx.ComposeView(canvas.Identity.RotateAbout(-rotationDeg, px, py))
	}

	for li, items := range lines {
		lineW := 0.0
		for _, it := range items {
			lineW += it.width
		}
		curX := px - hAlign*lineW
		curY := firstBaseline + float64(li)*advance

		for _, it := range items {
			r.ctx.DrawText(curX, curY, it.line)

			// Underline / strikethrough / overstrike decorations
			for _, deco := range []struct {
				on bool
				dy float64
			}{
				{it.seg.Style.Underline, it.h * 0.2},
				{it.seg.Style.Strikethrough, -it.h * 0.35},
				{it.seg.Style.Overstrike, -it.h * 1.15},
			} {
				if !deco.on {
					continue
				}
				r.ctx.Push()
				r.ctx.SetStrokeColor(it.col)
				r.ctx.SetStrokeWidth(it.h * 0.07)
				r.ctx.SetFillColor(color.RGBA{0, 0, 0, 0})
				r.drawLine(curX, curY+deco.dy, curX+it.width, curY+deco.dy)
				r.ctx.Pop()
			}

			curX += it.width
		}
	}

	if math.Abs(rotationDeg) > 0.01 {
		r.ctx.Pop()
	}
}

func (r *Renderer) DrawSpline(controlPoints [][2]float64, degree int, knots []float64) {
	if len(controlPoints) < 2 {
		return
	}

	numSamples := len(controlPoints) * 10
	points := evaluateBSpline(controlPoints, degree, knots, numSamples)

	p := &canvas.Path{}
	p.MoveTo(r.transform.X(points[0][0]), r.transform.Y(points[0][1]))
	for i := 1; i < len(points); i++ {
		p.LineTo(r.transform.X(points[i][0]), r.transform.Y(points[i][1]))
	}
	r.ctx.DrawPath(0, 0, p)
}

func (r *Renderer) DrawDebugBBox(bbox BBox) {
	r.ctx.Push()
	r.ctx.SetStrokeColor(color.RGBA{255, 0, 0, 255})
	r.ctx.SetStrokeWidth(0.3)
	r.ctx.SetFillColor(color.RGBA{0, 0, 0, 0})

	x1 := r.transform.X(bbox.MinX)
	y1 := r.transform.Y(bbox.MaxY)
	x2 := r.transform.X(bbox.MaxX)
	y2 := r.transform.Y(bbox.MinY)

	r.drawLine(x1, y1, x2, y1) // top
	r.drawLine(x2, y1, x2, y2) // right
	r.drawLine(x2, y2, x1, y2) // bottom
	r.drawLine(x1, y2, x1, y1) // left
	r.drawLine(x1, y1, x2, y2) // diagonal
	r.drawLine(x1, y2, x2, y1) // diagonal

	r.ctx.Pop()
}

func (r *Renderer) AddPage() {
	r.pages = append(r.pages, r.c)
	c := canvas.New(r.pageW, r.pageH)
	r.c = c
	r.ctx = canvas.NewContext(c)
	r.ctx.SetCoordSystem(canvas.CartesianIV)
}

func (r *Renderer) SetClipRect(x, y, w, h float64) {
	// Canvas doesn't have a direct clip rect on context, so we use Push/Pop
	// and will rely on the drawing being within bounds.
	// For proper clipping, we'd need to intersect paths, but for tiling
	// the content is already positioned to fit within the tile.
	r.ctx.Push()
}

func (r *Renderer) ClipEnd() {
	r.ctx.Pop()
}

func (r *Renderer) Save(path string, format string, dpi float64, transparent bool) error {
	if format == "" {
		format = formatFromExtension(path)
	}

	allPages := make([]*canvas.Canvas, 0, len(r.pages)+1)
	allPages = append(allPages, r.pages...)
	allPages = append(allPages, r.c)

	switch format {
	case "pdf":
		return r.savePDF(path, allPages)
	case "png":
		return r.saveRaster(path, allPages, dpi, transparent, "png")
	case "jpg", "jpeg":
		return r.saveRaster(path, allPages, dpi, false, "jpg")
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func (r *Renderer) savePDF(path string, pages []*canvas.Canvas) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	opts := pdf.DefaultOptions
	p := pdf.New(f, pages[0].W, pages[0].H, &opts)
	pages[0].RenderTo(p)

	for i := 1; i < len(pages); i++ {
		p.NewPage(pages[i].W, pages[i].H)
		pages[i].RenderTo(p)
	}

	return p.Close()
}

func (r *Renderer) saveRaster(path string, pages []*canvas.Canvas, dpi float64, transparent bool, format string) error {
	if dpi <= 0 {
		dpi = 300
	}
	res := canvas.DPI(dpi)

	for i, c := range pages {
		pagePath := path
		if len(pages) > 1 {
			ext := filepath.Ext(path)
			base := strings.TrimSuffix(path, ext)
			pagePath = fmt.Sprintf("%s_%d%s", base, i+1, ext)
		}

		if !transparent {
			// Create new canvas with white background, then render drawing on top
			bgCanvas := canvas.New(c.W, c.H)
			bgCtx := canvas.NewContext(bgCanvas)
			bgCtx.SetFillColor(color.RGBA{255, 255, 255, 255})
			bgCtx.SetStrokeColor(color.RGBA{0, 0, 0, 0})
			bgCtx.DrawPath(0, 0, canvas.Rectangle(c.W, c.H))
			c.RenderTo(bgCanvas)
			c = bgCanvas
		}

		var writer canvas.Writer
		switch format {
		case "png":
			writer = renderers.PNG(res)
		case "jpg":
			writer = renderers.JPEG(res)
		}

		if err := c.WriteFile(pagePath, writer); err != nil {
			return err
		}
	}
	return nil
}

func formatFromExtension(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "png"
	case ".jpg", ".jpeg":
		return "jpg"
	case ".pdf":
		return "pdf"
	default:
		return "pdf"
	}
}

// DrawRawLine draws a line in page coordinates (no transform). Used by crop marks.
func (r *Renderer) DrawRawLine(x1, y1, x2, y2 float64) {
	r.drawLine(x1, y1, x2, y2)
}

// SetRawStyle sets stroke color and width for page-coordinate drawing.
func (r *Renderer) SetRawStyle(col RGB, lineWidthMM float64) {
	rgba := color.RGBA{col.R, col.G, col.B, 255}
	r.ctx.SetStrokeColor(rgba)
	r.ctx.SetStrokeWidth(lineWidthMM)
	r.ctx.SetFillColor(color.RGBA{0, 0, 0, 0})
}

// evaluateBSpline evaluates a B-spline curve at numSamples points.
func evaluateBSpline(controlPoints [][2]float64, degree int, knots []float64, numSamples int) [][2]float64 {
	n := len(controlPoints) - 1
	p := degree

	if len(knots) < n+p+2 {
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

	k := p
	for k < n && k+1 < len(knots) && knots[k+1] <= t {
		k++
	}
	if k >= n {
		k = n - 1
	}

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

