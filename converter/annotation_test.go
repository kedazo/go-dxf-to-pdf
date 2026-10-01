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
