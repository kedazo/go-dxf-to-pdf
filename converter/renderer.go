package converter

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
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
	"github.com/tdewolff/canvas/renderers/rasterizer"
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

	// Stroke batching (see SetBatching, and SetRasterStrokes for raster
	// output): consecutive strokes with the same style are collected into one
	// path and drawn in a single call. Writing one PDF path per segment
	// dominated the cost on large drawings.
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

	lineTypes    *lineTypes    // the drawing's LTYPE table (nil = all solid)
	leaderArrows *leaderArrows // leader arrowheads by dimension style (nil = none)
	minStrokeMM  float64       // thinner strokes are widened to this (raster: one pixel)
	clipPoly     [][2]float64  // page-space clip polygon (viewport), nil = none
	contentRect  *canvas.Rect  // current page: transparent raster output keeps only this (nil = all)
	overlay      *canvas.Canvas
	pageRects    []*canvas.Rect // of the completed pages
	overlays     []*canvas.Canvas
	rasterTol    float64                   // > 0: strokes are outlined here, with this tolerance (SetRasterStrokes)
	textCut      bool                      // text across the clip rect is cut as outlines (SetTextCut)
	textLines    map[shapedKey]*canvas.Text // laid-out text, reused by the tiles (textLine)
	textWidths   map[shapedKey]float64
	hatchCache   map[hatchKey][][4]float64 // pattern hatch lines, by hatch and scale
	hatchCached  int                       // segments held in hatchCache
	imageFiles   map[*dxf.Image]string
	images       map[string]image.Image // decoded image files
	wipeoutFrame bool                   // WIPEOUTFRAME 1: wipeout outlines plot
	imageFrame   bool                   // IMAGEFRAME 1: image outlines plot
}

// SetFrames sets whether the outlines of wipeouts and images plot, from
// the drawing's WIPEOUTVARIABLES and RASTERVARIABLES (no frames without).
func (r *Renderer) SetFrames(d *dxf.Drawing) {
	r.wipeoutFrame = d.WipeoutVariables != nil && d.WipeoutVariables.IsFramePlotted()
	r.imageFrame = d.RasterVariables != nil && d.RasterVariables.IsFramePlotted()
}

// SetClipPolygon clips what follows to a polygon in page coordinates (nil
// ends it): strokes and fills are cut, text and points are kept when their
// anchor is inside.
func (r *Renderer) SetClipPolygon(poly [][2]float64) {
	r.flush()
	r.clipPoly = poly
}

// clippedOut reports whether the page point lies outside the clip polygon.
func (r *Renderer) clippedOut(px, py float64) bool {
	return r.clipPoly != nil && !pointInPolygon(px, py, r.clipPoly)
}

// textClippedOut reports whether a text's page rectangle [x0,x1]×[y0,y1],
// turned by rotDeg (CCW in DXF terms) about (px, py), lies wholly outside
// the clip polygon. Text partly inside is drawn whole.
func (r *Renderer) textClippedOut(px, py, x0, y0, x1, y1, rotDeg float64) bool {
	if r.clipPoly == nil {
		return false
	}
	// Y-down page: a CCW DXF rotation turns clockwise here.
	s, c := math.Sincos(-rotDeg * math.Pi / 180)
	rect := make([][2]float64, 0, 4)
	for _, p := range [4][2]float64{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}} {
		dx, dy := p[0]-px, p[1]-py
		rect = append(rect, [2]float64{px + dx*c - dy*s, py + dx*s + dy*c})
	}
	return !polygonsIntersect(rect, r.clipPoly)
}

// clipFill cuts a fill path (even-odd) to the clip polygon.
func (r *Renderer) clipFill(p *canvas.Path) *canvas.Path {
	return r.clipFillRule(p, canvas.EvenOdd)
}

// clipFillRule cuts a fill path, filled with the given rule, to the clip
// polygon and the clip rectangle (only fills crossing its edge are cut).
func (r *Renderer) clipFillRule(p *canvas.Path, rule canvas.FillRule) *canvas.Path {
	if r.clip != nil {
		b := hullBounds(p)
		switch {
		case b.X1 < r.clip.X0 || b.X0 > r.clip.X1 || b.Y1 < r.clip.Y0 || b.Y0 > r.clip.Y1:
			return &canvas.Path{}
		case b.X0 < r.clip.X0 || b.X1 > r.clip.X1 || b.Y0 < r.clip.Y0 || b.Y1 > r.clip.Y1:
			// Uncut where canvas fails: the tile mask hides the spill.
			if q, ok := intersectFill(p, rule, canvas.Rectangle(r.clip.W(), r.clip.H()).Translate(r.clip.X0, r.clip.Y0)); ok {
				p, rule = q, canvas.NonZero // settled
			}
		}
	}
	if r.clipPoly == nil {
		return p
	}
	if q, ok := intersectFill(p, rule, polygonPath(r.clipPoly)); ok {
		return q
	}
	return &canvas.Path{} // better lost than spilling out of the viewport
}

// intersectFill intersects a fill path with a clip path; ok is false where
// canvas' boolean operations panic (degenerate geometry).
func intersectFill(p *canvas.Path, rule canvas.FillRule, clip *canvas.Path) (q *canvas.Path, ok bool) {
	defer func() {
		if recover() != nil {
			q, ok = nil, false
		}
	}()
	return p.Settle(rule).And(clip), true
}

