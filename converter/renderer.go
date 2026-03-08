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

func (r *Renderer) DrawCircle(cx, cy, radius float64) {
	// Use arc sampling for consistency (avoids canvas Circle positioning issues with CartesianIV)
	r.DrawArc(cx, cy, radius, 0, 360)
}

func (r *Renderer) DrawArc(cx, cy, radius, startAngleDeg, endAngleDeg float64) {
	// Sample arc into line segments in DXF coordinates, then transform each point.
	// This avoids all angle/positioning complexity with canvas's arc API.
	sweep := endAngleDeg - startAngleDeg
	numSegs := int(math.Ceil(math.Abs(sweep) / 2.0)) // ~2° per segment
	if numSegs < 4 {
		numSegs = 4
	}

	p := &canvas.Path{}
	for i := 0; i <= numSegs; i++ {
		t := float64(i) / float64(numSegs)
		angleDeg := startAngleDeg + t*sweep
		angleRad := angleDeg * math.Pi / 180
		// Point on arc in DXF coordinates
		dxfX := cx + radius*math.Cos(angleRad)
		dxfY := cy + radius*math.Sin(angleRad)
		px := r.transform.X(dxfX)
		py := r.transform.Y(dxfY)
		if i == 0 {
			p.MoveTo(px, py)
		} else {
			p.LineTo(px, py)
		}
	}
	r.ctx.DrawPath(0, 0, p)
}

