package converter

import (
	"testing"

	dxf "github.com/kedazo/dxf-go"
)

func TestLayerFilterMatch(t *testing.T) {
	f := newLayerFilter([]string{"Walls", " Rajz és ábra ", "Méret*"})
	for layer, want := range map[string]bool{
		"Walls":                    true,
		"WALLS":                    true, // layer names are case-insensitive
		"Walls_Pen_No__21":         true, // ArchiCAD pen sublayer
		"Walls2":                   false,
		"Rajz és ábra _Pen_No__41": true, // trailing space before the suffix
		"Méretezés - általános":    true, // glob
		"Méret/Ext":                true, // '/' is an ordinary character
		"Doors":                    false,
	} {
		if got := f.match(layer); got != want {
			t.Errorf("match(%q) = %v, want %v", layer, got, want)
		}
	}
	if newLayerFilter(nil) != nil || newLayerFilter([]string{" "}) != nil {
		t.Error("no patterns must select everything (nil filter)")
	}
	if !(*layerFilter)(nil).match("anything") {
		t.Error("nil filter must match")
	}
}

// ArchiCAD-style structure: the INSERT sits on layer A while its geometry is
// on pen sublayers of A (and on "0"); another layer B holds unrelated lines.
func TestLayerSelectionThroughBlocks(t *testing.T) {
	line := func(layer string, x1, y1, x2, y2 float64) *dxf.Line {
		l := dxf.NewLine()
		l.SetLayer(layer)
		l.P1, l.P2 = dxf.Point{X: x1, Y: y1}, dxf.Point{X: x2, Y: y2}
		return l
	}
	blk := dxf.NewBlock()
	blk.Name = "WALL"
	blk.Entities = []dxf.Entity{
		line("A_Pen_No__1", 0, 0, 10, 0),
		line("0", 0, 0, 0, 5),
		line("C", 0, 0, -3, 0), // a different layer inside the block
	}
	ins := dxf.NewInsert()
	ins.Name = "WALL"
	ins.SetLayer("A")
	ents := []dxf.Entity{ins, line("B", 100, 100, 200, 200), line("C", 50, 50, 60, 50)}
	blocks := map[string]*dxf.Block{"WALL": blk}

	check := func(sel []string, want BBox) {
		t.Helper()
		if got := selectionBBox(ents, blocks, newLayerFilter(sel)); got != want {
			t.Errorf("--layers %q: bbox %+v, want %+v", sel, got, want)
		}
	}
	// Selecting the INSERT's layer selects the whole block instance.
	check([]string{"a"}, BBox{MinX: -3, MinY: 0, MaxX: 10, MaxY: 5})
	// Selecting a pen sublayer shows only that part of the block.
	check([]string{"A_Pen_No__1"}, BBox{MinX: 0, MinY: 0, MaxX: 10, MaxY: 0})
	// A layer used both inside the block and at top level.
	check([]string{"C"}, BBox{MinX: -3, MinY: 0, MaxX: 60, MaxY: 50})
	check([]string{"B"}, BBox{MinX: 100, MinY: 100, MaxX: 200, MaxY: 200})
	check([]string{"nothing"}, NewBBox())
}