// polygonPath is a closed path through the points.
func polygonPath(poly [][2]float64) *canvas.Path {
	p := &canvas.Path{}
	for i, v := range poly {
		if i == 0 {
			p.MoveTo(v[0], v[1])
		} else {
			p.LineTo(v[0], v[1])
		}
	}
	p.Close()
	return p
}

// clipStrokes cuts a stroke path to the clip polygon; pieces that stay
// connected remain one subpath.
//
// Closed subpaths keep their join at the start point: one left whole is
// closed again, and in one that was cut the piece through its start point
// is joined back together.
func (r *Renderer) clipStrokes(p *canvas.Path) *canvas.Path {
	out := &canvas.Path{}
	for _, sp := range p.Flatten(canvas.Tolerance).Split() {
		var segs [][4]float64
		closed := false
		for s := sp.Scanner(); s.Scan(); {
			if s.Cmd() == canvas.LineToCmd || s.Cmd() == canvas.CloseCmd {
				a, b := s.Start(), s.End()
				segs = append(segs, [4]float64{a.X, a.Y, b.X, b.Y})
				closed = closed || s.Cmd() == canvas.CloseCmd
			}
		}
		if len(segs) == 0 {
			continue
		}
		var pieces [][]canvas.Point
		clipToPolygon(segs, r.clipPoly, func(x1, y1, x2, y2 float64) {
			if n := len(pieces); n == 0 || !samePoint(pieces[n-1][len(pieces[n-1])-1], canvas.Point{X: x1, Y: y1}) {
				pieces = append(pieces, []canvas.Point{{X: x1, Y: y1}})
			}
			pieces[len(pieces)-1] = append(pieces[len(pieces)-1], canvas.Point{X: x2, Y: y2})
		})
		if len(pieces) == 0 {
			continue
		}
		start := canvas.Point{X: segs[0][0], Y: segs[0][1]}
		first, last := pieces[0], pieces[len(pieces)-1]
		whole := false
		if closed && samePoint(first[0], start) && samePoint(last[len(last)-1], start) {
			if len(pieces) == 1 {
				whole = true
			} else { // the last piece runs on into the first
				pieces[0] = append(last, first[1:]...)
				pieces = pieces[:len(pieces)-1]
			}
		}
		for _, piece := range pieces {
			out.MoveTo(piece[0].X, piece[0].Y)
			end := len(piece)
			if whole {
				end-- // the closing segment
			}
			for _, q := range piece[1:end] {
				out.LineTo(q.X, q.Y)
			}
			if whole {
				out.Close()
			}
		}
	}
	return out
}

// samePoint reports whether two page points coincide.
func samePoint(a, b canvas.Point) bool {
	return math.Abs(a.X-b.X) <= 1e-9 && math.Abs(a.Y-b.Y) <= 1e-9
}

// SetMinStrokeWidth widens every stroke to at least mm (0 = no minimum).
// Raster output uses one pixel, so hairlines don't fade away.
func (r *Renderer) SetMinStrokeWidth(mm float64) {
	r.minStrokeMM = mm
}

// SetLeaderArrows sets how leader arrowheads are drawn.
func (r *Renderer) SetLeaderArrows(la *leaderArrows) {
	r.leaderArrows = la
}

// maxPendingSegs bounds the size of one batched path.
const maxPendingSegs = 20000

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

// SetBatching enables merging same-style strokes into large paths. For
// raster output use it with SetRasterStrokes: canvas' rasterizer strokes a
// path by settling its whole outline, which is slow and can panic on large
// self-intersecting paths.
func (r *Renderer) SetBatching(on bool) {
	r.flush()
	r.batch = on
}

// SetRasterStrokes makes the renderer outline strokes itself, for raster
// output at dpi: each subpath of a batch is stroked on its own (falling back
// to single segments where canvas fails) and the batch is filled as one.
func (r *Renderer) SetRasterStrokes(dpi float64) {
	r.flush()
	r.rasterTol = canvas.PixelTolerance * 25.4 / dpi
}

func (r *Renderer) SetStyle(col RGB, lineWidthMM float64) {
	r.SetDashedStyle(col, lineWidthMM, 0, nil)
}

