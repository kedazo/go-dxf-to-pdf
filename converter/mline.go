package converter

import (
	dxf "github.com/kedazo/dxf-go"
)

// mlineElements returns the lines of a MLINE's elements, through all its
// vertices (WCS). Each element at a vertex lies along the vertex's miter
// direction, at a distance that is the element's first parameter (in drawing
// units, justification and scale already applied).
//
// The dxf package keeps the element parameters in one flat list, so each
// element is assumed to have the same number of them; the MLINESTYLE (caps,
// fills, element colours and line types) isn't read at all, so only the
// element lines are drawn, in the MLINE's own style. ok is false if the
// parameters don't fit that layout.
func mlineElements(e *dxf.MLine) (lines [][]dxf.Point, ok bool) {
	nv, ne := len(e.Vertices), e.StyleElementCount
	if nv < 2 || ne < 1 || len(e.MiterDirections) < nv || len(e.Parameters) == 0 || len(e.Parameters)%(nv*ne) != 0 {
		return nil, false
	}
	k := len(e.Parameters) / (nv * ne) // parameters per element and vertex
	lines = make([][]dxf.Point, ne)
	for j := range lines {
		lines[j] = make([]dxf.Point, nv)
		for i := 0; i < nv; i++ {
			off := e.Parameters[(i*ne+j)*k]
			v, d := e.Vertices[i], e.MiterDirections[i]
			lines[j][i] = dxf.Point{X: v.X + d.X*off, Y: v.Y + d.Y*off, Z: v.Z + d.Z*off}
		}
	}
	return lines, true
}

// renderMLine draws a MLINE's element lines.
func renderMLine(r *Renderer, e *dxf.MLine, m affine) {
	lines, ok := mlineElements(e)
	if !ok {
		return
	}
	for _, line := range lines {
		pts := make([][2]float64, len(line))
		for i, p := range line {
			pts[i][0], pts[i][1] = m.apply(p.X, p.Y)
		}
		r.BreakPath()
		r.DrawPolyline(pts, e.IsClosed())
	}
}
