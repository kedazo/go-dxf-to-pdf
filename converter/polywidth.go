package converter

import (
	"math"

	dxf "github.com/kedazo/dxf-go"
)

// Polylines with widths are drawn as filled outlines: each wide segment is
// a trapezoid (straight) or a band (bulged arc) whose width runs from the
// start to the end width, and the outer gap at each joint is closed with a
// miter (or a bevel where the miter would be too long).

// maxMiter limits a joint's miter, in half-widths, before it is bevelled.
const maxMiter = 4

// lwPolylineEdges returns the edges of an LWPOLYLINE with their widths: a
// vertex's own widths, or the constant width when it has none.
func lwPolylineEdges(e *dxf.LWPolyline) []polylineEdge {
	verts := e.Vertices
	n := len(verts)
	if n < 2 {
		return nil
	}
	count := n - 1
	if e.IsClosed() {
		count = n
	}
	edges := make([]polylineEdge, 0, count)
	for i := 0; i < count; i++ {
		a, b := verts[i], verts[(i+1)%n]
		w0, w1 := a.StartingWidth, a.EndingWidth
		if w0 == 0 && w1 == 0 {
			w0, w1 = e.ConstantWidth, e.ConstantWidth
		}
		edges = append(edges, polylineEdge{X1: a.X, Y1: a.Y, X2: b.X, Y2: b.Y, Bulge: a.Bulge, W0: w0, W1: w1})
	}
	return edges
}

// isWide reports whether the edge has a width.
func (e polylineEdge) isWide() bool {
	return e.W0 > 0 || e.W1 > 0
}

// maxHalfWidth returns half the largest width of the edges.
func maxHalfWidth(edges []polylineEdge) float64 {
	w := 0.0
	for _, e := range edges {
		w = max(w, e.W0, e.W1)
	}
	return w / 2
}

// widePolylinePieces returns the filled outline pieces of the wide edges
// (in the edges' coordinates); arcs are flattened to within tol. closed
// joins the last edge to the first.
func widePolylinePieces(edges []polylineEdge, closed bool, tol float64) [][][2]float64 {
	var pieces [][][2]float64
	for _, e := range edges {
		if p := edgePiece(e, tol); p != nil {
			pieces = append(pieces, p)
		}
	}
	n := len(edges)
	for i := 0; i < n; i++ {
		j := i + 1
		if j == n {
			if !closed {
				break
			}
			j = 0
		}
		if w := joinPiece(edges[i], edges[j]); w != nil {
			pieces = append(pieces, w)
		}
	}
	return pieces
}

// edgePiece returns the outline of one wide edge (nil if it has no width
// or no length).
func edgePiece(e polylineEdge, tol float64) [][2]float64 {
	if !e.isWide() || (e.X1 == e.X2 && e.Y1 == e.Y2) {
		return nil
	}
	h0, h1 := e.W0/2, e.W1/2
	if math.Abs(e.Bulge) <= 1e-10 {
		dx, dy := e.X2-e.X1, e.Y2-e.Y1
		l := math.Hypot(dx, dy)
		nx, ny := -dy/l, dx/l // left normal
		return [][2]float64{
			{e.X1 + nx*h0, e.Y1 + ny*h0}, {e.X2 + nx*h1, e.Y2 + ny*h1},
			{e.X2 - nx*h1, e.Y2 - ny*h1}, {e.X1 - nx*h0, e.Y1 - ny*h0},
		}
	}
	cx, cy, radius, start, sweep := bulgeArc(e.X1, e.Y1, e.X2, e.Y2, e.Bulge)
	step := math.Pi / 8
	if tol > 0 && tol < radius {
		step = min(step, 2*math.Acos(1-tol/radius))
	}
	steps := min(max(int(math.Ceil(math.Abs(sweep)/step)), 2), 512)
	outer := make([][2]float64, 0, 2*(steps+1))
	inner := make([][2]float64, 0, steps+1)
	for k := 0; k <= steps; k++ {
		f := float64(k) / float64(steps)
		s, c := math.Sincos(start + sweep*f)
		h := h0 + (h1-h0)*f
		outer = append(outer, [2]float64{cx + c*(radius+h), cy + s*(radius+h)})
		ri := max(radius-h, 0)
		inner = append(inner, [2]float64{cx + c*ri, cy + s*ri})
	}
	for k := len(inner) - 1; k >= 0; k-- {
		outer = append(outer, inner[k])
	}
	return outer
}

