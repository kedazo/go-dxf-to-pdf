package converter

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	dxf "github.com/kedazo/dxf-go"
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
	fonts      *fontLib
	textStyles map[string]textStyle // STYLE table by upper-cased name
	pageW      float64
	pageH      float64
	color      color.RGBA // current entity color (used for text)
	clip       *canvas.Rect // page-space clip rectangle (tiled output), nil = none

	// Stroke batching (PDF only, see SetBatching): consecutive strokes with
	// the same style are collected into one path and drawn in a single call.
	// Writing one PDF path per segment dominated the cost on large drawings.
	batch        bool
	pending      *canvas.Path // batched strokes, page coordinates
	pendingSegs  int
	penX, penY   float64 // end point of the last batched segment
	styleSet     bool    // ctx stroke style matches strokeCol/strokeW
	fillSet      bool    // ctx fill is opaque (SetFillColor), not the stroke default
	strokeCol    color.RGBA
	strokeW      float64
	dashes       []float64 // dash/gap lengths in page mm (nil = solid), applied in flush
	dashOffset   float64
	breakPath    bool // the next segment starts a new subpath (new dash pattern)

	lineTypes *lineTypes // the drawing's LTYPE table (nil = all solid)
}

// maxPendingSegs bounds the size of one batched path.
const maxPendingSegs = 20000

// maxRasterChain bounds the connected segments (polyline edges, arc pieces)
// collected for a continuous dash pattern when not batching.
const maxRasterChain = 256

// SetLineTypes sets the line types used to dash entities.
func (r *Renderer) SetLineTypes(lt *lineTypes) {
	r.lineTypes = lt
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

	return &Renderer{
		c:          c,
		ctx:        ctx,
		paper:      paper,
		landscape:  landscape,
		margin:     margin,
		fonts:      fontLibrary(fontDir),
		pageW:      w,
		pageH:      h,
	}
}

func (r *Renderer) SetTransform(t Transform) {
	r.transform = t
}

// SetBatching enables merging same-style strokes into large paths. Only use
// it for PDF output: canvas' rasterizer strokes a path by settling its
// outline, which is slow and can panic on large self-intersecting paths, so
// raster output keeps one path per primitive.
func (r *Renderer) SetBatching(on bool) {
	r.flush()
	r.batch = on
}

func (r *Renderer) SetStyle(col RGB, lineWidthMM float64) {
	r.SetDashedStyle(col, lineWidthMM, 0, nil)
}

// SetDashedStyle sets a stroke style with a dash pattern (page mm, starting
// with a dash; nil = solid) entered at offset, see Path.Dash.
func (r *Renderer) SetDashedStyle(col RGB, lineWidthMM, offset float64, dashes []float64) {
	rgba := color.RGBA{col.R, col.G, col.B, 255}
	r.color = rgba
	if r.styleSet && !r.fillSet && rgba == r.strokeCol && lineWidthMM == r.strokeW &&
		offset == r.dashOffset && slices.Equal(dashes, r.dashes) {
		return // same style: keep batching
	}
	r.flush()
	r.strokeCol, r.strokeW, r.styleSet, r.fillSet = rgba, lineWidthMM, true, false
	r.dashOffset, r.dashes = offset, dashes
	r.ctx.SetStrokeColor(rgba)
	r.ctx.SetStrokeWidth(lineWidthMM)
	r.ctx.SetFillColor(color.RGBA{0, 0, 0, 0}) // transparent fill by default
}

func (r *Renderer) SetFillColor(col RGB) {
	r.flush()
	r.fillSet = true
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
	r.penTo(x1, y1)
	r.lineTo(x2, y2)
}

// penTo starts a batched subpath at page point (x, y), unless the previous
// segment already ended there (then the stroke simply continues, which also
// gives polylines proper joins).
func (r *Renderer) penTo(x, y float64) {
	continues := r.pending != nil && !r.pending.Empty() && !r.breakPath &&
		math.Abs(x-r.penX) <= 1e-9 && math.Abs(y-r.penY) <= 1e-9
	r.breakPath = false
	switch {
	case r.batch && r.pendingSegs >= maxPendingSegs:
		r.flush()
	case !r.batch && (!continues || r.dashes == nil || r.pendingSegs >= maxRasterChain):
		// Without batching only dashed strokes chain up (for the pattern).
		r.flush()
	case continues:
		return
	}
	if r.pending == nil {
		r.pending = &canvas.Path{}
	}
	r.pending.MoveTo(x, y)
	r.penX, r.penY = x, y
}

// BreakPath makes the next segment start a new subpath even if it continues
// the last one, so it begins its own dash pattern.
func (r *Renderer) BreakPath() {
	r.breakPath = true
}

func (r *Renderer) lineTo(x, y float64) {
	r.pending.LineTo(x, y)
	r.penX, r.penY = x, y
	r.pendingSegs++
}

