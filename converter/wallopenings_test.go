package converter

import (
	"math"
	"testing"

	dxf "github.com/kedazo/dxf-go"
)

// Drawings here are in millimetres; scene points are compared through the
// frame <meta> states, so the plotted bbox may be anything.

func newBlock(d *dxf.Drawing, name string, ents ...dxf.Entity) {
	b := dxf.NewBlock()
	b.Name = name
	b.Entities = ents
	d.Blocks = append(d.Blocks, *b)
}

func line(layer string, x1, y1, x2, y2 float64) *dxf.Line {
	l := dxf.NewLine()
	l.SetLayer(layer)
	l.P1 = dxf.Point{X: x1, Y: y1}
	l.P2 = dxf.Point{X: x2, Y: y2}
	return l
}

func arc(layer string, cx, cy, r, a0, a1 float64) *dxf.Arc {
	a := dxf.NewArc()
	a.SetLayer(layer)
	a.Center = dxf.Point{X: cx, Y: cy}
	a.Radius = r
	a.StartAngle, a.EndAngle = a0, a1
	return a
}

func insert(name, layer string, x, y float64) *dxf.Insert {
	ins := dxf.NewInsert()
	ins.Name = name
	ins.SetLayer(layer)
	ins.Location = dxf.Point{X: x, Y: y}
	return ins
}

// sceneOf maps DXF millimetres to the document's scene frame.
func sceneOf(doc WallSegmentsDoc) func(x, y float64) [2]float64 {
	return func(x, y float64) [2]float64 {
		u := doc.Meta.UnitToMeters
		return [2]float64{(x - doc.Meta.OriginDxfX) * u, (doc.Meta.OriginDxfY - y) * u}
	}
}

func near2(a, b [2]float64) bool {
	return math.Abs(a[0]-b[0]) < 1e-6 && math.Abs(a[1]-b[1]) < 1e-6
}

// archicadOpeningDrawing: a "Wall_1_1" block (both faces with a 900 mm door
// gap, a nested door block with its swing arc and a marker stamp), inserted
// at (1000, 0).
func archicadOpeningDrawing() *dxf.Drawing {
	d := dxf.NewDrawing()
	d.Header.Version = dxf.R2000
	d.Header.DefaultDrawingUnits = dxf.UnitsMillimeters

	label := dxf.NewText()
	label.SetLayer("0")
	label.Location = dxf.Point{X: 5000, Y: 5000} // far away: must not grow the corners
	label.Value = "D1"
	label.Height = 100
	newBlock(d, "ajtó 24",
		line("0", 0, 0, 0, 300), line("0", 900, 0, 900, 300), // jambs
		line("0", 0, 300, 0, 1200),   // leaf
		arc("0", 0, 300, 900, 0, 90), // swing
		label)
	newBlock(d, "ajtó konszignáció 22", line("0", 0, 0, 100, 0))

	marker := insert("ajtó konszignáció 22", "Archicad Door Markers", 450, 600)
	att := *dxf.NewAttribute()
	att.AttributeTag = "AC_MarkerText_2"
	att.Value = "90"
	att.TextHeight = 100
	marker.Attributes = append(marker.Attributes, att)
	marker.HasAttributes = true

	newBlock(d, "Wall_1_1",
		line("0", 0, 0, 1000, 0), line("0", 1900, 0, 4000, 0),
		line("0", 0, 300, 1000, 300), line("0", 1900, 300, 4000, 300),
		insert("ajtó 24", "Archicad Doors", 1000, 0),
		marker)
	d.Entities = append(d.Entities, insert("Wall_1_1", "WALLS", 1000, 0))
	return d
}