// SetDashedStyle sets a stroke style with a dash pattern (page mm, starting
// with a dash; nil = solid) entered at offset, see Path.Dash.
func (r *Renderer) SetDashedStyle(col RGB, lineWidthMM, offset float64, dashes []float64) {
	rgba := color.RGBA{col.R, col.G, col.B, 255}
	r.color = rgba
	if lineWidthMM > 0 {
		lineWidthMM = max(lineWidthMM, r.minStrokeMM)
	}
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
	case !r.batch && (!continues || r.dashes == nil || r.pendingSegs >= maxPendingSegs):
		// Without batching only dashed strokes chain up, for a continuous
		// pattern; flush strokes their dashes one segment at a time.
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
	if r.clipPoly != nil {
		if p = r.clipStrokes(p); p.Empty() {
			return
		}
	}
	if r.rasterTol > 0 && r.strokeW > 0 {
		r.fillStrokeOutline(p)
		return
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

// fillStrokeOutline draws strokes as one filled outline (raster output, see
// SetRasterStrokes). Every subpath is stroked on its own, so the outlines
// all wind the same way and fill as their union.
func (r *Renderer) fillStrokeOutline(p *canvas.Path) {
	outline := &canvas.Path{}
	for _, sp := range p.Split() {
		if o := strokeOutline(sp, r.strokeW, r.rasterTol); o != nil {
			outline = outline.Append(o)
			continue
		}
		// Chains can make canvas panic: stroke their segments one by one.
		for s := sp.Scanner(); s.Scan(); {
			if s.Cmd() != canvas.MoveToCmd {
				if o := strokeOutline(s.Path(), r.strokeW, r.rasterTol); o != nil {
					outline = outline.Append(o)
				}
			}
		}
	}
	if outline.Empty() {
		return
	}
	r.ctx.Push()
	r.ctx.SetFillColor(r.strokeCol)
	r.ctx.SetStrokeColor(color.RGBA{})
	r.ctx.SetFillRule(canvas.NonZero)
	r.ctx.DrawPath(0, 0, outline)
	r.ctx.Pop()
}

// strokeOutline outlines a stroke of width w (butt caps, miter joins, like
// the context's default style), or returns nil where canvas panics.
func strokeOutline(p *canvas.Path, w, tolerance float64) (outline *canvas.Path) {
	defer func() {
		if recover() != nil {
			outline = nil
		}
	}()
	return p.Stroke(w, canvas.ButtCap, canvas.MiterJoin, tolerance)
}

// clipPath clips a (multi-subpath) stroke path to a rectangle. Path.Clip only
// handles straight segments, and it joins consecutive subpaths that are both
// inside with a LineTo, so each subpath is flattened and clipped on its own.
//
// Bounds come first: paths and subpaths inside the rectangle are kept as
// they are (curves too), those outside dropped, and only the ones crossing
// its edge are flattened and clipped.
func clipPath(p *canvas.Path, c canvas.Rect) *canvas.Path {
	inside := func(b canvas.Rect) bool { return b.X0 >= c.X0 && b.X1 <= c.X1 && b.Y0 >= c.Y0 && b.Y1 <= c.Y1 }
	if inside(hullBounds(p)) {
		return p
	}
	clipped := &canvas.Path{}
	for _, sp := range p.Split() {
		switch b := hullBounds(sp); {
		case b.X1 < c.X0 || b.X0 > c.X1 || b.Y1 < c.Y0 || b.Y0 > c.Y1:
		case inside(b):
			clipped = clipped.Append(sp)
		default:
			clipped = clipped.Append(sp.Flatten(canvas.Tolerance).Clip(c.X0, c.Y0, c.X1, c.Y1))
		}
	}
	return clipped
}

// hullBounds returns a rectangle around a path's points and control points,
// which holds the path. (canvas' FastBounds misses the end of a cubic that
// lies beyond its second control point; Bounds solves for the extremes.)
func hullBounds(p *canvas.Path) canvas.Rect {
	b := canvas.Rect{X0: math.Inf(1), Y0: math.Inf(1), X1: math.Inf(-1), Y1: math.Inf(-1)}
	add := func(q canvas.Point) {
		b.X0, b.X1 = min(b.X0, q.X), max(b.X1, q.X)
		b.Y0, b.Y1 = min(b.Y0, q.Y), max(b.Y1, q.Y)
	}
	for s := p.Scanner(); s.Scan(); {
		switch s.Cmd() {
		case canvas.ArcToCmd:
			return p.Bounds()
		case canvas.QuadToCmd:
			add(s.CP1())
		case canvas.CubeToCmd:
			add(s.CP1())
			add(s.CP2())
		}
		add(s.End())
	}
	if b.X0 > b.X1 {
		return canvas.Rect{}
	}
	return b
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

// DrawCurve draws connected cubic Béziers (start, two control points, end;
// drawing coordinates) as one stroke.
func (r *Renderer) DrawCurve(curves [][4][2]float64) {
	if len(curves) == 0 {
		return
	}
	t := r.transform
	r.penTo(t.X(curves[0][0][0]), t.Y(curves[0][0][1]))
	for _, c := range curves {
		r.cubeTo(t.X(c[1][0]), t.Y(c[1][1]), t.X(c[2][0]), t.Y(c[2][1]), t.X(c[3][0]), t.Y(c[3][1]))
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
	r.ctx.DrawPath(0, 0, r.clipFill(p))
}

// FillPolygons fills a set of closed loops (DXF world coordinates) as one
// shape with the even-odd rule, so inner loops become holes.
func (r *Renderer) FillPolygons(polys [][][2]float64, col RGB) {
	r.fillPolygons(polys, col, canvas.EvenOdd)
}

// FillUnion fills the union of closed loops (DXF world coordinates) that may
// overlap: drawn as one shape, so no seams show where they meet. The loops
// are turned to the same orientation for the non-zero rule.
func (r *Renderer) FillUnion(polys [][][2]float64, col RGB) {
	oriented := make([][][2]float64, 0, len(polys))
	for _, poly := range polys {
		area := 0.0
		for i := range poly {
			a, b := poly[i], poly[(i+1)%len(poly)]
			area += a[0]*b[1] - b[0]*a[1]
		}
		if area < 0 {
			rev := make([][2]float64, len(poly))
			for i, v := range poly {
				rev[len(poly)-1-i] = v
			}
			poly = rev
		}
		oriented = append(oriented, poly)
	}
	r.fillPolygons(oriented, col, canvas.NonZero)
}

func (r *Renderer) fillPolygons(polys [][][2]float64, col RGB, rule canvas.FillRule) {
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
	r.ctx.SetFillRule(rule)
	r.ctx.DrawPath(0, 0, r.clipFillRule(p, rule))
	r.ctx.Pop()
}

func (r *Renderer) DrawPoint(x, y float64) {
	px := r.transform.X(x)
	py := r.transform.Y(y)
	if r.outsideClip(px, py, 0.2) || r.clippedOut(px, py) {
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

// textStyle is a STYLE table entry: font file, TrueType family and face,
// and default width/slant.
type textStyle struct {
	font         string
	family       string // TrueType family from the style's extended data ("" = none)
	bold, italic bool
	width        float64
	oblique      float64 // degrees
}

// faceStyle is the canvas face style for bold/italic.
func faceStyle(bold, italic bool) canvas.FontStyle {
	style := canvas.FontRegular
	if bold {
		style |= canvas.FontBold
	}
	if italic {
		style |= canvas.FontItalic
	}
	return style
}

// SetTextStyles sets the drawing's STYLE table.
func (r *Renderer) SetTextStyles(styles []dxf.Style) {
	r.textStyles = make(map[string]textStyle, len(styles))
	for i := range styles {
		s := &styles[i]
		st := textStyle{font: s.PrimaryFontFileName, width: s.WidthFactor, oblique: s.ObliqueAngle}
		if st.font == "" {
			st.font = s.Name
		}
		// Bold and italic variants of one TrueType file differ only here.
		if family, bold, italic, ok := s.TrueTypeFont(); ok {
			st.family, st.bold, st.italic = family, bold, italic
		}
		r.textStyles[strings.ToUpper(s.Name)] = st
	}
}

// textLook is how a piece of text is drawn: font and face, horizontal
// scale (width factor × substitution compensation) and slant.
type textLook struct {
	font    *fontSet
	style   canvas.FontStyle
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
	look := r.lookWithFont(st.font, st.family, width, oblique)
	look.style = faceStyle(st.bold, st.italic)
	return look
}

// lookWithFont returns the look (regular face) for a font file and/or
// family name, width factor and slant.
func (r *Renderer) lookWithFont(font, family string, width, oblique float64) textLook {
	fs := r.fonts.fontFor(font, family)
	return textLook{font: fs, width: width * fs.widthComp, oblique: oblique}
}

// textStyleNamed returns a text style by name (the zero style if unknown).
func (r *Renderer) textStyleNamed(styleName string) textStyle {
	return r.textStyles[strings.ToUpper(styleName)]
}

// textFace returns a face of the look's font whose cap height is heightMM.
// CAD text height is the height of capital letters, not the em size.
func (r *Renderer) textFace(look textLook, heightMM float64, col color.RGBA, style canvas.FontStyle) *canvas.FontFace {
	return look.font.face(heightMM, col, style)
}

// shapedKey identifies laid-out text: a face (faces are reused, see
// fontSet.face) and a string.
type shapedKey struct {
	face *canvas.FontFace
	s    string
}

// maxShapedTexts bounds the text lines (and widths) kept for reuse.
const maxShapedTexts = 100_000

// textLine returns the laid-out line of s, shaping it once per face: tiles
// and repeated labels draw the same text again and again.
func (r *Renderer) textLine(face *canvas.FontFace, s string) *canvas.Text {
	key := shapedKey{face, s}
	if t, ok := r.textLines[key]; ok {
		return t
	}
	if r.textLines == nil || len(r.textLines) >= maxShapedTexts {
		r.textLines = map[shapedKey]*canvas.Text{}
	}
	t := canvas.NewTextLine(face, s, canvas.Left)
	r.textLines[key] = t
	return t
}

// textWidth returns the advance width of s in the face, measured once.
func (r *Renderer) textWidth(face *canvas.FontFace, s string) float64 {
	key := shapedKey{face, s}
	if w, ok := r.textWidths[key]; ok {
		return w
	}
	if r.textWidths == nil || len(r.textWidths) >= maxShapedTexts {
		r.textWidths = map[shapedKey]float64{}
	}
	w := face.TextWidth(s)
	r.textWidths[key] = w
	return w
}

// drawTextLine draws a text line with its baseline origin at (x, y),
// stretched horizontally by sx and slanted by obliqueDeg.
func (r *Renderer) drawTextLine(x, y float64, line *canvas.Text, sx, obliqueDeg float64) {
	if sx == 1 && obliqueDeg == 0 {
		r.drawText(x, y, line)
		return
	}
	// The page is Y-down: glyph tops lie at smaller y, and a positive
	// oblique angle leans them to the right.
	shear := -math.Tan(obliqueDeg * math.Pi / 180)
	r.ctx.Push()
	// Shear after scaling (matrices apply right to left), so the width
	// factor doesn't change the slant angle.
	r.ctx.ComposeView(canvas.Identity.Translate(x, y).Shear(shear, 0).Scale(sx, 1).Translate(-x, -y))
	r.drawText(x, y, line)
	r.ctx.Pop()
}

// SetTextCut makes text that crosses the clip rectangle be drawn as glyph
// outlines cut at it (transparent tiled PDF, which has no mask over the
// margins).
func (r *Renderer) SetTextCut(on bool) {
	r.textCut = on
}

// drawText draws a text line through the context, cut at the clip
// rectangle when SetTextCut is on.
func (r *Renderer) drawText(x, y float64, line *canvas.Text) {
	if !r.textCut || r.clip == nil {
		r.ctx.DrawText(x, y, line)
		return
	}
	// The matrix Context.DrawText uses, into canvas coordinates.
	m := r.ctx.CoordSystemView().Mul(r.ctx.View()).Translate(x, y).ReflectY()
	clip := canvas.Rectangle(r.clip.W(), r.clip.H()).Translate(r.clip.X0, r.clip.Y0).Transform(r.ctx.CoordSystemView())
	c, b := clip.FastBounds(), line.OutlineBounds().Add(line.Bounds()).Transform(m) // glyphs may overhang their advance
	switch {
	case b.X0 >= c.X0 && b.X1 <= c.X1 && b.Y0 >= c.Y0 && b.Y1 <= c.Y1: // inside
		r.ctx.DrawText(x, y, line)
	case b.X0 < c.X1 && b.X1 > c.X0 && b.Y0 < c.Y1 && b.Y1 > c.Y0: // across the edge
		line.RenderTo(textCutter{dst: r.c, clip: clip}, m, 0)
	}
}

// textCutter renders text as paths cut to a clip path (canvas coordinates).
type textCutter struct {
	dst  canvas.Renderer
	clip *canvas.Path
}

func (t textCutter) Size() (float64, float64) { return t.dst.Size() }

func (t textCutter) RenderPath(p *canvas.Path, style canvas.Style, m canvas.Matrix) {
	p = p.Copy().Transform(m)
	if cut, ok := intersectFill(p, style.FillRule, t.clip); ok {
		p = cut
	}
	if !p.Empty() {
		t.dst.RenderPath(p, style, canvas.Identity)
	}
}

func (t textCutter) RenderText(text *canvas.Text, m canvas.Matrix) { text.RenderTo(t, m, 0) }

func (t textCutter) RenderImage(img image.Image, m canvas.Matrix) { t.dst.RenderImage(img, m) }

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

	face := r.textFace(look, scaledH, r.color, look.style)
	textLine := r.textLine(face, text)
	width := textLine.Bounds().W() * look.width
	if r.outsideClip(px, py, width+2*scaledH) {
		return
	}
	dx := -hAlign * width
	dy := baselineShift(scaledH, face.Metrics().Descent, vAlign)
	if r.textClippedOut(px, py, px+dx, py+dy-scaledH, px+dx+width, py+dy+face.Metrics().Descent, rotationDeg) {
		return
	}

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

// mtextItem is one laid-out piece of MText: a run of text, or a stacked
// fraction.
type mtextItem struct {
	seg   MTextSegment
	line  *canvas.Text
	den   *canvas.Text // a stacked fraction's denominator (line is the numerator)
	look  textLook
	x     float64 // offset from the line start in page mm
	width float64 // drawn width (with the look's horizontal scale)
	ink   float64 // width without trailing spaces
	space bool    // ends with a space: a line may break (and justify) after it
	h     float64 // cap height in page mm
	rise  float64 // baseline raise in page mm (super/subscripts)
	col   color.RGBA
}

// mtextTabStops is the distance of the default MText tab stops, in text
// heights, used where a paragraph sets none (AutoCAD's own default is
// unverified).
const mtextTabStops = 4

// mtextTabbed is text placed at a centre or right tab stop, still to be
// aligned on it.
type mtextTabbed struct {
	first      int     // its first item in the line
	stop, from float64 // the stop, and the pen position before the tab
	kind       dxf.MTextTabStopType
}

// mtextWrapSlack widens an MText box for wrapping: boxes are often fitted
// to the text in the original font, which ours only approximates.
const mtextWrapSlack = 1.03

// mtextStackScale is the height of a stacked fraction's parts, as a share
// of the text height.
const mtextStackScale = 0.6

// splitMTextRun splits a run into the pieces laid out on their own: tabs,
// and, when wrapping, words (each with its trailing spaces).
func splitMTextRun(text string, words bool) []string {
	var pieces []string
	start := 0
	for i := 0; i < len(text); i++ {
		switch {
		case text[i] == '\t':
			if start < i {
				pieces = append(pieces, text[start:i])
			}
			pieces = append(pieces, "\t")
			start = i + 1
		case words && text[i] == ' ' && (i+1 == len(text) || text[i+1] != ' '):
			pieces = append(pieces, text[start:i+1])
			start = i + 1
		}
	}
	if start < len(text) {
		pieces = append(pieces, text[start:])
	}
	return pieces
}

// justifyGaps counts the word gaps a justified line can widen: the items
// ending in a space, except the last.
func justifyGaps(items []mtextItem) int {
	n := 0
	for _, it := range items[:max(len(items)-1, 0)] {
		if it.space {
			n++
		}
	}
	return n
}

// drawStack draws a stacked fraction item with its left edge at x on the
// baseline y: one part above the other, with a bar between them (a/b) or
// without (a^b), or side by side with a slash (a#b).
func (r *Renderer) drawStack(it mtextItem, x, y float64) {
	numW, denW := it.line.Bounds().W()*it.look.width, it.den.Bounds().W()*it.look.width
	line := func(x1, y1, x2, y2 float64) {
		r.ctx.Push()
		r.ctx.SetStrokeColor(it.col)
		r.ctx.SetStrokeWidth(max(it.h*0.05, r.minStrokeMM))
		r.ctx.SetFillColor(color.RGBA{0, 0, 0, 0})
		p := &canvas.Path{}
		p.MoveTo(x1, y1)
		p.LineTo(x2, y2)
		r.ctx.DrawPath(0, 0, p)
		r.ctx.Pop()
	}
	if it.seg.Style.StackType == dxf.MTextStackDiagonal {
		gap := it.width - numW - denW
		r.drawTextLine(x, y-it.h*0.45, it.line, it.look.width, it.look.oblique)
		r.drawTextLine(x+numW+gap, y+it.h*0.1, it.den, it.look.width, it.look.oblique)
		line(x+numW+gap*0.15, y+it.h*0.1, x+numW+gap*0.85, y-it.h*0.95)
		return
	}
	r.drawTextLine(x+(it.width-numW)/2, y-it.h*0.7, it.line, it.look.width, it.look.oblique)
	r.drawTextLine(x+(it.width-denW)/2, y+it.h*0.35, it.den, it.look.width, it.look.oblique)
	if it.seg.Style.StackType != dxf.MTextStackTolerance {
		line(x, y-it.h*0.5, x+it.width, y-it.h*0.5)
	}
}

// DrawMText draws multi-line formatted text. attach is the DXF attachment
// point (1..9: top/middle/bottom × left/center/right); lineSpacing is the
// MTEXT line spacing factor (0 = default 1.0); boxWidthMM is the MTEXT's
// box width, which lines wrap at and paragraphs align in (0 = no box);
// style is the text style, whose font and face apply where the text sets no
// font. Paragraph indents and tab stops are in multiples of the text height.
func (r *Renderer) DrawMText(x, y float64, segments []MTextSegment, defaultHeightMM, rotationDeg float64, attach int, lineSpacing, boxWidthMM float64, style textStyle) {
	r.flush()
	px := r.transform.X(x)
	py := r.transform.Y(y)
	defaultScaledH := max(r.transform.Dist(defaultHeightMM), minTextHeightMM)
	if lineSpacing <= 0 {
		lineSpacing = 1
	}
	wrapWidthMM := boxWidthMM * mtextWrapSlack

	// Pass 1: lay out segments into lines (wrapping words at the box width,
	// indenting paragraphs, moving to tab stops) and measure them.
	lines := [][]mtextItem{nil}
	paras := []dxf.MTextParagraph{{}} // the paragraph of each line
	firstOfPara := []bool{true}
	hasTab := []bool{false} // tab columns aren't justified
	x0 := 0.0               // pen position in the current line
	started := false        // the current line has its indent
	newLine := func(paragraphStart bool) {
		lines = append(lines, nil)
		paras = append(paras, dxf.MTextParagraph{})
		firstOfPara = append(firstOfPara, paragraphStart)
		hasTab = append(hasTab, false)
		x0, started = 0, false
	}
	// begin starts a line's content: it takes the paragraph's indent.
	begin := func(p dxf.MTextParagraph) {
		if started {
			return
		}
		started = true
		last := len(lines) - 1
		paras[last] = p
		indent := p.LeftIndent
		if firstOfPara[last] {
			indent += p.FirstLineIndent
		}
		x0 = max(x0, indent*defaultScaledH)
	}
	canBreak := false // the line may wrap before the next piece (after a space or tab)
	// The text after a centre or right tab stop, up to the next tab or the
	// line end, is laid out from the stop and then moved to centre on it or
	// end at it (but not back over what precedes it).
	var tabbed *mtextTabbed
	alignTabbed := func() {
		g := tabbed
		tabbed = nil
		items := lines[len(lines)-1]
		if g == nil || g.first >= len(items) {
			return
		}
		last := items[len(items)-1]
		w := last.x + last.ink - items[g.first].x
		shift := -w
		if g.kind == dxf.MTextTabStopCenter {
			shift = -w / 2
		}
		shift = max(shift, g.from-g.stop)
		for k := g.first; k < len(items); k++ {
			items[k].x += shift
		}
		x0 += shift
	}
	// place puts an item (with its ink width set) at the pen; wrapInk is the
	// width that must still fit on the line (its whole word).
	place := func(it mtextItem, wrapInk float64) {
		begin(it.seg.Style.Paragraph)
		last := len(lines) - 1
		limit := wrapWidthMM - paras[last].RightIndent*defaultScaledH
		extent := x0 + wrapInk
		if g := tabbed; g != nil { // it will move back to end at or centre on the stop
			shift := -(extent - g.stop)
			if g.kind == dxf.MTextTabStopCenter {
				shift /= 2
			}
			extent += max(shift, g.from-g.stop)
		}
		if wrapWidthMM > 0 && canBreak && len(lines[last]) > 0 && extent > limit {
			tabbed = nil // wrapped: left as a left stop
			newLine(false)
			begin(it.seg.Style.Paragraph)
			last++
		}
		it.x = x0
		x0 += it.width
		lines[last] = append(lines[last], it)
		canBreak = it.space
	}
	tab := func(p dxf.MTextParagraph) {
		alignTabbed()
		canBreak = true
		begin(p)
		hasTab[len(lines)-1] = true
		for _, stop := range p.TabStops {
			if pos := stop.Position * defaultScaledH; pos > x0+1e-9 {
				if stop.Type != dxf.MTextTabStopLeft {
					tabbed = &mtextTabbed{len(lines[len(lines)-1]), pos, x0, stop.Type}
				}
				x0 = pos
				return
			}
		}
		every := mtextTabStops * defaultScaledH
		x0 = (math.Floor(x0/every+1e-9) + 1) * every
	}
	for _, seg := range segments {
		if seg.NewLine {
			alignTabbed()
			newLine(true)
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

		// A run without a \f font code is in the text style's font and face.
		segFont, segFamily := style.font, style.family
		bold, italic := style.bold || seg.Style.Bold, style.italic || seg.Style.Italic
		if seg.Style.FontName != "" {
			segFont, segFamily = seg.Style.FontName, ""
			bold, italic = seg.Style.Bold, seg.Style.Italic
		}
		fontStyle := faceStyle(bold, italic)

		textColor := r.color
		if seg.Style.HasColor {
			textColor = color.RGBA{uint8(seg.Style.ColorR), uint8(seg.Style.ColorG), uint8(seg.Style.ColorB), 255}
		}
		// Without \W and \Q the text style's width factor and slant apply.
		width, oblique := seg.Style.WidthFactor, seg.Style.ObliqueAngle
		if width <= 0 || width == 1 {
			width = style.width
		}
		if width <= 0 {
			width = 1
		}
		if oblique == 0 {
			oblique = style.oblique
		}
		look := r.lookWithFont(segFont, segFamily, width, oblique)

		// A stacked fraction: numerator and denominator, smaller.
		if seg.Style.Stacked {
			num, den := seg.Style.Numerator, seg.Style.Denominator
			face := r.textFace(look, max(scaledH*mtextStackScale, minTextHeightMM), textColor, fontStyle)
			it := mtextItem{seg: seg, look: look, h: scaledH, col: textColor,
				line: r.textLine(face, num), den: r.textLine(face, den)}
			if seg.Style.StackType == dxf.MTextStackDiagonal { // side by side, divided by a slash
				it.width = (r.textWidth(face, num) + r.textWidth(face, den) + scaledH*0.3) * look.width
			} else {
				it.width = max(r.textWidth(face, num), r.textWidth(face, den)) * look.width
			}
			it.ink = it.width
			place(it, it.width)
			continue
		}

		// \A: a run smaller than the line sits at its bottom, middle or top.
		if !seg.Style.Superscript && !seg.Style.Subscript && scaledH < defaultScaledH {
			switch seg.Style.VerticalAlignment {
			case dxf.MTextVerticalAlignmentCenter:
				dy += (defaultScaledH - scaledH) / 2
			case dxf.MTextVerticalAlignmentTop:
				dy += defaultScaledH - scaledH
			}
		}

		face := r.textFace(look, scaledH, textColor, fontStyle)
		for _, piece := range splitMTextRun(seg.Text, wrapWidthMM > 0) {
			if piece == "\t" {
				tab(seg.Style.Paragraph)
				continue
			}
			ink := r.textWidth(face, strings.TrimRight(piece, " ")) * look.width // trailing spaces may hang over
			if t := seg.Style.Tracking; t > 0 && math.Abs(t-1) > 1e-9 {
				// \T: each character on its own, its advance scaled.
				wrapInk := ink * t
				chars := []rune(piece)
				for i, ch := range chars {
					textLine := r.textLine(face, string(ch))
					adv := textLine.Bounds().W() * look.width
					it := mtextItem{seg: seg, line: textLine, look: look, width: adv * t, ink: adv, h: scaledH, rise: dy, col: textColor}
					if ch == ' ' { // a run of spaces is one break (and justify gap), after its last
						it.ink, it.space = 0, i+1 == len(chars) || chars[i+1] != ' '
					}
					place(it, wrapInk)
					wrapInk = it.ink
				}
				continue
			}
			textLine := r.textLine(face, piece)
			w := textLine.Bounds().W() * look.width
			place(mtextItem{seg: seg, line: textLine, look: look, width: w, ink: ink, space: strings.HasSuffix(piece, " "),
				h: scaledH, rise: dy, col: textColor}, ink)
		}
	}
	alignTabbed()

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

	lineWidth := func(items []mtextItem) float64 {
		if len(items) == 0 {
			return 0
		}
		last := items[len(items)-1]
		return last.x + last.ink // trailing spaces don't count for alignment
	}
	maxW := 0.0
	for _, items := range lines {
		maxW = max(maxW, lineWidth(items))
	}
	// Paragraphs align within the box, or within the widest line without
	// one; the box sits at the attachment point.
	boxW := max(boxWidthMM, maxW)
	boxLeft := px - hAlign*boxW
	if r.outsideClip(px, py, boxW+n*advance+2*defaultScaledH) {
		return
	}
	if r.textClippedOut(px, py, boxLeft, firstBaseline-defaultScaledH, boxLeft+boxW,
		firstBaseline+(n-1)*advance+defaultScaledH*estDescent, rotationDeg) {
		return
	}

	if math.Abs(rotationDeg) > 0.01 {
		r.ctx.Push()
		r.ctx.ComposeView(canvas.Identity.RotateAbout(-rotationDeg, px, py))
	}

	for li, items := range lines {
		align := hAlign // the attachment point's, unless the paragraph sets one
		switch paras[li].Alignment {
		case dxf.MTextParagraphAlignmentLeft, dxf.MTextParagraphAlignmentJustified, dxf.MTextParagraphAlignmentDistributed:
			align = 0
		case dxf.MTextParagraphAlignmentCenter:
			align = 0.5
		case dxf.MTextParagraphAlignmentRight:
			align = 1
		}
		// Lines align between the indents (the left one is in the item offsets).
		free := boxW - paras[li].RightIndent*defaultScaledH - lineWidth(items)
		lineX := boxLeft + free*align
		lastOfPara := li+1 == len(lines) || firstOfPara[li+1]
		gap := 0.0 // justification: extra space after each word
		if a := paras[li].Alignment; boxWidthMM > 0 && free > 0 && !hasTab[li] &&
			(a == dxf.MTextParagraphAlignmentJustified && !lastOfPara || a == dxf.MTextParagraphAlignmentDistributed) {
			if n := justifyGaps(items); n > 0 {
				gap = free / float64(n)
			}
		}
		curY := firstBaseline + float64(li)*advance

		spread := 0.0
		for k, it := range items {
			curX := lineX + it.x + spread
			if it.space {
				spread += gap
			}
			if it.den != nil {
				r.drawStack(it, curX, curY)
				continue
			}
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
				r.ctx.SetStrokeWidth(max(it.h*0.07, r.minStrokeMM))
				r.ctx.SetFillColor(color.RGBA{0, 0, 0, 0})
				// Drawn in the (possibly rotated) text frame, so not
				// clipped against the page-space clip rect.
				dp := &canvas.Path{}
				dp.MoveTo(curX, curY+deco.dy)
				w := it.width
				if it.space && k < len(items)-1 {
					w += gap // through a justified word gap (see justifyGaps)
				}
				dp.LineTo(curX+w, curY+deco.dy)
				r.ctx.DrawPath(0, 0, dp)
				r.ctx.Pop()
			}
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
	r.pageRects = append(r.pageRects, r.contentRect)
	r.overlays = append(r.overlays, r.overlay)
	r.contentRect, r.overlay = nil, nil
	c := canvas.New(r.pageW, r.pageH)
	r.c = c
	r.ctx = canvas.NewContext(c)
	r.ctx.SetCoordSystem(canvas.CartesianIV)
	r.styleSet = false // fresh context, default style
}

// SetContentRect limits the current page of transparent raster output to a
// page-space rectangle: anything drawn outside it (text or fills spilling
// over a tile's edge) is cleared when the page is rasterized.
func (r *Renderer) SetContentRect(x, y, w, h float64) {
	r.contentRect = &canvas.Rect{X0: x, Y0: y, X1: x + w, Y1: y + h}
}

// DrawOverlay runs draw on the current page's overlay, which is put on top
// of the page after the content rectangle is applied (e.g. crop marks).
func (r *Renderer) DrawOverlay(draw func()) {
	r.flush()
	if r.overlay == nil {
		r.overlay = canvas.New(r.pageW, r.pageH)
	}
	c, ctx := r.c, r.ctx
	r.c, r.ctx = r.overlay, canvas.NewContext(r.overlay)
	r.ctx.SetCoordSystem(canvas.CartesianIV)
	r.styleSet = false
	draw()
	r.flush()
	r.c, r.ctx = c, ctx
	r.styleSet = false
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
	rects := append(append([]*canvas.Rect(nil), r.pageRects...), r.contentRect)
	overlays := append(append([]*canvas.Canvas(nil), r.overlays...), r.overlay)

	switch format {
	case "pdf":
		return r.savePDF(path, allPages, overlays)
	case "png":
		return r.saveRaster(path, allPages, rects, overlays, dpi, transparent, "png")
	case "jpg", "jpeg":
		return r.saveRaster(path, allPages, rects, overlays, dpi, false, "jpg")
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func (r *Renderer) savePDF(path string, pages, overlays []*canvas.Canvas) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	opts := pdf.DefaultOptions
	p := pdf.New(f, pages[0].W, pages[0].H, &opts)
	for i, page := range pages {
		if i > 0 {
			p.NewPage(page.W, page.H)
		}
		page.RenderTo(p)
		if overlays[i] != nil {
			overlays[i].RenderTo(p)
		}
	}
	return p.Close()
}

// rasterDPI is the raster resolution used for a requested DPI (0 = default).
func rasterDPI(dpi float64) float64 {
	if dpi <= 0 {
		return 300
	}
	return dpi
}

func (r *Renderer) saveRaster(path string, pages []*canvas.Canvas, rects []*canvas.Rect, overlays []*canvas.Canvas,
	dpi float64, transparent bool, format string) error {
	res := canvas.DPI(rasterDPI(dpi))

	for i, c := range pages {
		pagePath := path
		if len(pages) > 1 {
			ext := filepath.Ext(path)
			base := strings.TrimSuffix(path, ext)
			pagePath = fmt.Sprintf("%s_%d%s", base, i+1, ext)
		}

		if transparent && format == "png" && (rects[i] != nil || overlays[i] != nil) {
			if err := writeMaskedPNG(pagePath, c, rects[i], overlays[i], res); err != nil {
				return err
			}
			continue
		}
		if overlays[i] != nil {
			c = withOverlay(c, overlays[i])
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

// withOverlay returns a canvas with the overlay drawn over the page.
func withOverlay(page, overlay *canvas.Canvas) *canvas.Canvas {
	c := canvas.New(page.W, page.H)
	page.RenderTo(c)
	overlay.RenderTo(c)
	return c
}

// writeMaskedPNG rasterizes a page, clears everything outside rect (nil =
// keep all), puts the overlay on top and writes a transparent PNG.
func writeMaskedPNG(path string, page *canvas.Canvas, rect *canvas.Rect, overlay *canvas.Canvas, res canvas.Resolution) error {
	img := rasterizer.Draw(page, res, canvas.DefaultColorSpace)
	if rect != nil {
		dpmm := res.DPMM()
		b := img.Bounds()
		x0, x1 := int(math.Round(rect.X0*dpmm)), int(math.Round(rect.X1*dpmm))
		y0, y1 := int(math.Round(rect.Y0*dpmm)), int(math.Round(rect.Y1*dpmm))
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if x < x0 || x >= x1 || y < y0 || y >= y1 {
					img.SetRGBA(x, y, color.RGBA{})
				}
			}
		}
	}
	if overlay != nil {
		draw.Draw(img, img.Bounds(), rasterizer.Draw(overlay, res, canvas.DefaultColorSpace), image.Point{}, draw.Over)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
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