func (r *Renderer) cubeTo(c1x, c1y, c2x, c2y, x, y float64) {
	r.pending.CubeTo(c1x, c1y, c2x, c2y, x, y)
	r.penX, r.penY = x, y
	r.pendingSegs++
}

// flush draws the batched strokes, clipped to the clip rectangle when one is
// set (tiled output). Called before anything that changes the style or must
// keep its place in the drawing order (fills, text, page changes).
func (r *Renderer) flush() {
	p := r.pending
	r.pending, r.pendingSegs = nil, 0
	if p == nil || p.Empty() {
		return
	}
	// Dashing the geometry (rather than via the stroke style) keeps the
	// pattern phase intact when tiles clip the path.
	if r.dashes != nil {
		if p = p.Dash(r.dashOffset, r.dashes...); p.Empty() {
			return
		}
	}
	if r.clip != nil {
		if p = clipPath(p, *r.clip); p.Empty() {
			return
		}
	}
	if !r.batch && r.dashes != nil {
		// The rasterizer can panic stroking connected chains (see
		// SetBatching): draw the dash pieces one segment at a time.
		for s := p.Scanner(); s.Scan(); {
			if s.Cmd() != canvas.MoveToCmd {
				r.ctx.DrawPath(0, 0, s.Path())
			}
		}
		return
	}
	r.ctx.DrawPath(0, 0, p)
}

// clipPath clips a (multi-subpath) stroke path to a rectangle. Path.Clip only
// handles straight segments, and it joins consecutive subpaths that are both
// inside with a LineTo, so each subpath is flattened and clipped on its own.
func clipPath(p *canvas.Path, c canvas.Rect) *canvas.Path {
	clipped := &canvas.Path{}
	for _, sp := range p.Flatten(canvas.Tolerance).Split() {
		clipped = clipped.Append(sp.Clip(c.X0, c.Y0, c.X1, c.Y1))
	}
	return clipped
}

// outsideClip reports whether the page point (px, py), grown by pad mm in
// every direction, lies completely outside the clip rectangle.
func (r *Renderer) outsideClip(px, py, pad float64) bool {
	c := r.clip
	return c != nil && (px+pad < c.X0 || px-pad > c.X1 || py+pad < c.Y0 || py-pad > c.Y1)
}

// DrawEllipticArc draws the curve c + u·cos(t) + v·sin(t) for t from t0 to t1
// (radians; t1 < t0 runs clockwise), in DXF world coordinates. u and v are
// conjugate semi-axes, so circles, arcs, ellipses and their images under any
// affine INSERT transform (mirrored, rotated, non-uniformly scaled) all go
// through here.
//
// The curve is emitted as cubic Béziers (one per ≤90° piece), which are
// exact up to the usual 4/3·tan(θ/4) circle approximation and stay exact
// under the affine page transform.
func (r *Renderer) DrawEllipticArc(cx, cy, ux, uy, vx, vy, t0, t1 float64) {
	sweep := t1 - t0
	if sweep == 0 {
		return
	}
	n := max(int(math.Ceil(math.Abs(sweep)/(math.Pi/2)-1e-9)), 1)
	step := sweep / float64(n)
	k := 4.0 / 3.0 * math.Tan(step/4)

	// point and derivative at parameter t, in DXF world coordinates
	at := func(t float64) (x, y, dx, dy float64) {
		s, c := math.Sincos(t)
		return cx + ux*c + vx*s, cy + uy*c + vy*s, -ux*s + vx*c, -uy*s + vy*c
	}
	tx, ty := r.transform.X, r.transform.Y

	x0, y0, dx0, dy0 := at(t0)
	r.penTo(tx(x0), ty(y0))
	for i := 1; i <= n; i++ {
		x1, y1, dx1, dy1 := at(t0 + step*float64(i))
		r.cubeTo(tx(x0+k*dx0), ty(y0+k*dy0), tx(x1-k*dx1), ty(y1-k*dy1), tx(x1), ty(y1))
		x0, y0, dx0, dy0 = x1, y1, dx1, dy1
	}
}

func (r *Renderer) DrawPolyline(points [][2]float64, closed bool) {
	if len(points) < 2 {
		return
	}
	r.penTo(r.transform.X(points[0][0]), r.transform.Y(points[0][1]))
	for i := 1; i < len(points); i++ {
		r.lineTo(r.transform.X(points[i][0]), r.transform.Y(points[i][1]))
	}
	if closed && len(points) > 2 {
		r.lineTo(r.transform.X(points[0][0]), r.transform.Y(points[0][1]))
	}
}