func TestOpeningNestedArchicadDoor(t *testing.T) {
	doc, res := emitAndParse(t, archicadOpeningDrawing(), WallSegmentsOptions{})
	scene := sceneOf(doc)

	if doc.Version != "3" {
		t.Errorf("version = %q, want 3", doc.Version)
	}
	if len(doc.Openings) != 1 {
		t.Fatalf("openings = %+v, want the nested door only (not the wall, not the marker)", doc.Openings)
	}
	o := doc.Openings[0]
	if o.ID != 0 || o.Block != "ajtó 24" || o.Layer != "Archicad Doors" || o.Kind != "door" ||
		o.Elem != 0 || o.Depth != 1 || o.NameWidth != 0 {
		t.Errorf("opening = %+v, want door ajtó 24 on Archicad Doors, elem 0, depth 1, no name size", o)
	}
	if !near2([2]float64{o.X, o.Y}, scene(2000, 0)) {
		t.Errorf("insertion = %v,%v, want %v", o.X, o.Y, scene(2000, 0))
	}
	want := [][2]float64{scene(2000, 0), scene(2900, 0), scene(2900, 1200), scene(2000, 1200)}
	got := parsePoints(t, o.Corners)
	if len(got) != 4 {
		t.Fatalf("corners = %q, want 4 points", o.Corners)
	}
	for i := range want {
		if !near2(got[i], want[i]) {
			t.Errorf("corner %d = %v, want %v (text left out)", i, got[i], want[i])
		}
	}

	if len(doc.Arcs) != 1 {
		t.Fatalf("arcs = %+v, want the swing", doc.Arcs)
	}
	a := doc.Arcs[0]
	s45 := 900 * math.Sqrt2 / 2
	if a.Opening != 0 || a.Elem != 0 || math.Abs(a.R-0.9) > 1e-9 ||
		!near2([2]float64{a.CX, a.CY}, scene(2000, 300)) ||
		!near2([2]float64{a.X0, a.Y0}, scene(2900, 300)) ||
		!near2([2]float64{a.XM, a.YM}, scene(2000+s45, 300+s45)) ||
		!near2([2]float64{a.X1, a.Y1}, scene(2000, 1200)) {
		t.Errorf("arc = %+v, want opening 0, r 0.9, centre %v, 0°→90°", a, scene(2000, 300))
	}

	// The marker stays a stamp, with the wall's elem.
	if len(doc.Stamps) != 1 || doc.Stamps[0].Elem != 0 || doc.Stamps[0].Attrs[0].Value != "90" {
		t.Errorf("stamps = %+v, want the marker with elem 0", doc.Stamps)
	}

	// The faces' 900 mm door gap is bridged and recorded.
	if len(doc.Walls) != 1 || len(doc.Gaps) != 1 {
		t.Fatalf("walls = %+v, gaps = %+v, want one wall with one gap", doc.Walls, doc.Gaps)
	}
	w, g := doc.Walls[0], doc.Gaps[0]
	if w.ID != 0 || g.Wall != 0 || math.Abs(g.T1-g.T0-0.9) > 1e-6 {
		t.Errorf("wall %+v gap %+v, want gap of 0.9 m on wall 0", w, g)
	}
	p, q := [2]float64{g.X1, g.Y1}, [2]float64{g.X2, g.Y2}
	if !(near2(p, scene(2000, 150)) && near2(q, scene(2900, 150))) &&
		!(near2(p, scene(2900, 150)) && near2(q, scene(2000, 150))) {
		t.Errorf("gap ends = %v %v, want %v %v", p, q, scene(2000, 150), scene(2900, 150))
	}
	// t runs from the wall's x1,y1.
	at := func(t float64) [2]float64 {
		L := math.Hypot(w.X2-w.X1, w.Y2-w.Y1)
		return [2]float64{w.X1 + (w.X2-w.X1)*t/L, w.Y1 + (w.Y2-w.Y1)*t/L}
	}
	if !near2(at(g.T0), p) || !near2(at(g.T1), q) {
		t.Errorf("gap t0/t1 = %v/%v don't land on its end points", g.T0, g.T1)
	}

	if res.OpeningCount != 1 || res.DoorCount != 1 || res.ArcCount != 1 || res.GapCount != 1 ||
		doc.Meta.OpeningCount != 1 || doc.Meta.ArcCount != 1 || doc.Meta.GapCount != 1 {
		t.Errorf("counts: result %+v meta %+v", res, doc.Meta)
	}
}

