package converter

import (
	"math"
	"strconv"
	"strings"

	dxf "github.com/kedazo/dxf-go"
)

// The --emit-wall-segments output beyond loose line segments: the structure a
// CAD export already carries and BuildingDesigner can decide from. This file
// only EXTRACTS; it makes no wall or room decision.
//
//   - elements: every top-level INSERT, so segments, polygons and stamps can be
//     grouped by the drawing element they belong to (ArchiCAD writes one block
//     per wall, "Wall_N_M", holding both faces, the hatch and its openings);
//   - polygons: closed (LW)POLYLINEs and HATCH boundary loops (ArchiCAD zones
//     are closed polylines; wall poché hatches outline the wall body);
//   - stamps: INSERTs that carry ATTRIBs (an ArchiCAD zone stamp holds the
//     room's name and area).
//
// Unlike detection, this ignores --layers: zone layers are exactly the layers
// a wall-layer selection leaves out. Only non-plotting layers are skipped.

// XMLElement is one top-level INSERT.
type XMLElement struct {
	ID    int    `xml:"id,attr"`
	Block string `xml:"block,attr"`
	Layer string `xml:"layer,attr"`
}

// XMLPolygon is a closed ring in scene meters (Y-down), as "x,y x,y ...".
type XMLPolygon struct {
	Layer   string `xml:"layer,attr"`
	Source  string `xml:"source,attr"` // LWPOLYLINE | POLYLINE | HATCH
	Pattern string `xml:"pattern,attr,omitempty"`
	Elem    int    `xml:"elem,attr"` // top-level element id, -1 = none
	Points  string `xml:"points,attr"`
}

// XMLStamp is an INSERT with attributes, at its insertion point.
type XMLStamp struct {
	Block string    `xml:"block,attr"`
	Layer string    `xml:"layer,attr"`
	X     float64   `xml:"x,attr"`
	Y     float64   `xml:"y,attr"`
	Elem  int       `xml:"elem,attr"`
	Attrs []XMLAttr `xml:"attr"`
}

// XMLAttr is one ATTRIB of a stamp.
type XMLAttr struct {
	Tag   string `xml:"tag,attr"`
	Value string `xml:"value,attr"`
}

// wallStructure accumulates the extracted structure in DXF world coordinates.
type wallStructure struct {
	elements []XMLElement
	polygons []rawPolygon
	stamps   []rawStamp
}

type rawPolygon struct {
	pts     [][2]float64
	layer   string
	source  string
	pattern string
	elem    int
}

type rawStamp struct {
	x, y  float64
	block string
	layer string
	elem  int
	attrs []XMLAttr
}

// collectStructure walks one top-level entity (index elem) and everything it
// inserts, appending closed polygons and attributed INSERTs to out.
func collectStructure(out *wallStructure, ent dxf.Entity, elem int, layers map[string]dxf.Layer,
	blocks map[string]*dxf.Block, ctx drawCtx, flatTol float64) {

	if ctx.depth > maxBlockDepth || !ent.IsVisible() || ctx.hidden(ent) {
		return
	}
	layer := ctx.effectiveLayer(ent)
	m := ctx.m

	switch e := ent.(type) {
	case *dxf.LWPolyline:
		if !e.IsClosed() || len(e.Vertices) < 3 {
			return
		}
		om := m.mul(ocsAffine(e.ExtrusionDirection, e.Elevation()))
		var ring [][2]float64
		n := len(e.Vertices)
		for i := 0; i < n; i++ {
			a, b := e.Vertices[i], e.Vertices[(i+1)%n]
			ring = appendBulgeEdge(ring, a.X, a.Y, b.X, b.Y, a.Bulge, flatTol)
		}
		out.addPolygon(om, ring, layer, "LWPOLYLINE", "", elem)

	case *dxf.Polyline:
		if !e.IsClosed() || e.IsPolyfaceMesh() || e.Is3DPolygonMesh() {
			return
		}
		om := m.mul(polyline2DAffine(e))
		var ring [][2]float64
		for _, edge := range simplePolylineEdges(e) {
			ring = appendBulgeEdge(ring, edge.X1, edge.Y1, edge.X2, edge.Y2, edge.Bulge, flatTol)
		}
		if len(ring) >= 3 {
			out.addPolygon(om, ring, layer, "POLYLINE", "", elem)
		}

	case *dxf.Hatch:
		om := m.mul(ocsAffine(e.ExtrusionDirection, e.Elevation()))
		pattern := e.PatternName
		if e.SolidFill {
			pattern = "SOLID"
		}
		for _, poly := range hatchPolygons(e, flatTol) {
			out.addPolygon(om, poly, layer, "HATCH", pattern, elem)
		}

	case *dxf.Insert:
		if len(e.Attributes) > 0 {
			st := rawStamp{block: e.Name, layer: layer, elem: elem}
			st.x, st.y = m.mul(ocsAffine(e.ExtrusionDirection, e.Location.Z)).apply(e.Location.X, e.Location.Y)
			for i := range e.Attributes {
				a := &e.Attributes[i]
				st.attrs = append(st.attrs, XMLAttr{Tag: a.AttributeTag, Value: strings.TrimSpace(a.PlainText())})
			}
			out.stamps = append(out.stamps, st)
		}
		if blk, ok := blocks[e.Name]; ok {
			for _, local := range insertInstances(e, blk.BasePoint) {
				cctx := ctx.inner(e, local)
				for _, be := range blk.Entities {
					collectStructure(out, be, elem, layers, blocks, cctx, flatTol)
				}
			}
		}
	}
}