func (r *Renderer) DrawSolid(x1, y1, x2, y2, x3, y3, x4, y4 float64) {
	r.flush()
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
	r.flush()
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
	if r.outsideClip(px, py, 0.2) {
		return
	}
	r.flush()
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

// textStyle is a STYLE table entry: font name and default width/slant.
type textStyle struct {
	font    string
	width   float64
	oblique float64 // degrees
}

// SetTextStyles sets the drawing's STYLE table.
func (r *Renderer) SetTextStyles(styles []dxf.Style) {
	r.textStyles = make(map[string]textStyle, len(styles))
	for _, s := range styles {
		font := s.PrimaryFontFileName
		if font == "" {
			font = s.Name
		}
		r.textStyles[strings.ToUpper(s.Name)] = textStyle{font: font, width: s.WidthFactor, oblique: s.ObliqueAngle}
	}
}

// textLook is how a piece of text is drawn: font and horizontal scale
// (width factor × substitution compensation) and slant.
type textLook struct {
	font    *fontSet
	width   float64
	oblique float64 // degrees
}

// lookFor returns the look of text in the named text style; width and
// oblique (from the entity, 0 = none given) override the style's.
func (r *Renderer) lookFor(styleName string, width, oblique float64) textLook {
	st, ok := r.textStyles[strings.ToUpper(styleName)]
	if !ok {
		st = textStyle{width: 1}
	}
	if width <= 0 {
		width = st.width
	}
	if width <= 0 {
		width = 1
	}
	if oblique == 0 {
		oblique = st.oblique
	}
	return r.lookWithFont(st.font, width, oblique)
}

// lookWithFont returns the look for a font name, width factor and slant.
func (r *Renderer) lookWithFont(font string, width, oblique float64) textLook {
	fs := r.fonts.font(font)
	return textLook{font: fs, width: width * fs.widthComp, oblique: oblique}
}

// styleFont returns the font name of a text style ("" = default).
func (r *Renderer) styleFont(styleName string) string {
	return r.textStyles[strings.ToUpper(styleName)].font
}

// textFace returns a face of the look's font whose cap height is heightMM.
// CAD text height is the height of capital letters, not the em size.
func (r *Renderer) textFace(look textLook, heightMM float64, col color.RGBA, style canvas.FontStyle) *canvas.FontFace {
	return look.font.face(heightMM, col, style)
}

// drawTextLine draws a text line with its baseline origin at (x, y),
// stretched horizontally by sx and slanted by obliqueDeg.
func (r *Renderer) drawTextLine(x, y float64, line *canvas.Text, sx, obliqueDeg float64) {
	if sx == 1 && obliqueDeg == 0 {
		r.ctx.DrawText(x, y, line)
		return
	}
	// The page is Y-down: glyph tops lie at smaller y, and a positive
	// oblique angle leans them to the right.
	shear := -math.Tan(obliqueDeg * math.Pi / 180)
	r.ctx.Push()
	r.ctx.ComposeView(canvas.Identity.Translate(x, y).Scale(sx, 1).Shear(shear, 0).Translate(-x, -y))
	r.ctx.DrawText(x, y, line)
	r.ctx.Pop()
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
func (r *Renderer) DrawText(x, y float64, text string, heightMM, rotationDeg, hAlign float64, vAlign textVAlign, look textLook) {
	if text == "" {
		return
	}
	r.flush()
	px := r.transform.X(x)
	py := r.transform.Y(y)
	scaledH := max(r.transform.Dist(heightMM), minTextHeightMM)

	face := r.textFace(look, scaledH, r.color, canvas.FontRegular)
	textLine := canvas.NewTextLine(face, text, canvas.Left)
	width := textLine.Bounds().W() * look.width
	if r.outsideClip(px, py, width+2*scaledH) {
		return
	}
	dx := -hAlign * width
	dy := baselineShift(scaledH, face.Metrics().Descent, vAlign)

	if math.Abs(rotationDeg) > 0.01 {
		r.ctx.Push()
		// DXF angles are CCW in a Y-up world; the page is Y-down, so negate.
		r.ctx.ComposeView(canvas.Identity.RotateAbout(-rotationDeg, px, py))
		r.drawTextLine(px+dx, py+dy, textLine, look.width, look.oblique)
		r.ctx.Pop()
	} else {
		r.drawTextLine(px+dx, py+dy, textLine, look.width, look.oblique)
	}
}

// mtextItem is one laid-out MText segment.
type mtextItem struct {
	seg   MTextSegment
	line  *canvas.Text
	look  textLook
	width float64 // drawn width (with the look's horizontal scale)
	h     float64 // cap height in page mm
	rise  float64 // baseline raise in page mm (super/subscripts)
	col   color.RGBA
}

// DrawMText draws multi-line formatted text. attach is the DXF attachment
// point (1..9: top/middle/bottom × left/center/right); lineSpacing is the
// MTEXT line spacing factor (0 = default 1.0); font is the text style's font,
// used where the text sets none.
func (r *Renderer) DrawMText(x, y float64, segments []MTextSegment, defaultHeightMM, rotationDeg float64, attach int, lineSpacing float64, font string) {
	r.flush()
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
		if seg.Style.Height > 0 {
			scaledH = r.transform.Dist(seg.Style.Height)
		}
		if seg.Style.HeightRelative > 0 {
			scaledH *= seg.Style.HeightRelative
		}
		// Stacked super/subscripts are drawn smaller (AutoCAD's default
		// stack scale), raised above or lowered below the baseline.
		var dy float64
		if seg.Style.Superscript || seg.Style.Subscript {
			dy = scaledH * 0.5 // Y-down: raise
			if seg.Style.Subscript {
				dy = -scaledH * 0.3
			}
			scaledH *= 0.7
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
		segFont := font
		if seg.Style.FontName != "" {
			segFont = seg.Style.FontName
		}
		width := seg.Style.WidthFactor
		if width <= 0 {
			width = 1
		}
		look := r.lookWithFont(segFont, width, seg.Style.ObliqueAngle)
		face := r.textFace(look, scaledH, textColor, fontStyle)
		textLine := canvas.NewTextLine(face, seg.Text, canvas.Left)
		last := len(lines) - 1
		lines[last] = append(lines[last], mtextItem{
			seg: seg, line: textLine, look: look, width: textLine.Bounds().W() * look.width, h: scaledH, rise: dy, col: textColor,
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

	maxW := 0.0
	for _, items := range lines {
		w := 0.0
		for _, it := range items {
			w += it.width
		}
		maxW = max(maxW, w)
	}
	if r.outsideClip(px, py, maxW+n*advance+2*defaultScaledH) {
		return
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
			r.drawTextLine(curX, curY-it.rise, it.line, it.look.width, it.look.oblique)

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
				// Drawn in the (possibly rotated) text frame, so not
				// clipped against the page-space clip rect.
				dp := &canvas.Path{}
				dp.MoveTo(curX, curY+deco.dy)
				dp.LineTo(curX+it.width, curY+deco.dy)
				r.ctx.DrawPath(0, 0, dp)
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

	r.penTo(r.transform.X(points[0][0]), r.transform.Y(points[0][1]))
	for i := 1; i < len(points); i++ {
		r.lineTo(r.transform.X(points[i][0]), r.transform.Y(points[i][1]))
	}
}

func (r *Renderer) DrawDebugBBox(bbox BBox) {
	r.SetStyle(RGB{255, 0, 0}, 0.3)

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
	r.flush()
}

func (r *Renderer) AddPage() {
	r.flush()
	r.pages = append(r.pages, r.c)
	c := canvas.New(r.pageW, r.pageH)
	r.c = c
	r.ctx = canvas.NewContext(c)
	r.ctx.SetCoordSystem(canvas.CartesianIV)
	r.styleSet = false // fresh context, default style
}

// SetClipRect restricts subsequent drawing to a page-space rectangle.
// Canvas has no clip state, so stroked paths are clipped geometrically and
// points/text entirely outside are skipped; fills and text straddling the
// edge are trimmed by MaskOutside for opaque output.
func (r *Renderer) SetClipRect(x, y, w, h float64) {
	r.flush()
	r.clip = &canvas.Rect{X0: x, Y0: y, X1: x + w, Y1: y + h}
}

func (r *Renderer) ClipEnd() {
	r.flush()
	r.clip = nil
}

// MaskOutside paints the page white outside the given page-space rectangle,
// hiding whatever spilled over the printable area of a tile.
func (r *Renderer) MaskOutside(x, y, w, h float64) {
	r.flush()
	r.ctx.Push()
	r.ctx.SetFillColor(color.RGBA{255, 255, 255, 255})
	r.ctx.SetStrokeColor(color.RGBA{0, 0, 0, 0})
	for _, rc := range [][4]float64{
		{0, 0, r.pageW, y},                       // top
		{0, y + h, r.pageW, r.pageH},             // bottom
		{0, y, x, y + h},                         // left
		{x + w, y, r.pageW, y + h},               // right
	} {
		if rc[2] <= rc[0] || rc[3] <= rc[1] {
			continue
		}
		p := &canvas.Path{}
		p.MoveTo(rc[0], rc[1])
		p.LineTo(rc[2], rc[1])
		p.LineTo(rc[2], rc[3])
		p.LineTo(rc[0], rc[3])
		p.Close()
		r.ctx.DrawPath(0, 0, p)
	}
	r.ctx.Pop()
}

func (r *Renderer) Save(path string, format string, dpi float64, transparent bool) error {
	if format == "" {
		format = formatFromExtension(path)
	}
	r.flush()

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
	r.SetStyle(col, lineWidthMM)
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

