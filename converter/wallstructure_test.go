package converter

import (
	"math"
	"strconv"
	"strings"
	"testing"

	dxf "github.com/kedazo/dxf-go"
)

// structureDrawing is an ArchiCAD-shaped plan in millimetres: one wall
// element (a block holding both faces and its hatch, inserted 1000 mm to the
// right), one zone (a closed polyline) and the zone's stamp (an INSERT with a
// NAME attribute).
func structureDrawing() *dxf.Drawing {
	d := dxf.NewDrawing()
	d.Header.Version = dxf.R2000 // R12 has no LWPOLYLINE
	d.Header.DefaultDrawingUnits = dxf.UnitsMillimeters

	wall := dxf.NewBlock()
	wall.Name = "Wall_1_1"
	for _, y := range []float64{0, 300} {
		l := dxf.NewLine()
		l.SetLayer("0") // inherits the INSERT's layer
		l.P1 = dxf.Point{X: 0, Y: y}
		l.P2 = dxf.Point{X: 4000, Y: y}
		wall.Entities = append(wall.Entities, l)
	}
	h := dxf.NewHatch()
	h.SetLayer("WALLS")
	h.Paths = []dxf.HatchBoundaryPath{{
		PathType: 1 | 2, IsClosed: true,
		Vertices: [][2]float64{{0, 0}, {4000, 0}, {4000, 300}, {0, 300}},
	}}
	wall.Entities = append(wall.Entities, h)
	d.Blocks = append(d.Blocks, *wall)

	stamp := dxf.NewBlock()
	stamp.Name = "ZONE_STAMP"
	d.Blocks = append(d.Blocks, *stamp)

	ins := dxf.NewInsert()
	ins.Name = "Wall_1_1"
	ins.SetLayer("WALLS")
	ins.Location = dxf.Point{X: 1000, Y: 0}
	d.Entities = append(d.Entities, ins)

	zone := dxf.NewLWPolyline()
	zone.SetLayer("ZONES")
	zone.Vertices = []dxf.LwVertex{{X: 1000, Y: 300}, {X: 5000, Y: 300}, {X: 5000, Y: 3300}, {X: 1000, Y: 3300}}
	zone.SetIsClosed(true)
	d.Entities = append(d.Entities, zone)

	st := dxf.NewInsert()
	st.Name = "ZONE_STAMP"
	st.SetLayer("ZONES")
	st.Location = dxf.Point{X: 3000, Y: 1800}
	att := *dxf.NewAttribute()
	att.AttributeTag = "NAME"
	att.Value = "SZOBA 1"
	att.TextHeight = 200
	att.Location = st.Location
	st.Attributes = append(st.Attributes, att)
	st.HasAttributes = true
	d.Entities = append(d.Entities, st)
	return d
}

func parsePoints(t *testing.T, s string) [][2]float64 {
	t.Helper()
	var out [][2]float64
	for _, xy := range strings.Fields(s) {
		parts := strings.Split(xy, ",")
		x, err1 := strconv.ParseFloat(parts[0], 64)
		y, err2 := strconv.ParseFloat(parts[1], 64)
		if len(parts) != 2 || err1 != nil || err2 != nil {
			t.Fatalf("bad point %q", xy)
		}
		out = append(out, [2]float64{x, y})
	}
	return out
}