func (r *Renderer) DrawEllipse(cx, cy, majorX, majorY, minorRatio, startAngleRad, endAngleRad float64) {
	majorLen := math.Sqrt(majorX*majorX + majorY*majorY)
	minorLen := majorLen * minorRatio
	rotRad := math.Atan2(majorY, majorX)

	isFullEllipse := math.Abs(endAngleRad-startAngleRad-2*math.Pi) < 0.001 ||
		(startAngleRad == 0 && endAngleRad == 0)

	startA := startAngleRad
	endA := endAngleRad
	if isFullEllipse {
		startA = 0
		endA = 2 * math.Pi
	}

	// Sample ellipse arc in DXF coordinates, then transform each point.
	sweep := endA - startA
	numSegs := int(math.Ceil(math.Abs(sweep) / (2.0 * math.Pi / 180))) // ~2° per segment
	if numSegs < 4 {
		numSegs = 4
	}

	cosR := math.Cos(rotRad)
	sinR := math.Sin(rotRad)

	p := &canvas.Path{}
	for i := 0; i <= numSegs; i++ {
		t := float64(i) / float64(numSegs)
		angle := startA + t*sweep
		// Point on unrotated ellipse
		ex := majorLen * math.Cos(angle)
		ey := minorLen * math.Sin(angle)
		// Rotate by ellipse rotation
		dxfX := cx + ex*cosR - ey*sinR
		dxfY := cy + ex*sinR + ey*cosR
		px := r.transform.X(dxfX)
		py := r.transform.Y(dxfY)
		if i == 0 {
			p.MoveTo(px, py)
		} else {
			p.LineTo(px, py)
		}
	}
	if isFullEllipse {
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

func (r *Renderer) DrawBulgeArc(x1, y1, x2, y2, bulge float64) {
	if math.Abs(bulge) < 1e-10 {
		r.DrawLine(x1, y1, x2, y2)
		return
	}

	dx := x2 - x1
	dy := y2 - y1
	chordLen := math.Sqrt(dx*dx + dy*dy)
	sagitta := math.Abs(bulge) * chordLen / 2
	radius := (chordLen*chordLen/4 + sagitta*sagitta) / (2 * sagitta)

	mx := (x1 + x2) / 2
	my := (y1 + y2) / 2

	px := -dy / chordLen
	py := dx / chordLen

	d := radius - sagitta
	if bulge < 0 {
		d = -d
	}

	cx := mx + px*d
	cy := my + py*d

	startAngle := math.Atan2(y1-cy, x1-cx) * 180 / math.Pi
	endAngle := math.Atan2(y2-cy, x2-cx) * 180 / math.Pi

	if bulge > 0 {
		if endAngle < startAngle {
			endAngle += 360
		}
	} else {
		startAngle, endAngle = endAngle, startAngle
		if endAngle < startAngle {
			endAngle += 360
		}
	}

	r.DrawArc(cx, cy, radius, startAngle, endAngle)
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

func (r *Renderer) DrawText(x, y float64, text string, heightMM, rotationDeg float64) {
	px := r.transform.X(x)
	py := r.transform.Y(y)
	scaledH := r.transform.Dist(heightMM)

	if scaledH < 0.5 {
		scaledH = 0.5
	}

	ptSize := scaledH * 2.83465 // mm to points
	face := r.fontFamily.Face(ptSize, canvas.Black, canvas.FontRegular, canvas.FontNormal)
	textLine := canvas.NewTextLine(face, text, canvas.Left)

	if math.Abs(rotationDeg) > 0.01 {
		r.ctx.Push()
		r.ctx.ComposeView(canvas.Identity.RotateAbout(rotationDeg, px, py))
		r.ctx.DrawText(px, py, textLine)
		r.ctx.Pop()
	} else {
		r.ctx.DrawText(px, py, textLine)
	}
}

func (r *Renderer) DrawMText(x, y float64, segments []MTextSegment, defaultHeightMM, rotationDeg float64) {
	px := r.transform.X(x)
	py := r.transform.Y(y)
	defaultScaledH := r.transform.Dist(defaultHeightMM)
	if defaultScaledH < 0.5 {
		defaultScaledH = 0.5
	}

	if math.Abs(rotationDeg) > 0.01 {
		r.ctx.Push()
		r.ctx.ComposeView(canvas.Identity.RotateAbout(rotationDeg, px, py))
	}

	curX := px
	curY := py
	lineHeight := defaultScaledH * 1.4

	for _, seg := range segments {
		if seg.NewLine {
			curX = px
			curY += lineHeight
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
		if scaledH < 0.5 {
			scaledH = 0.5
		}

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

		ptSize := scaledH * 2.83465
		textColor := color.RGBA{uint8(seg.Style.ColorR), uint8(seg.Style.ColorG), uint8(seg.Style.ColorB), 255}
		face := r.fontFamily.Face(ptSize, textColor, fontStyle, canvas.FontNormal)
		textLine := canvas.NewTextLine(face, seg.Text, canvas.Left)

		r.ctx.DrawText(curX, curY, textLine)

		// Underline
		if seg.Style.Underline {
			textW := textLine.Bounds().W()
			lineColor := color.RGBA{uint8(seg.Style.ColorR), uint8(seg.Style.ColorG), uint8(seg.Style.ColorB), 255}
			r.ctx.Push()
			r.ctx.SetStrokeColor(lineColor)
			r.ctx.SetStrokeWidth(scaledH * 0.05)
			r.ctx.SetFillColor(color.RGBA{0, 0, 0, 0})
			underY := curY + scaledH*0.15
			r.drawLine(curX, underY, curX+textW, underY)
			r.ctx.Pop()
		}

		// Strikethrough
		if seg.Style.Strikethrough {
			textW := textLine.Bounds().W()
			lineColor := color.RGBA{uint8(seg.Style.ColorR), uint8(seg.Style.ColorG), uint8(seg.Style.ColorB), 255}
			r.ctx.Push()
			r.ctx.SetStrokeColor(lineColor)
			r.ctx.SetStrokeWidth(scaledH * 0.05)
			r.ctx.SetFillColor(color.RGBA{0, 0, 0, 0})
			strikeY := curY - scaledH*0.25
			r.drawLine(curX, strikeY, curX+textW, strikeY)
			r.ctx.Pop()
		}

		// Overstrike
		if seg.Style.Overstrike {
			textW := textLine.Bounds().W()
			lineColor := color.RGBA{uint8(seg.Style.ColorR), uint8(seg.Style.ColorG), uint8(seg.Style.ColorB), 255}
			r.ctx.Push()
			r.ctx.SetStrokeColor(lineColor)
			r.ctx.SetStrokeWidth(scaledH * 0.05)
			r.ctx.SetFillColor(color.RGBA{0, 0, 0, 0})
			overY := curY - scaledH*0.7
			r.drawLine(curX, overY, curX+textW, overY)
			r.ctx.Pop()
		}

		curX += textLine.Bounds().W()
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

