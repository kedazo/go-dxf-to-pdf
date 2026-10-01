package converter

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	dxf "github.com/kedazo/dxf-go"
)

func TestAffineInverse(t *testing.T) {
	m := translateAffine(5, -3).mul(rotateAffine(30)).mul(scaleAffine(2, -1))
	x, y := m.inverse().apply(m.apply(7, 11))
	assertPoint(t, "round trip", x, y, 7, 11)
}

func TestClipToPolygon(t *testing.T) {
	square := [][2]float64{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	var got [][4]float64
	clipToPolygon([][4]float64{{-5, 5, 15, 5}, {2, 2, 3, 3}, {20, 20, 30, 30}}, square, func(x1, y1, x2, y2 float64) {
		got = append(got, [4]float64{x1, y1, x2, y2})
	})
	want := [][4]float64{{0, 5, 10, 5}, {2, 2, 3, 3}}
	if len(got) != len(want) {
		t.Fatalf("clipped = %v, want %v", got, want)
	}
	for i := range want {
		for k := range want[i] {
			if math.Abs(got[i][k]-want[i][k]) > 1e-9 {
				t.Errorf("piece %d = %v, want %v", i, got[i], want[i])
			}
		}
	}

	if !polygonsIntersect([][2]float64{{9, 9}, {20, 9}, {20, 20}, {9, 20}}, square) ||
		polygonsIntersect([][2]float64{{11, 0}, {20, 0}, {20, 5}}, square) ||
		!polygonsIntersect([][2]float64{{-1, 4}, {11, 4}, {11, 6}, {-1, 6}}, square) { // crossing bar, no vertex inside
		t.Error("polygonsIntersect")
	}
}

// A viewport shows model space scaled, turned and clipped to its outline.
func TestRenderViewport(t *testing.T) {
	model := dxf.NewLine() // long line through the view target
	model.P1, model.P2 = dxf.Point{X: -100, Y: 0}, dxf.Point{X: 100, Y: 0}

	v := dxf.NewViewport()
	v.ID, v.Status = 2, 1
	v.Center = dxf.Point{X: 50, Y: 50} // paper mm
	v.Width, v.Height = 40, 20
	v.ViewTarget = dxf.Point{X: 0, Y: 0}
	v.ViewHeight = 2 // model units → 10 paper units per model unit
	v.TwistAngle = 90
	v.SetLayer("VIEWPORTS") // not plotted: no border, the view still shows
	model.SetLayer("PLAN")
	layers := map[string]dxf.Layer{"VIEWPORTS": {Name: "VIEWPORTS"}, "PLAN": {Name: "PLAN", IsLayerPlotted: true}}

	paper := PaperSize{Width: 100, Height: 100}
	r := NewRenderer(paper, false, 0, "")
	r.SetTransform(NewTransform(BBox{MaxX: 100, MaxY: 100}, 1, paper, 0, AlignTopLeft, false))
	ctx := plotted(nil, layers)
	ctx.vp = &viewportSource{entities: []dxf.Entity{model}, boxes: entityBoxes([]dxf.Entity{model}, nil, nil, layers)}
	renderEntity(r, v, layers, nil, ctx)
	r.flush()

	// Turned 90°, the model X axis runs up the sheet through the center, cut
	// to the outline's 20 mm height.
	b := r.c.Bounds() // canvas-native Y-up
	if math.Abs(b.X0-50) > 0.5 || math.Abs(b.X1-50) > 0.5 || math.Abs(b.Y0-40) > 0.5 || math.Abs(b.Y1-60) > 0.5 {
		t.Errorf("drawn bounds %v, want the vertical x=50, y 40..60", b)
	}
}

func TestPlotHiddenLayers(t *testing.T) {
	layers := map[string]dxf.Layer{
		"PLOT":      {Name: "PLOT", IsLayerPlotted: true},
		"NOPLOT":    {Name: "NOPLOT", IsLayerPlotted: false},
		"FROZEN":    {Name: "FROZEN", IsLayerPlotted: true, Flags: 1},
		"Defpoints": {Name: "Defpoints", IsLayerPlotted: true},
	}
	line := func(layer string, x float64) *dxf.Line {
		l := dxf.NewLine()
		l.SetLayer(layer)
		l.P1, l.P2 = dxf.Point{X: x}, dxf.Point{X: x, Y: 1}
		return l
	}
	blk := dxf.NewBlock()
	blk.Name = "B"
	blk.Entities = []dxf.Entity{line("PLOT", 50)}
	ins := func(layer string) *dxf.Insert {
		i := dxf.NewInsert()
		i.Name = "B"
		i.SetLayer(layer)
		return i
	}
	ents := []dxf.Entity{line("PLOT", 0), line("NOPLOT", 10), line("FROZEN", 20), line("Defpoints", 30), ins("NOPLOT"), ins("FROZEN")}
	bb := plottedBBox(ents, map[string]*dxf.Block{"B": blk}, nil, layers)
	// Plotted: x=0, and the block content on PLOT via the INSERT on NOPLOT
	// (x=50); the frozen INSERT hides its block.
	if bb.MinX != 0 || bb.MaxX != 50 {
		t.Errorf("bbox %+v, want x 0..50", bb)
	}
}

func TestRasterMinimumStroke(t *testing.T) {
	r := NewRenderer(PaperSize{Width: 10, Height: 10}, false, 0, "")
	r.SetMinStrokeWidth(0.1)
	r.SetStyle(RGB{}, 0.05)
	if r.strokeW != 0.1 {
		t.Errorf("hairline stroke = %v, want widened to 0.1", r.strokeW)
	}
	r.SetStyle(RGB{}, 0.5)
	if r.strokeW != 0.5 {
		t.Errorf("thick stroke = %v, want 0.5", r.strokeW)
	}
}

// External references: a mangled folder name is found by its ASCII letters,
// and the referenced drawing's tables and blocks come in prefixed.
func TestBindXref(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "Xrefed Views")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	ext := dxf.NewDrawing()
	ext.Header.Version = dxf.R2000 // R12 files don't carry $INSUNITS
	ext.Header.DefaultDrawingUnits = dxf.UnitsMillimeters
	wall := dxf.NewLayer()
	wall.Name = "WALL"
	ext.Layers = append(ext.Layers, *wall)
	door := dxf.NewBlock()
	door.Name = "DOOR"
	dl := dxf.NewLine()
	dl.P2 = dxf.Point{X: 1000}
	door.Entities = append(door.Entities, dl)
	ext.Blocks = append(ext.Blocks, *door)
	wl := dxf.NewLine()
	wl.SetLayer("WALL")
	wl.P2 = dxf.Point{X: 2000, Y: 1000}
	di := dxf.NewInsert()
	di.Name = "DOOR"
	di.Location = dxf.Point{Y: 3000}
	ext.Entities = append(ext.Entities, wl, di)
	// The archive mangled "Ö" in the file name.
	if err := ext.SaveFile(filepath.Join(sub, "FЩLD.dxf")); err != nil {
		t.Fatal(err)
	}

	host := dxf.NewDrawing()
	host.Header.Version = dxf.R2000
	host.Header.DefaultDrawingUnits = dxf.UnitsMeters
	xb := dxf.NewBlock()
	xb.Name = "PLAN"
	xb.Flags = 4
	xb.XrefName = `Xrefed Views\FÖLD.dxf`
	host.Blocks = append(host.Blocks, *xb)
	// The host's record of the xref's layer carries its overrides: frozen.
	hostWall := dxf.NewLayer()
	hostWall.Name, hostWall.Flags = "PLAN|WALL", 1|16|32
	host.Layers = append(host.Layers, *hostWall)
	xi := dxf.NewInsert()
	xi.Name = "PLAN"
	xi.Location = dxf.Point{X: 10}
	// CAD programs put the mm → m factor into the INSERT's scale.
	xi.XScaleFactor, xi.YScaleFactor, xi.ZScaleFactor = 0.001, 0.001, 0.001
	host.Entities = append(host.Entities, xi)
	hostPath := filepath.Join(dir, "host.dxf")
	if err := host.SaveFile(hostPath); err != nil {
		t.Fatal(err)
	}

	info, err := Inspect(hostPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Xrefs) != 1 || info.Xrefs[0].Error != "" || filepath.Base(info.Xrefs[0].Path) != "FЩLD.dxf" {
		t.Fatalf("xrefs = %+v", info.Xrefs)
	}
	walls := 0
	for _, l := range info.Layers {
		if l.Name == "PLAN|WALL" {
			walls++
			if l.Visible {
				t.Error("PLAN|WALL: the host's frozen record must win")
			}
		}
	}
	if walls != 1 {
		t.Errorf("layer PLAN|WALL listed %d times, want once", walls)
	}
	// 2 × 3 m (mm content at INSERT scale 0.001), inserted at x = 10.
	bb := info.BoundingBox
	if math.Abs(bb.MinX-10) > 1e-9 || math.Abs(bb.MaxX-12) > 1e-9 || math.Abs(bb.MinY) > 1e-9 || math.Abs(bb.MaxY-3) > 1e-9 {
		t.Errorf("bbox %+v, want (10,0)-(12,3)", bb)
	}
}

// Layers named in --layers show even when they don't plot.
func TestSelectedLayerOverridesNoPlot(t *testing.T) {
	layers := map[string]dxf.Layer{"NOPLOT": {Name: "NOPLOT"}}
	l := dxf.NewLine()
	l.SetLayer("NOPLOT")
	l.P2 = dxf.Point{X: 5, Y: 5}
	if bb := plottedBBox([]dxf.Entity{l}, nil, nil, layers); bb.MinX <= bb.MaxX {
		t.Errorf("non-plotting layer in the frame: %+v", bb)
	}
	if bb := plottedBBox([]dxf.Entity{l}, nil, newLayerFilter([]string{"NOPLOT"}), layers); bb.MaxX != 5 {
		t.Errorf("selected non-plotting layer: bbox %+v, want it shown", bb)
	}
}