func TestOpeningMirroredRotatedInstance(t *testing.T) {
	d := dxf.NewDrawing()
	d.Header.DefaultDrawingUnits = dxf.UnitsMillimeters
	newBlock(d, "Door_1 - 2_-4_ X 6_-8_-437402-1-1st Floor Plan",
		line("0", 0, 0, 1000, 0), arc("0", 0, 0, 1000, 0, 90))
	ins := insert("Door_1 - 2_-4_ X 6_-8_-437402-1-1st Floor Plan", "A-DOOR", 10000, 0)
	ins.XScaleFactor = -1
	ins.Rotation = 90
	d.Entities = append(d.Entities, ins)
	addLine(d, "A-WALL", 0, 3000, 12000, 3000) // something else in the frame

	doc, _ := emitAndParse(t, d, WallSegmentsOptions{})
	scene := sceneOf(doc)
	if len(doc.Openings) != 1 || len(doc.Arcs) != 1 {
		t.Fatalf("openings %+v arcs %+v, want one each", doc.Openings, doc.Arcs)
	}
	o := doc.Openings[0]
	if o.Kind != "door" || o.Depth != 0 || o.Elem != 0 ||
		math.Abs(o.NameWidth-0.7112) > 1e-9 || math.Abs(o.NameHeight-2.032) > 1e-9 {
		t.Errorf("opening = %+v, want a top-level door 0.7112 × 2.032", o)
	}
	// local (x, y) → mirror x → rotate 90° → + (10000, 0): (x, y) ↦ (10000 - y, -x)
	want := [][2]float64{scene(10000, 0), scene(10000, -1000), scene(9000, -1000), scene(9000, 0)}
	got := parsePoints(t, o.Corners)
	for i := range want {
		if i >= len(got) || !near2(got[i], want[i]) {
			t.Errorf("corner %d = %v, want %v", i, got, want[i])
		}
	}
	a := doc.Arcs[0]
	s45 := 1000 * math.Sqrt2 / 2
	if !near2([2]float64{a.X0, a.Y0}, scene(10000, -1000)) ||
		!near2([2]float64{a.XM, a.YM}, scene(10000-s45, -s45)) ||
		!near2([2]float64{a.X1, a.Y1}, scene(9000, 0)) || math.Abs(a.R-1) > 1e-9 {
		t.Errorf("arc = %+v, want start %v mid %v end %v", a, scene(10000, -1000),
			scene(10000-s45, -s45), scene(9000, 0))
	}
}