func (s *wallStructure) addPolygon(m affine, ring [][2]float64, layer, source, pattern string, elem int) {
	if len(ring) < 3 {
		return
	}
	pts := make([][2]float64, len(ring))
	for i, p := range ring {
		pts[i][0], pts[i][1] = m.apply(p[0], p[1])
	}
	s.polygons = append(s.polygons, rawPolygon{pts: pts, layer: layer, source: source, pattern: pattern, elem: elem})
}

// appendBulgeEdge appends the start of edge a→b to ring, flattening a bulged
// (arc) edge into chords no farther than tol from the arc. The edge's end
// point is the next edge's start, so it is not appended.
func appendBulgeEdge(ring [][2]float64, x1, y1, x2, y2, bulge, tol float64) [][2]float64 {
	ring = append(ring, [2]float64{x1, y1})
	if math.Abs(bulge) < 1e-10 || math.Hypot(x2-x1, y2-y1) < 1e-12 {
		return ring
	}
	cx, cy, r, start, sweep := bulgeArc(x1, y1, x2, y2, bulge)
	n := 8
	if tol > 0 && r > tol {
		// chord sagitta r(1-cos(θ/2)) <= tol
		step := 2 * math.Acos(1-tol/r)
		if step > 0 {
			n = int(math.Ceil(math.Abs(sweep) / step))
		}
	}
	n = max(2, min(n, 256))
	for i := 1; i < n; i++ {
		t := start + sweep*float64(i)/float64(n)
		ring = append(ring, [2]float64{cx + r*math.Cos(t), cy + r*math.Sin(t)})
	}
	return ring
}

// structureXML maps the collected structure to scene meters (sx, sy are the
// same DXF→scene functions the segments use).
func structureXML(s *wallStructure, sx, sy func(float64) float64) ([]XMLPolygon, []XMLStamp) {
	polys := make([]XMLPolygon, 0, len(s.polygons))
	for _, p := range s.polygons {
		var b strings.Builder
		for i, v := range p.pts {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(strconv.FormatFloat(round6(sx(v[0])), 'f', -1, 64))
			b.WriteByte(',')
			b.WriteString(strconv.FormatFloat(round6(sy(v[1])), 'f', -1, 64))
		}
		polys = append(polys, XMLPolygon{Layer: p.layer, Source: p.source, Pattern: p.pattern,
			Elem: p.elem, Points: b.String()})
	}
	stamps := make([]XMLStamp, 0, len(s.stamps))
	for _, st := range s.stamps {
		stamps = append(stamps, XMLStamp{Block: st.block, Layer: st.layer,
			X: round6(sx(st.x)), Y: round6(sy(st.y)), Elem: st.elem, Attrs: st.attrs})
	}
	return polys, stamps
}
