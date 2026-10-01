package converter

import (
	"math"
	"testing"

	dxf "github.com/kedazo/dxf-go"
)

func TestWideSegmentTrapezoid(t *testing.T) {
	got := edgePiece(polylineEdge{X2: 10, W0: 2, W1: 4}, 0.01)
	want := [][2]float64{{0, 1}, {10, 2}, {10, -2}, {0, -1}}
	for i := range want {
		assertPoint(t, "corner", got[i][0], got[i][1], want[i][0], want[i][1])
	}
	if edgePiece(polylineEdge{X2: 10}, 0.01) != nil {
		t.Error("a zero-width edge has no outline")
	}
}

// A closed square with constant width: four bands and four mitered corners,
// whose miter tips sit diagonally out from the vertices.
// The bbox covers a wide polyline's outline: past the last vertex and
// beyond the arc of a bulge.
func TestWidePolylineBBox(t *testing.T) {
	diag := dxf.NewLWPolyline()
	diag.ConstantWidth = 2
	diag.Vertices = []dxf.LwVertex{{X: 0, Y: 0}, {X: 10, Y: 10}}
	if bb := ComputeBoundingBox([]dxf.Entity{diag}, nil); math.Abs(bb.MaxY-(10+math.Sqrt2/2)) > 1e-6 {
		t.Errorf("diagonal bbox MaxY = %v, want %v", bb.MaxY, 10+math.Sqrt2/2)
	}
	semi := dxf.NewLWPolyline() // a semicircle of radius 5 below the x axis
	semi.ConstantWidth = 2
	semi.Vertices = []dxf.LwVertex{{X: -5, Y: 0, Bulge: 1}, {X: 5, Y: 0}}
	if bb := ComputeBoundingBox([]dxf.Entity{semi}, nil); bb.MinY > -5.99 {
		t.Errorf("semicircle bbox MinY = %v, want about -6", bb.MinY)
	}
}

func TestWidePolylineJoins(t *testing.T) {
	pl := dxf.NewLWPolyline()
	pl.ConstantWidth = 1
	pl.Vertices = []dxf.LwVertex{{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}, {X: 0, Y: 10}}
	pl.SetIsClosed(true)
	pieces := widePolylinePieces(lwPolylineEdges(pl), true, 0.01)
	if len(pieces) != 8 {
		t.Fatalf("pieces = %d, want 4 bands + 4 joins", len(pieces))
	}
	join := pieces[4] // at (10,0): a left turn, so the outer side is right/below
	if len(join) != 4 {
		t.Fatalf("join = %v, want a miter quad", join)
	}
	assertPoint(t, "miter tip", join[2][0], join[2][1], 10.5, -0.5)

	// Its bbox grows by the half width.
	if bb := ComputeBoundingBox([]dxf.Entity{pl}, nil); !near(bb.MinX, -0.5) || !near(bb.MaxY, 10.5) {
		t.Errorf("bbox = %+v, want (-0.5,-0.5)-(10.5,10.5)", bb)
	}
}

// A bulged wide segment is a band around the arc.
func TestWideArcBand(t *testing.T) {
	// Half circle (bulge 1) from (-1,0) to (1,0): CCW, below the chord.
	band := edgePiece(polylineEdge{X1: -1, X2: 1, Bulge: 1, W0: 0.5, W1: 0.5}, 0.001)
	if len(band) < 10 {
		t.Fatalf("band has %d points", len(band))
	}
	for _, p := range band {
		if r := math.Hypot(p[0], p[1]); r < 0.75-1e-9 || r > 1.25+1e-9 {
			t.Errorf("point %v at radius %v, want 0.75..1.25", p, r)
		}
	}
}
