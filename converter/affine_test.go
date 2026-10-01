package converter

import (
	"math"
	"testing"

	dxf "github.com/kedazo/dxf-go"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func assertPoint(t *testing.T, what string, gx, gy, wx, wy float64) {
	t.Helper()
	if !near(gx, wx) || !near(gy, wy) {
		t.Errorf("%s = (%v, %v), want (%v, %v)", what, gx, gy, wx, wy)
	}
}

// A mirrored parent containing a rotated child: the old "multiply scales,
// add rotations" composition got this wrong.
func TestNestedMirroredRotatedInsert(t *testing.T) {
	parent := dxf.NewInsert()
	parent.XScaleFactor = -1 // mirror in X
	child := dxf.NewInsert()
	child.Rotation = 90

	m := identityAffine.
		mul(insertAffine(parent, dxf.Point{}, 0, 0)).
		mul(insertAffine(child, dxf.Point{}, 0, 0))

	// child rotates (1,0) → (0,1); parent mirror keeps it at (0,1).
	x, y := m.apply(1, 0)
	assertPoint(t, "nested (1,0)", x, y, 0, 1)
	// child rotates (0,1) → (-1,0); parent mirror flips it to (1,0).
	x, y = m.apply(0, 1)
	assertPoint(t, "nested (0,1)", x, y, 1, 0)
}

func TestInsertAffineBasePointScaleRotate(t *testing.T) {
	ins := dxf.NewInsert()
	ins.Location = dxf.Point{X: 100, Y: 50}
	ins.XScaleFactor, ins.YScaleFactor = 2, 3
	ins.Rotation = 90
	m := insertAffine(ins, dxf.Point{X: 10, Y: 10}, 0, 0)
	// (11,10) - base = (1,0) → scale (2,0) → rotate (0,2) → +loc
	x, y := m.apply(11, 10)
	assertPoint(t, "insert", x, y, 100, 52)
}

func TestOCSNegativeZMirrorsX(t *testing.T) {
	m := ocsAffine(dxf.Vector{Z: -1}, 5)
	x, y := m.apply(3, 4)
	assertPoint(t, "ocs(0,0,-1)", x, y, -3, 4)
	if m := ocsAffine(dxf.Vector{Z: 1}, 5); m != identityAffine {
		t.Errorf("ocs(0,0,1) = %+v, want identity", m)
	}
	if m := ocsAffine(dxf.Vector{}, 0); m != identityAffine {
		t.Errorf("ocs(0,0,0) = %+v, want identity", m)
	}
}

// A vertical OCS plane (normal along +X): OCS x → world Y, OCS y → world Z
// (dropped in plan), elevation z → world X.
func TestOCSVerticalPlaneUsesElevation(t *testing.T) {
	m := ocsAffine(dxf.Vector{X: 1}, 7)
	x, y := m.apply(2, 3)
	assertPoint(t, "ocs(1,0,0)", x, y, 7, 2)
}

func TestBulgeArc(t *testing.T) {
	// Quarter circle CCW from (1,0) to (0,1) around the origin.
	cx, cy, r, start, sweep := bulgeArc(1, 0, 0, 1, math.Tan(math.Pi/8))
	assertPoint(t, "center", cx, cy, 0, 0)
	if !near(r, 1) || !near(start, 0) || !near(sweep, math.Pi/2) {
		t.Errorf("r=%v start=%v sweep=%v, want 1, 0, π/2", r, start, sweep)
	}
	// Negative bulge: clockwise, center on the other side.
	cx, cy, _, _, sweep = bulgeArc(1, 0, 0, 1, -math.Tan(math.Pi/8))
	assertPoint(t, "cw center", cx, cy, 1, 1)
	if !near(sweep, -math.Pi/2) {
		t.Errorf("cw sweep = %v, want -π/2", sweep)
	}
}

func TestCCWRange(t *testing.T) {
	for _, tc := range []struct{ t0, t1, w0, w1 float64 }{
		{0, math.Pi / 2, 0, math.Pi / 2},
		{3 * math.Pi / 2, math.Pi / 2, 3 * math.Pi / 2, 5 * math.Pi / 2}, // wraps through 0
		{1, 1, 1, 1 + 2*math.Pi},                                         // equal: full turn
		{-math.Pi / 2, 0, -math.Pi / 2, 0},
	} {
		if g0, g1 := ccwRange(tc.t0, tc.t1); !near(g0, tc.w0) || !near(g1, tc.w1) {
			t.Errorf("ccwRange(%v, %v) = %v, %v, want %v, %v", tc.t0, tc.t1, g0, g1, tc.w0, tc.w1)
		}
	}
	// Malformed angles must neither hang nor produce huge sweeps.
	for _, tc := range [][2]float64{{1e300, -1e300}, {math.Inf(1), 0}, {0, math.NaN()}} {
		if g0, g1 := ccwRange(tc[0], tc[1]); math.IsNaN(g1-g0) || g1-g0 < 0 || g1-g0 > 2*math.Pi+1e-9 {
			t.Errorf("ccwRange(%v, %v) = %v, %v", tc[0], tc[1], g0, g1)
		}
	}
}

func TestLineWeightHairline(t *testing.T) {
	if got := LineWeightToMM(0); got != hairlineMM {
		t.Errorf("line weight 0 = %v mm, want hairline %v", got, hairlineMM)
	}
	if got := ResolveLineWeight(dxf.LineWeightByLayer, 0, true, 1); got != hairlineMM {
		t.Errorf("ByLayer on a 0 layer = %v mm, want hairline", got)
	}
	if got := ResolveLineWeight(dxf.LineWeightByLayer, 0, false, 1); got != defaultLineWidthMM {
		t.Errorf("ByLayer on a missing layer = %v mm, want default", got)
	}
	if got := LineWeightToMM(dxf.LineWeightStandard); got != defaultLineWidthMM {
		t.Errorf("Standard = %v mm, want default", got)
	}
}

func TestTextFrameMirrored(t *testing.T) {
	_, _, rot, h, mirrored := textFrame(scaleAffine(-1, 1), 0, 0, 0)
	if !mirrored || !near(rot, 0) || !near(h, 1) {
		t.Errorf("mirror X: rot=%v h=%v mirrored=%v, want 0, 1, true", rot, h, mirrored)
	}
	_, _, rot, h, mirrored = textFrame(rotateAffine(30).mul(scaleAffine(2, 2)), 0, 0, 15)
	if mirrored || !near(rot, 45) || !near(h, 2) {
		t.Errorf("rotated: rot=%v h=%v mirrored=%v, want 45, 2, false", rot, h, mirrored)
	}
}

func TestEllipseInRotatedInsertBBox(t *testing.T) {
	blk := dxf.NewBlock()
	blk.Name = "E"
	el := dxf.NewEllipse()
	el.MajorAxis = dxf.Vector{X: 10}
	el.MinorAxisRatio = 0.5
	el.StartAngle, el.EndAngle = 0, 2*math.Pi
	blk.Entities = append(blk.Entities, el)

	ins := dxf.NewInsert()
	ins.Name = "E"
	ins.Rotation = 90
	bb := ComputeBoundingBox([]dxf.Entity{ins}, map[string]*dxf.Block{"E": blk})
	// Rotated 90°: 5 wide, 10 tall.
	if !near(bb.MaxX, 5) || !near(bb.MaxY, 10) || !near(bb.MinX, -5) || !near(bb.MinY, -10) {
		t.Errorf("bbox = %+v, want (-5,-10)-(5,10)", bb)
	}
}

// A bulged edge's arc bulges past its vertices; a drawing of only such
// edges must not be "empty".
func TestBulgeArcInBBox(t *testing.T) {
	pl := dxf.NewLWPolyline()
	pl.Vertices = []dxf.LwVertex{{X: -1, Y: 0, Bulge: 1}, {X: 1, Y: 0}} // CCW half circle, apex (0,-1)
	bb := ComputeBoundingBox([]dxf.Entity{pl}, nil)
	if !near(bb.MinY, -1) || !near(bb.MaxY, 0) || !near(bb.MinX, -1) || !near(bb.MaxX, 1) {
		t.Errorf("bbox = %+v, want (-1,-1)-(1,0)", bb)
	}
}

// Text extends from its anchor, so labels crossing a tile edge are kept.
func TestTextExtentsInBBox(t *testing.T) {
	txt := dxf.NewText()
	txt.Value = "ABCDE" // ~5 × 0.8 × 2 = 8 wide
	txt.Height = 2
	bb := ComputeBoundingBox([]dxf.Entity{txt}, nil)
	if bb.MaxX < 6 || bb.MaxY < 2 || bb.MinY > -0.5 || bb.MinX != 0 {
		t.Errorf("TEXT bbox = %+v, want about (0,-0.6)-(8,2)", bb)
	}

	mt := dxf.NewMText()
	mt.Text = `ONE\PTWO`
	mt.InitialTextHeight = 1
	mt.AttachmentPoint = dxf.AttachmentPointMiddleCenter
	bb = ComputeBoundingBox([]dxf.Entity{mt}, nil)
	if !(bb.MinX < -1 && bb.MaxX > 1 && bb.MinY < -1 && bb.MaxY > 1) {
		t.Errorf("centered two-line MTEXT bbox = %+v, want around the anchor", bb)
	}

	// Rotated 90°: the text runs up from the anchor.
	txt.Rotation = 90
	bb = ComputeBoundingBox([]dxf.Entity{txt}, nil)
	if bb.MaxY < 6 || bb.MaxX > 0.7 {
		t.Errorf("rotated TEXT bbox = %+v, want it to run up the Y axis", bb)
	}
}

func TestMInsertArrayBBox(t *testing.T) {
	blk := dxf.NewBlock()
	blk.Name = "P"
	pt := dxf.NewModelPoint()
	blk.Entities = append(blk.Entities, pt)

	ins := dxf.NewInsert()
	ins.Name = "P"
	ins.ColumnCount, ins.RowCount = 3, 2
	ins.ColumnSpacing, ins.RowSpacing = 10, 5
	bb := ComputeBoundingBox([]dxf.Entity{ins}, map[string]*dxf.Block{"P": blk})
	if !near(bb.MaxX, 20) || !near(bb.MaxY, 5) || !near(bb.MinX, 0) || !near(bb.MinY, 0) {
		t.Errorf("bbox = %+v, want (0,0)-(20,5)", bb)
	}
}

func TestResolveStyleInheritance(t *testing.T) {
	layers := map[string]dxf.Layer{
		"0":     {Name: "0", Color: dxf.Color(7)},
		"WALLS": {Name: "WALLS", Color: dxf.Color(1), LineWeight: dxf.LineWeight(50)},
	}

	ins := dxf.NewInsert()
	ins.SetLayer("WALLS")
	ins.SetLineWeight(dxf.LineWeightByLayer)
	ctx := topCtx.child(ins, identityAffine, layers)

	// Layer "0" entity with ByLayer color inherits the INSERT's layer (red).
	l := dxf.NewLine()
	l.SetLayer("0")
	if rgb, _ := resolveStyle(l, layers, ctx); rgb != (RGB{255, 0, 0}) {
		t.Errorf("layer-0 ByLayer color = %v, want red", rgb)
	}

	// ByBlock color/lineweight take the INSERT's resolved style.
	bb := dxf.NewLine()
	bb.SetLayer("0")
	bb.SetColor(dxf.ByBlock())
	bb.SetLineWeight(dxf.LineWeightByBlock)
	if rgb, lw := resolveStyle(bb, layers, ctx); rgb != (RGB{255, 0, 0}) || !near(lw, 0.5) {
		t.Errorf("ByBlock style = %v %v, want red 0.5", rgb, lw)
	}

	// True color wins over ACI.
	tc := dxf.NewLine()
	tc.SetColor(dxf.Color(1))
	tc.SetColor24Bit(0x123456)
	if rgb, _ := resolveStyle(tc, layers, topCtx); rgb != (RGB{0x12, 0x34, 0x56}) {
		t.Errorf("true color = %v, want #123456", rgb)
	}

	// True color black is a color, not "unset".
	black := dxf.NewLine()
	black.SetColor(dxf.Color(1))
	black.SetColor24Bit(0)
	if rgb, _ := resolveStyle(black, layers, topCtx); rgb != (RGB{}) {
		t.Errorf("true color black = %v, want black", rgb)
	}

	// ByLayer takes the layer's true color over its ACI.
	tl := dxf.NewLine()
	tl.SetLayer("TC")
	tlLayers := map[string]dxf.Layer{"TC": {Name: "TC", Color: dxf.Color(1), Color24Bit: 0x00FF80, HasColor24Bit: true}}
	if rgb, _ := resolveStyle(tl, tlLayers, topCtx); rgb != (RGB{0, 0xFF, 0x80}) {
		t.Errorf("layer true color = %v, want #00FF80", rgb)
	}
}
