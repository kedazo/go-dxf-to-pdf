package converter

import (
	"math"

	dxf "github.com/kedazo/dxf-go"
)

// mlineElements returns the drawn pieces of a MLINE's elements (WCS). Each
// element at a vertex lies along the vertex's miter direction, at its miter
// offset (in drawing units, justification and scale already applied). Along
// a segment an element starts at its start position and, where MLEDIT cut
// it, stops and starts again at the listed positions; a last start runs on to
// the segment's end. Unbroken elements come out
// as one path through all vertices, so they keep their joins.
//
// The MLINESTYLE (caps, fills, element colours and line types) isn't read,
// so the elements are drawn in the MLINE's own style.
func mlineElements(e *dxf.MLine) [][]dxf.Point {
	nv := len(e.Vertices)
	if nv < 2 || len(e.ElementParameters) < nv || len(e.MiterDirections) < nv {
		return nil
	}
	ne := len(e.ElementParameters[0])
	for _, els := range e.ElementParameters[:nv] {
		ne = min(ne, len(els))
	}
	at := func(i, j int) (dxf.Point, bool) {
		line := e.ElementParameters[i][j].Line
		if len(line) == 0 {
			return dxf.Point{}, false
		}
		v, d := e.Vertices[i], e.MiterDirections[i]
		return dxf.Point{X: v.X + d.X*line[0], Y: v.Y + d.Y*line[0], Z: v.Z + d.Z*line[0]}, true
	}
	segments := nv - 1
	if e.IsClosed() {
		segments = nv
	}

	var paths [][]dxf.Point
	for j := 0; j < ne; j++ {
		var cur []dxf.Point // the path being extended
		end := func() {
			if len(cur) >= 2 {
				paths = append(paths, cur)
			}
			cur = nil
		}
		for i := 0; i < segments; i++ {
			a, okA := at(i, j)
			b, okB := at((i+1)%nv, j)
			if !okA || !okB {
				end()
				continue
			}
			dx, dy, dz := b.X-a.X, b.Y-a.Y, b.Z-a.Z
			length := math.Sqrt(dx*dx + dy*dy + dz*dz)
			if length == 0 {
				continue
			}
			point := func(s float64) dxf.Point {
				t := s / length
				return dxf.Point{X: a.X + dx*t, Y: a.Y + dy*t, Z: a.Z + dz*t}
			}
			for _, piece := range mlinePieces(e.ElementParameters[i][j].Line, length) {
				if piece[0] > 1e-9 || cur == nil {
					end()
					cur = []dxf.Point{point(piece[0])}
				}
				cur = append(cur, point(piece[1]))
				if piece[1] < length-1e-9 {
					end()
				}
			}
		}
		end()
	}
	return paths
}

// mlinePieces returns the drawn intervals of an element along a segment of
// the given length, from its parameters: the miter offset, then positions
// measured from the element's miter point where it starts, stops, starts
// again… (an AutoCAD tee joint stores [-2.13, 0, 3.46, 5.06] for a gap from
// 3.46 to 5.06). A last start runs to the segment's end.
func mlinePieces(line []float64, length float64) [][2]float64 {
	if len(line) <= 1 {
		return [][2]float64{{0, length}}
	}
	var pieces [][2]float64
	pos := line[1:]
	for k := 0; k < len(pos); k += 2 {
		s, t := max(pos[k], 0), length
		if k+1 < len(pos) {
			t = min(pos[k+1], length)
		}
		if t > s {
			pieces = append(pieces, [2]float64{s, t})
		}
	}
	return pieces
}

// renderMLine draws a MLINE's element lines.
func renderMLine(r *Renderer, e *dxf.MLine, m affine) {
	for _, line := range mlineElements(e) {
		pts := make([][2]float64, len(line))
		for i, p := range line {
			pts[i][0], pts[i][1] = m.apply(p.X, p.Y)
		}
		closed := len(line) > 2 && line[0] == line[len(line)-1]
		if closed {
			pts = pts[:len(pts)-1]
		}
		r.BreakPath()
		r.DrawPolyline(pts, closed)
	}
}