func TestOpeningKindsAndExclusions(t *testing.T) {
	d := dxf.NewDrawing()
	d.Header.DefaultDrawingUnits = dxf.UnitsMillimeters
	newBlock(d, "ablak 24", line("0", 0, 0, 1200, 0))
	newBlock(d, "ajtó felülvilágítóval 24", line("0", 0, 0, 900, 0), insert("ablak 24", "0", 0, 2000),
		arc("0", 0, 0, 900, 0, 90))
	newBlock(d, "padló nyílásszimbólum 24", line("0", 0, 0, 500, 500))
	newBlock(d, "Window_5 - 3_-8_ x 3_-0_-437500-1-1st Floor Plan", line("0", 0, 0, 1117, 0))
	newBlock(d, "Simple Door Opening", line("0", 0, 0, 800, 0))
	newBlock(d, "Wall_2_1", line("0", 0, 0, 3000, 0))
	newBlock(d, "GENERIC", line("0", 0, 0, 900, 0))

	d.Entities = append(d.Entities,
		insert("ajtó felülvilágítóval 24", "Archicad Doors", 0, 0),
		insert("padló nyílásszimbólum 24", "Archicad Doors", 3000, 0),
		insert("Window_5 - 3_-8_ x 3_-0_-437500-1-1st Floor Plan", "A-GLAZ", 6000, 0),
		insert("Simple Door Opening", "Archicad Doors", 9000, 0),
		insert("Wall_2_1", "Archicad Doors", 0, 5000),
		insert("GENERIC", "A-GLAZ-SILL", 12000, 0),
		insert("GENERIC", "FURNITURE", 15000, 0),
		arc("A-DOOR", 20000, 0, 900, 90, 180),    // loose swing on a door layer
		arc("FURNITURE", 22000, 0, 900, 90, 180), // not evidence
	)
	doc, res := emitAndParse(t, d, WallSegmentsOptions{})

	type ok struct{ block, kind string }
	want := []ok{
		{"ajtó felülvilágítóval 24", "door"}, // the nested "ablak 24" is part of it
		{"Window_5 - 3_-8_ x 3_-0_-437500-1-1st Floor Plan", "window"},
		{"Simple Door Opening", "door"},
		{"GENERIC", "window"}, // by layer
	}
	if len(doc.Openings) != len(want) {
		t.Fatalf("openings = %+v, want %v", doc.Openings, want)
	}
	for i, w := range want {
		if o := doc.Openings[i]; o.Block != w.block || o.Kind != w.kind || o.ID != i {
			t.Errorf("opening %d = %+v, want %v", i, o, w)
		}
	}
	if w := doc.Openings[1]; math.Abs(w.NameWidth-1.1176) > 1e-9 || math.Abs(w.NameHeight-0.9144) > 1e-9 {
		t.Errorf("window name size = %v × %v, want 1.1176 × 0.9144", w.NameWidth, w.NameHeight)
	}
	if len(doc.Arcs) != 2 || doc.Arcs[0].Opening != 0 || doc.Arcs[1].Opening != -1 || doc.Arcs[1].Layer != "A-DOOR" {
		t.Errorf("arcs = %+v, want the door's (opening 0) and the loose A-DOOR one (-1)", doc.Arcs)
	}
	if res.DoorCount != 2 || res.WindowCount != 2 || res.UnknownCount != 0 {
		t.Errorf("result = %+v, want 2 doors, 2 windows", res)
	}
}

func TestOpeningKind(t *testing.T) {
	cases := []struct {
		name, layer, kind string
	}{
		{"ajtó 24_3", "Archicad Doors", "door"},
		{"ablak 24_5", "Archicad Windows", "window"},
		{"ajtó felülvilágítóval 24", "", "door"},
		{"BLK1", "A-DOOR", "door"},
		{"BLK1", "A-GLAZ", "window"},
		{"Nyílászáró 3", "", "unknown"},
		{"AJT2 kétszárnyú 01 13", "0", "door"},
		{"AJT1 egyszárnyú 01 13_2", "0", "door"},
		{"fajta 2", "0", ""},
		{"rajta", "0", ""},
		{"ablak konszignáció 22", "Archicad Window Markers", ""},
		{"BLK1", "Archicad Door Markers", ""},
		{"padló nyílásszimbólum 24", "Archicad Doors", ""},
		{"Wall_1_1", "Archicad Doors", ""},
		{"Door Tag", "A-DOOR", ""},
		{"BLK1", "WALLS", ""},
	}
	for _, c := range cases {
		k, ok := openingKind(c.name, c.layer)
		if ok != (c.kind != "") || k != c.kind {
			t.Errorf("openingKind(%q, %q) = %q, %v; want %q", c.name, c.layer, k, ok, c.kind)
		}
	}
}