// edgeTangent returns the unit direction of an edge at its start (end =
// false) or end.
func edgeTangent(e polylineEdge, end bool) (float64, float64, bool) {
	if math.Abs(e.Bulge) <= 1e-10 {
		dx, dy := e.X2-e.X1, e.Y2-e.Y1
		l := math.Hypot(dx, dy)
		if l == 0 {
			return 0, 0, false
		}
		return dx / l, dy / l, true
	}
	_, _, _, start, sweep := bulgeArc(e.X1, e.Y1, e.X2, e.Y2, e.Bulge)
	a := start
	if end {
		a += sweep
	}
	s, c := math.Sincos(a)
	if sweep < 0 { // clockwise
		return s, -c, true
	}
	return -s, c, true
}

// joinPiece returns the wedge closing the outer gap where edge a meets
// edge b (nil if they don't turn or aren't both wide there).
func joinPiece(a, b polylineEdge) [][2]float64 {
	h1, h2 := a.W1/2, b.W0/2
	if h1 <= 0 || h2 <= 0 {
		return nil
	}
	t1x, t1y, ok1 := edgeTangent(a, true)
	t2x, t2y, ok2 := edgeTangent(b, false)
	cross := t1x*t2y - t1y*t2x
	if !ok1 || !ok2 || math.Abs(cross) < 1e-9 {
		return nil
	}
	// The outer side is right of a left turn, left of a right turn.
	side := -1.0
	if cross < 0 {
		side = 1
	}
	vx, vy := a.X2, a.Y2
	ax, ay := vx-t1y*side*h1, vy+t1x*side*h1 // a's corner: left normal (-ty, tx)
	bx, by := vx-t2y*side*h2, vy+t2x*side*h2
	// Miter: where a's edge line (ax + s·t1) meets b's (bx - u·t2).
	s := ((bx-ax)*t2y - (by-ay)*t2x) / cross
	mx, my := ax+t1x*s, ay+t1y*s
	if s >= 0 && math.Hypot(mx-vx, my-vy) <= maxMiter*max(h1, h2) {
		return [][2]float64{{vx, vy}, {ax, ay}, {mx, my}, {bx, by}}
	}
	return [][2]float64{{vx, vy}, {ax, ay}, {bx, by}} // bevel
}

// renderPolylineEdges draws polyline edges (in om's source coordinates):
// wide ones as filled outlines in the entity colour, the others as strokes.
func renderPolylineEdges(r *Renderer, edges []polylineEdge, closed bool, om affine, rgb RGB) {
	if maxHalfWidth(edges) == 0 {
		for _, e := range edges {
			drawEdge(r, om, e.X1, e.Y1, e.X2, e.Y2, e.Bulge)
		}
		return
	}
	tol := 0.0 // flatten arcs to ~0.05 mm on paper
	if s := r.transform.Scale * om.linearScale(); s > 0 {
		tol = 0.05 / s
	}
	pieces := widePolylinePieces(edges, closed, tol)
	for _, piece := range pieces {
		for i, p := range piece {
			piece[i][0], piece[i][1] = om.apply(p[0], p[1])
		}
	}
	r.FillUnion(pieces, rgb)
	for _, e := range edges {
		if !e.isWide() {
			drawEdge(r, om, e.X1, e.Y1, e.X2, e.Y2, e.Bulge)
		}
	}
}