func TestWallStructureElementsPolygonsStamps(t *testing.T) {
	// Detection restricted to the wall layer: the structure must still carry
	// the zone and its stamp, which live on another layer.
	doc, _ := emitAndParse(t, structureDrawing(), WallSegmentsOptions{Layers: []string{"WALLS"}})

	if doc.Version != "2" {
		t.Errorf("version = %q, want 2", doc.Version)
	}
	if len(doc.Elements) != 2 || doc.Elements[0].Block != "Wall_1_1" ||
		doc.Elements[0].Layer != "WALLS" || doc.Elements[1].Block != "ZONE_STAMP" {
		t.Fatalf("elements = %+v, want [Wall_1_1 on WALLS, ZONE_STAMP]", doc.Elements)
	}
	if len(doc.Segments) != 2 {
		t.Fatalf("segments = %d, want the wall's 2 faces", len(doc.Segments))
	}
	for _, s := range doc.Segments {
		if s.Elem != 0 || s.Layer != "WALLS" {
			t.Errorf("segment %+v: want elem 0 on WALLS", s)
		}
	}

	var zone, hatch *XMLPolygon
	for i := range doc.Polygons {
		switch doc.Polygons[i].Source {
		case "LWPOLYLINE":
			zone = &doc.Polygons[i]
		case "HATCH":
			hatch = &doc.Polygons[i]
		}
	}
	if zone == nil || hatch == nil || len(doc.Polygons) != 2 {
		t.Fatalf("polygons = %+v, want one zone polyline and one hatch loop", doc.Polygons)
	}
	if zone.Layer != "ZONES" || zone.Elem != -1 {
		t.Errorf("zone = %+v, want layer ZONES, elem -1", *zone)
	}
	if hatch.Elem != 0 || hatch.Pattern != "SOLID" {
		t.Errorf("hatch = %+v, want elem 0, pattern SOLID", *hatch)
	}

	// Everything shares the segments' scene frame, whose origin <meta> states
	// (it follows the plotted bbox, so it moves with --layers).
	scene := func(x, y float64) [2]float64 {
		u := doc.Meta.UnitToMeters
		return [2]float64{(x - doc.Meta.OriginDxfX) * u, (doc.Meta.OriginDxfY - y) * u}
	}
	near2 := func(a, b [2]float64) bool {
		return math.Abs(a[0]-b[0]) < 1e-6 && math.Abs(a[1]-b[1]) < 1e-6
	}
	want := [][2]float64{scene(1000, 300), scene(5000, 300), scene(5000, 3300), scene(1000, 3300)}
	got := parsePoints(t, zone.Points)
	if len(got) != len(want) {
		t.Fatalf("zone points = %v, want %v", got, want)
	}
	for i := range want {
		if !near2(got[i], want[i]) {
			t.Errorf("zone point %d = %v, want %v", i, got[i], want[i])
		}
	}
	// The hatch went through the INSERT's translation: x 1000..5000, y 0..300 mm.
	lo, hi := scene(1000, 300), scene(5000, 0)
	for _, p := range parsePoints(t, hatch.Points) {
		if p[0] < lo[0]-1e-6 || p[0] > hi[0]+1e-6 || p[1] < lo[1]-1e-6 || p[1] > hi[1]+1e-6 {
			t.Errorf("hatch point %v outside the wall body %v-%v", p, lo, hi)
		}
	}

	if len(doc.Stamps) != 1 {
		t.Fatalf("stamps = %+v, want 1", doc.Stamps)
	}
	s := doc.Stamps[0]
	if s.Block != "ZONE_STAMP" || s.Layer != "ZONES" || s.Elem != 1 ||
		!near2([2]float64{s.X, s.Y}, scene(3000, 1800)) {
		t.Errorf("stamp = %+v, want ZONE_STAMP on ZONES, elem 1, at %v", s, scene(3000, 1800))
	}
	if len(s.Attrs) != 1 || s.Attrs[0].Tag != "NAME" || s.Attrs[0].Value != "SZOBA 1" {
		t.Errorf("stamp attrs = %+v, want NAME=SZOBA 1", s.Attrs)
	}
}

func TestBulgeEdgeFlattening(t *testing.T) {
	// Half circle of radius 1 from (-1,0) to (1,0), bulge 1 (CCW, apex (0,-1)).
	ring := appendBulgeEdge(nil, -1, 0, 1, 0, 1, 0.01)
	if len(ring) < 8 {
		t.Fatalf("got %d points, want the arc flattened", len(ring))
	}
	for _, p := range ring {
		if r := math.Hypot(p[0], p[1]); math.Abs(r-1) > 1e-9 || p[1] > 1e-9 {
			t.Errorf("point %v not on the lower half circle", p)
		}
	}
}