func TestParseOpeningSize(t *testing.T) {
	cases := []struct {
		name string
		w, h float64
	}{
		{"Door_1 - 2_-4_ X 6_-8_-437402-1-1st Floor Plan", 0.7112, 2.032},
		{"Door_2 - 4_-8_ X 6_-8_", 1.4224, 2.032},
		{"door 90x210", 0.9, 2.1},
		{"ablak 1200x1500", 1.2, 1.5},
		{"ajtó 90/210", 0.9, 2.1},
		{"ajtó 24", 0, 0},
		{"ajtó 24_3", 0, 0},
		{"door 10x10", 0, 0}, // 0.1 m: not a door size
	}
	for _, c := range cases {
		w, h, ok := parseOpeningSize(c.name)
		if ok != (c.w > 0) || math.Abs(w-c.w) > 1e-9 || math.Abs(h-c.h) > 1e-9 {
			t.Errorf("parseOpeningSize(%q) = %v, %v, %v; want %v × %v", c.name, w, h, ok, c.w, c.h)
		}
	}
}

func TestWallGapRecords(t *testing.T) {
	pair := func(d *dxf.Drawing, x0, x1 float64) {
		addLine(d, "WALL", x0, 0, x1, 0)
		addLine(d, "WALL", x0, 300, x1, 300)
	}
	t.Run("bridged", func(t *testing.T) {
		d := dxf.NewDrawing()
		d.Header.DefaultDrawingUnits = dxf.UnitsMillimeters
		pair(d, 0, 2000)
		pair(d, 2900, 5000)
		doc, _ := emitAndParse(t, d, WallSegmentsOptions{})
		if len(doc.Walls) != 1 || len(doc.Gaps) != 1 {
			t.Fatalf("walls %+v gaps %+v, want one wall, one gap", doc.Walls, doc.Gaps)
		}
		g := doc.Gaps[0]
		lo, hi := math.Min(g.T0, g.T1), math.Max(g.T0, g.T1)
		w := doc.Walls[0]
		if g.Wall != w.ID || math.Abs(hi-lo-0.9) > 1e-6 || (math.Abs(lo-2) > 1e-6 && math.Abs(lo-2.1) > 1e-6) {
			t.Errorf("gap = %+v on wall %+v, want 2.0..2.9 from one end", g, w)
		}
	})
	t.Run("too wide", func(t *testing.T) {
		d := dxf.NewDrawing()
		d.Header.DefaultDrawingUnits = dxf.UnitsMillimeters
		pair(d, 0, 2000)
		pair(d, 3500, 5000)
		doc, _ := emitAndParse(t, d, WallSegmentsOptions{})
		if len(doc.Walls) != 2 || len(doc.Gaps) != 0 {
			t.Errorf("walls %+v gaps %+v, want two walls, no gap", doc.Walls, doc.Gaps)
		}
	})
	t.Run("ids after a dropped run", func(t *testing.T) {
		opts := WallSegmentsOptions{}
		opts.applyDefaults()
		pw := func(x0, y0, x1, y1 float64) pairWall {
			L := math.Hypot(x1-x0, y1-y0)
			return pairWall{x1: x0, y1: y0, x2: x1, y2: y1, thickness: 0.3,
				ux: (x1 - x0) / L, uy: (y1 - y0) / L, length: L, layer: "WALL"}
		}
		walls, gaps := mergeCollinear([]pairWall{
			pw(0, 0, 0, 2), pw(0, 3, 0, 5), // vertical: one wall, gap 2..3
			pw(10, 0, 10.03, 0),                  // a 3 cm run alone: dropped
			pw(15, 0, 17, 0), pw(17.5, 0, 19, 0), // gap 17..17.5
		}, opts)
		if len(walls) != 2 || len(gaps) != 2 {
			t.Fatalf("walls %+v gaps %+v, want 2 and 2", walls, gaps)
		}
		for i, w := range walls {
			if w.ID != i {
				t.Errorf("wall %d has id %d", i, w.ID)
			}
		}
		if gaps[0].Wall != 0 || gaps[1].Wall != 1 || gaps[1].T0 != 2 || gaps[1].T1 != 2.5 ||
			gaps[1].X1 != 17 || gaps[1].X2 != 17.5 {
			t.Errorf("gaps = %+v, want wall 0 (2..3) and wall 1 (t 2..2.5, x 17..17.5)", gaps)
		}
	})
}
