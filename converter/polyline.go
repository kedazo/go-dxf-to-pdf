package converter

import (
	dxf "github.com/kedazo/dxf-go"
)

// polylineEdge is one drawable edge of a POLYLINE, in the entity's own
// coordinates. Bulge != 0 means the edge is an arc (same meaning as on
// LWPOLYLINE vertices).
type polylineEdge struct {
	X1, Y1, X2, Y2 float64
	Bulge          float64
}

// polylineEdges flattens a POLYLINE into its visible edges. It understands the
// three POLYLINE flavours:
//   - polyface meshes (flag 64): faces come from the parser's PolyfaceFaces
//     (face records sit at 0,0,0 and must not be drawn as vertices); hidden
//     edges are skipped and edges shared by adjacent faces are emitted once.
//   - 3D polygon meshes (flag 16): an M×N vertex grid, optionally closed in
//     the M and/or N direction.
//   - plain 2D/3D polylines, including the closing edge for closed ones and
//     only the fitted vertices for spline-fit polylines.
func polylineEdges(p *dxf.Polyline) []polylineEdge {
	switch {
	case p.IsPolyfaceMesh():
		return polyfaceEdges(p)
	case p.Is3DPolygonMesh():
		return polygonMeshEdges(p)
	default:
		return simplePolylineEdges(p)
	}
}

func simplePolylineEdges(p *dxf.Polyline) []polylineEdge {
	// Spline-fit polylines carry both the frame control points and the fitted
	// vertices; only the fitted ones describe the visible curve.
	verts := make([]dxf.Vertex, 0, len(p.Vertices))
	for _, v := range p.Vertices {
		if v.IsSplineFrameControlPoint() {
			continue
		}
		verts = append(verts, v)
	}
	n := len(verts)
	if n < 2 {
		return nil
	}

	count := n - 1
	if p.IsClosed() {
		count = n
	}
	edges := make([]polylineEdge, 0, count)
	for i := 0; i < count; i++ {
		a, b := verts[i], verts[(i+1)%n]
		edges = append(edges, polylineEdge{
			X1: a.Location.X, Y1: a.Location.Y,
			X2: b.Location.X, Y2: b.Location.Y,
			Bulge: a.Bulge,
		})
	}
	return edges
}

func polyfaceEdges(p *dxf.Polyline) []polylineEdge {
	type edgeKey struct{ a, b int }
	seen := make(map[edgeKey]bool)
	var edges []polylineEdge
	for _, f := range p.PolyfaceFaces() {
		n := len(f.Indices)
		for i := range n {
			if !f.EdgeVisible[i] {
				continue
			}
			a, b := f.Indices[i], f.Indices[(i+1)%n]
			if a == b {
				continue
			}
			k := edgeKey{min(a, b), max(a, b)}
			if seen[k] {
				continue // shared by adjacent faces
			}
			seen[k] = true
			pa, pb := f.Points[i], f.Points[(i+1)%n]
			edges = append(edges, polylineEdge{X1: pa.X, Y1: pa.Y, X2: pb.X, Y2: pb.Y})
		}
	}
	return edges
}

// meshEdges returns the edges of a MESH: its explicit edges, or else the
// edges of its faces (each shared edge once).
func meshEdges(m *dxf.Mesh) []polylineEdge {
	vertex := func(i int) (dxf.Point, bool) {
		if i < 0 || i >= len(m.Vertices) {
			return dxf.Point{}, false
		}
		return m.Vertices[i], true
	}
	var edges []polylineEdge
	add := func(a, b int) {
		pa, okA := vertex(a)
		pb, okB := vertex(b)
		if okA && okB && a != b {
			edges = append(edges, polylineEdge{X1: pa.X, Y1: pa.Y, X2: pb.X, Y2: pb.Y})
		}
	}
	if len(m.Edges) > 0 {
		for _, e := range m.Edges {
			add(e[0], e[1])
		}
		return edges
	}
	type edgeKey struct{ a, b int }
	seen := make(map[edgeKey]bool)
	for _, f := range m.Faces {
		for i := range f {
			a, b := f[i], f[(i+1)%len(f)]
			if k := (edgeKey{min(a, b), max(a, b)}); !seen[k] {
				seen[k] = true
				add(a, b)
			}
		}
	}
	return edges
}

// attdefText returns what an ATTDEF shows: inside a block only a constant
// one shows its value (the others become ATTRIBs on the INSERT); one lying
// loose in a drawing shows its tag, as CAD programs do.
func attdefText(e *dxf.AttributeDefinition, inBlock bool) (text string, ok bool) {
	switch {
	case e.IsInvisible():
		return "", false
	case inBlock && e.IsConstant():
		return e.PlainText(), true
	case inBlock:
		return "", false
	}
	return e.TextTag, e.TextTag != ""
}

func polygonMeshEdges(p *dxf.Polyline) []polylineEdge {
	grid := p.PolygonMeshGrid()
	if grid == nil {
		return simplePolylineEdges(p)
	}
	m, n := len(grid), len(grid[0])
	at := func(i, j int) dxf.Point { return grid[i][j] }
	closedM := p.IsClosed()
	closedN := p.IsPolygonMeshClosedInNDirection()

	var edges []polylineEdge
	add := func(a, b dxf.Point) {
		edges = append(edges, polylineEdge{X1: a.X, Y1: a.Y, X2: b.X, Y2: b.Y})
	}
	for i := 0; i < m; i++ {
		for j := 0; j < n; j++ {
			if j+1 < n {
				add(at(i, j), at(i, j+1))
			} else if closedN && n > 2 {
				add(at(i, j), at(i, 0))
			}
			if i+1 < m {
				add(at(i, j), at(i+1, j))
			} else if closedM && m > 2 {
				add(at(i, j), at(0, j))
			}
		}
	}
	return edges
}
