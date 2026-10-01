package converter

import (
	"image"
	"strings"
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

// Spline leaders are smooth curves through their vertices.
func TestCurveThrough(t *testing.T) {
	pts := [][2]float64{{0, 0}, {10, 10}, {20, 0}}
	curves := curveThrough(pts)
	if len(curves) != 2 || curves[0][0] != pts[0] || curves[0][3] != pts[1] || curves[1][3] != pts[2] {
		t.Fatalf("curves = %v, want two through the points", curves)
	}
	// Smooth at the middle vertex: the control points on either side of it
	// are collinear with it (here level, at the apex).
	if curves[0][2][1] != 10 || curves[1][1][1] != 10 {
		t.Errorf("tangent at the apex: %v and %v, want level", curves[0][2], curves[1][1])
	}

	leader := dxf.NewLeader()
	leader.PathType = dxf.LeaderPathTypeSpline
	leader.Vertices = []dxf.Point{{X: 0, Y: 0}, {X: 10, Y: 10}, {X: 20, Y: 0}}
	paper := PaperSize{Width: 100, Height: 100}
	r := NewRenderer(paper, false, 0, "")
	r.SetTransform(NewTransform(BBox{MaxX: 100, MaxY: 100}, 1, paper, 0, AlignTopLeft, false))
	renderEntity(r, leader, nil, nil, topCtx)
	if r.pending == nil || !strings.Contains(r.pending.String(), "C") {
		t.Errorf("spline leader drawn as %v, want curves", r.pending)
	}
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
	paths, text, block := mleaderParts(ml, map[dxf.Handle]string{0x42: "TAG"}, nil)
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

// A MULTILEADER takes what it leaves unset from its MLEADERSTYLE.
func TestMLeaderStyle(t *testing.T) {
	d := dxf.NewDrawing()
	rec := dxf.NewBlockRecord()
	rec.Name = "DOT"
	rec.SetHandle(0x50)
	d.BlockRecords = append(d.BlockRecords, *rec)
	st := dxf.NewStyle()
	st.Name = "NOTES"
	st.SetHandle(0x60)
	d.Styles = append(d.Styles, *st)
	// kind in the high byte: 0xC0 ByLayer, 0xC2 true colour, 0xC3 ACI
	objColor := func(kind, value uint32) dxf.ObjectColor { return dxf.ObjectColor(int32(kind<<24 | value)) }
	d.MLeaderStyles = []dxf.MLeaderStyle{{
		Handle: 0x70, ArrowheadHandle: 0x50, ArrowheadSize: 0.2, TextStyleHandle: 0x60, TextHeight: 0.25,
		TextAlignment: 1, TextColor: objColor(0xC3, 1), LeaderLineColor: objColor(0xC2, 0x00FF00),
		LeaderLineWeight: dxf.LineWeight(50), LeaderLineType: mleaderLineSpline,
	}, {Handle: 0x71}}
	look, ok := newLeaderArrows(d).mleaderLook(0x70)
	if !ok || look.arrowBlock != "DOT" || look.textStyle != "NOTES" || look.arrowSize != 0.2 || look.lineWeight != 50 ||
		look.lineType != mleaderLineSpline {
		t.Fatalf("look = %+v (ok %v)", look, ok)
	}
	// A style's leader type 0 hides the lines (a missing 173 reads straight).
	if hidden, _ := newLeaderArrows(d).mleaderLook(0x71); hidden.lineType != mleaderLineInvisible {
		t.Errorf("style with leader type 0: line type %d, want invisible", hidden.lineType)
	}
	if c := objectColorRGB(look.textColor, RGB{}); c != (RGB{255, 0, 0}) {
		t.Errorf("text colour = %v, want ACI 1 red", c)
	}
	if c := objectColorRGB(look.lineColor, RGB{}); c != (RGB{0, 255, 0}) {
		t.Errorf("leader colour = %v, want true colour green", c)
	}
	if c := objectColorRGB(objColor(0xC0, 0), RGB{1, 2, 3}); c != (RGB{1, 2, 3}) { // ByLayer
		t.Errorf("ByLayer colour = %v, want the entity's", c)
	}

	ml := &dxf.MLeader{Scale: 2, HasText: true, Text: "Note", TextLocation: dxf.Point{X: 1}}
	ml.SetIsVisible(true)
	_, text, _ := mleaderParts(ml, nil, &look)
	if text.TextStyleName != "NOTES" || !near(text.InitialTextHeight, 0.5) || text.AttachmentPoint != dxf.AttachmentPointTopCenter {
		t.Errorf("text = style %q height %v attach %v", text.TextStyleName, text.InitialTextHeight, text.AttachmentPoint)
	}

	// The leader's own values win where it overrides the style, and only there.
	ml.StyleHandle = 0x70
	ml.PropertyOverrides = dxf.MLeaderOverrideLeaderLineColor | dxf.MLeaderOverrideTextAlignment
	ml.LeaderLineColor, ml.TextAlignment = objColor(0xC3, 5), 2
	ml.TextColor = objColor(0xC3, 3) // not overridden
	got, ok := newLeaderArrows(d).mleaderLookFor(ml)
	if !ok || got.lineColor != ml.LeaderLineColor || got.align != 2 || got.textColor != look.textColor || got.arrowBlock != "DOT" {
		t.Errorf("overridden look = %+v (ok %v)", got, ok)
	}
}

// Pixels outside a clip polygon become transparent.
func TestMaskImage(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range src.Pix {
		src.Pix[i] = 255
	}
	left := [][2]float64{{0, 0}, {2, 0}, {2, 4}, {0, 4}} // columns 0 and 1
	out := maskImage(src, [][][2]float64{left}).(*image.NRGBA)
	for k := 0; k < 4; k++ {
		for i := 0; i < 4; i++ {
			a := out.NRGBAAt(i, k).A
			if want := uint8(255); i >= 2 {
				want = 0
				if a != want {
					t.Errorf("pixel (%d,%d) alpha %d, want %d", i, k, a, want)
				}
			} else if a != want {
				t.Errorf("pixel (%d,%d) alpha %d, want %d", i, k, a, want)
			}
		}
	}

	// A two-point clip boundary is a rectangle of pixel centres.
	rect := imageClipPixels([]dxf.Point{{X: -0.5, Y: -0.5}, {X: 1.5, Y: 3.5}}, 1, 1)
	if len(rect) != 4 || rect[2] != [2]float64{2, 4} {
		t.Errorf("clip rectangle = %v", rect)
	}
}

// Wipeout outlines plot only with WIPEOUTFRAME 1.
func TestWipeoutFrame(t *testing.T) {
	w := dxf.NewWipeout()
	w.SetUVector(dxf.Vector{X: 10})
	w.SetVVector(dxf.Vector{Y: 10})
	w.SetImageSize(dxf.Vector{X: 1, Y: 1})
	paper := PaperSize{Width: 100, Height: 100}
	for frame, want := range map[int16]bool{0: false, 1: true, 2: false} {
		r := NewRenderer(paper, false, 0, "")
		r.SetTransform(NewTransform(BBox{MaxX: 100, MaxY: 100}, 1, paper, 0, AlignTopLeft, false))
		r.SetFrames(&dxf.Drawing{WipeoutVariables: &dxf.WipeoutVariables{Frame: frame}})
		renderEntity(r, w, nil, nil, topCtx)
		if got := r.pending != nil && !r.pending.Empty(); got != want {
			t.Errorf("WIPEOUTFRAME %d: outline drawn = %v, want %v", frame, got, want)
		}
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
