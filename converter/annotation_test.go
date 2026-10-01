package converter

import (
	"testing"

	dxf "github.com/kedazo/dxf-go"
)

func TestLeaderArrows(t *testing.T) {
	d := dxf.NewDrawing()
	d.Header.DimensioningArrowSize, d.Header.DimensioningScaleFactor = 0.18, 0
	rec := dxf.NewBlockRecord()
	rec.Name = "ARROW_X"
	rec.SetHandle(0x2682)
	d.BlockRecords = append(d.BlockRecords, *rec)
	custom := dxf.NewDimStyle()
	custom.Name = "Custom"
	custom.DimensioningArrowSize, custom.DimensioningScaleFactor = 0.1, 2
	custom.DimensionLeaderBlockName = "2682" // handle of the block record
	none := dxf.NewDimStyle()
	none.Name = "None"
	none.DimensionLeaderBlockName = "_NONE"
	d.DimStyles = []dxf.DimStyle{*custom, *none}

	la := newLeaderArrows(d)
	if a := la.arrow("custom"); a.block != "ARROW_X" || !near(a.size, 0.2) || a.none {
		t.Errorf("custom = %+v, want block ARROW_X size 0.2", a)
	}
	if a := la.arrow("None"); !a.none {
		t.Errorf("_NONE = %+v, want no arrowhead", a)
	}
	if a := la.arrow("missing"); !near(a.size, 0.18) || a.block != "" {
		t.Errorf("missing style = %+v, want header size 0.18, default arrow", a)
	}

	// The unit arrow's tip lands on the first vertex, its body toward the second.
	m := arrowAffine(dxf.Point{X: 5, Y: 5}, dxf.Point{X: 5, Y: 0}, 2)
	x, y := m.apply(0, 0)
	assertPoint(t, "tip", x, y, 5, 5)
	x, y = m.apply(-1, 0)
	assertPoint(t, "body", x, y, 5, 3)
}

// A table shows its "*T" block at its insertion point.
func TestTableAsInsert(t *testing.T) {
	blk := dxf.NewBlock()
	blk.Name = "*T1"
	line := dxf.NewLine()
	line.P2 = dxf.Point{X: 5, Y: 2}
	blk.Entities = append(blk.Entities, line)

	table := &dxf.Table{BlockName: "*T1", InsertionPoint: dxf.Point{X: 10, Y: 1}}
	table.SetIsVisible(true)
	table.SetLayer("TABLES")
	bb := ComputeBoundingBox([]dxf.Entity{table}, map[string]*dxf.Block{"*T1": blk})
	if bb != (BBox{MinX: 10, MinY: 1, MaxX: 15, MaxY: 3}) {
		t.Errorf("table bbox = %+v, want (10,1)-(15,3)", bb)
	}
	if ins := tableInsert(table); ins.Layer() != "TABLES" || ins.Name != "*T1" {
		t.Errorf("table insert = %q on %q", ins.Name, ins.Layer())
	}
}

func TestMLeaderParts(t *testing.T) {
	ml := &dxf.MLeader{
		Scale:     1,
		ArrowSize: 0.5,
		Leaders: []dxf.MLeaderLeader{{
			LastLeaderPoint: dxf.Point{X: 10, Y: 10},
			DoglegVector:    dxf.Vector{X: 1},
			DoglegLength:    2,
			Lines:           [][]dxf.Point{{{X: 0, Y: 0}, {X: 5, Y: 8}}},
		}},
		HasText:           true,
		Text:              "Note",
		TextLocation:      dxf.Point{X: 13, Y: 11},
		TextHeight:        1,
		HasBlock:          true,
		BlockRecordHandle: 0x42,
		BlockScale:        dxf.Vector{X: 1, Y: 1, Z: 1},
		BlockLocation:     dxf.Point{X: 20, Y: 20},
		BlockNormal:       dxf.Vector{Z: 1},
	}
	ml.SetIsVisible(true)
	paths, text, block := mleaderParts(ml, map[dxf.Handle]string{0x42: "TAG"})
	want := []dxf.Point{{X: 0, Y: 0}, {X: 5, Y: 8}, {X: 10, Y: 10}, {X: 12, Y: 10}}
	if len(paths) != 1 || len(paths[0]) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[0][i] != want[i] {
			t.Errorf("path point %d = %v, want %v", i, paths[0][i], want[i])
		}
	}
	if text == nil || text.Text != "Note" || text.InsertionPoint != ml.TextLocation {
		t.Errorf("text = %+v", text)
	}
	if block == nil || block.Name != "TAG" || block.Location != (dxf.Point{X: 20, Y: 20}) {
		t.Errorf("content block = %+v", block)
	}

	// Drawn: lines, arrow, text; the frame covers the arrow tip and the text.
	bb := ComputeBoundingBox([]dxf.Entity{ml}, nil)
	if bb.MinX != 0 || bb.MinY != 0 || bb.MaxX <= 13 {
		t.Errorf("multileader bbox = %+v", bb)
	}
	paper := PaperSize{Width: 100, Height: 100}
	r := NewRenderer(paper, false, 0, "")
	r.SetTransform(NewTransform(BBox{MaxX: 100, MaxY: 100}, 1, paper, 0, AlignTopLeft, false))
	renderEntity(r, ml, nil, nil, topCtx)
	r.flush()
	if r.c.Empty() {
		t.Error("nothing drawn")
	}
}

func TestWipeoutPolygon(t *testing.T) {
	w := dxf.NewWipeout()
	w.SetLocation(dxf.Point{X: 10, Y: 20})
	w.SetUVector(dxf.Vector{X: 4})
	w.SetVVector(dxf.Vector{Y: 2})
	w.SetImageSize(dxf.Vector{X: 1, Y: 1})
	w.SetClippingVertices([]dxf.Point{{X: -0.5, Y: -0.5}, {X: 0.5, Y: 0.5}}) // rectangle: whole image

	want := map[[2]float64]bool{{10, 22}: true, {14, 22}: true, {14, 20}: true, {10, 20}: true}
	poly := wipeoutPolygon(w)
	if len(poly) != 4 {
		t.Fatalf("polygon = %v, want 4 corners", poly)
	}
	for _, p := range poly {
		if !want[p] {
			t.Errorf("corner %v not in %v", p, want)
		}
	}
	// Image y runs down: the top-left clip corner (-0.5,-0.5) is at loc + v.
	if poly[0] != [2]float64{10, 22} {
		t.Errorf("first corner = %v, want (10,22)", poly[0])
	}
}
