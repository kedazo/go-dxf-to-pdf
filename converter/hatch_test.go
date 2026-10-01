package converter

import (
	"math"
	"strings"
	"testing"

	dxf "github.com/kedazo/dxf-go"
)

func square(x0, y0, x1, y1 float64) [][2]float64 {
	return [][2]float64{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}
}

// Horizontal lines, spacing 1.
var horizontal = dxf.HatchPatternLine{Angle: 0, OffsetX: 0, OffsetY: 1}

func TestHatchHoleStaysEmpty(t *testing.T) {
	polys := [][][2]float64{square(0, 0, 10, 10), square(3, 3, 7, 7)}
	for _, s := range patternLineSegments(polys, horizontal, 0.1) {
		midX := (s[0] + s[2]) / 2
		y := s[1]
		if y > 3 && y < 7 && midX > 3 && midX < 7 {
			t.Errorf("segment %v crosses the hole", s)
		}
	}
	// Line y=5 is split into two pieces around the hole.
	n := 0
	for _, s := range patternLineSegments(polys, horizontal, 0.1) {
		if s[1] == 5 {
			n++
		}
	}
	if n != 2 {
		t.Errorf("line y=5 has %d pieces, want 2", n)
	}
}

// A hatch's lines are generated once per scale and reused.
func TestHatchLinesCached(t *testing.T) {
	h := parseHatch(t, "2\nANSI31\n70\n0\n71\n0\n91\n1\n92\n1\n93\n4\n"+
		"72\n1\n10\n0\n20\n0\n11\n10\n21\n0\n72\n1\n10\n10\n20\n0\n11\n10\n21\n10\n"+
		"72\n1\n10\n10\n20\n10\n11\n0\n21\n10\n72\n1\n10\n0\n20\n10\n11\n0\n21\n0\n"+
		"97\n0\n75\n0\n76\n1\n52\n0\n41\n1\n77\n0\n78\n1\n53\n0\n43\n0\n44\n0\n45\n0\n46\n1\n79\n0\n98\n0\n")
	r := NewRenderer(PaperSize{Width: 100, Height: 100}, false, 0, "")
	a := r.hatchLines(h, 0.1, 0.01)
	if len(a) == 0 {
		t.Fatal("no hatch lines")
	}
	if b := r.hatchLines(h, 0.1, 0.01); &b[0] != &a[0] || r.hatchCached != len(a) {
		t.Error("second draw at the same scale regenerated the lines")
	}
	if c := r.hatchLines(h, 0.05, 0.005); len(c) == 0 || &c[0] == &a[0] {
		t.Error("another scale reused the lines")
	}
}

// Lines that touch a boundary vertex or run along an edge must keep the
// even-odd pairing intact for the rest of the line.
func TestLineIntervalsDegenerateContacts(t *testing.T) {
	near := func(got [][2]float64, want [][2]float64) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range want {
			if math.Abs(got[i][0]-want[i][0]) > 1e-9 || math.Abs(got[i][1]-want[i][1]) > 1e-9 {
				return false
			}
		}
		return true
	}
	// U shape: notch x 10..20 from y=10 up; the line y=10 runs along the
	// notch floor. Both outer parts must survive.
	u := [][2]float64{{0, 0}, {30, 0}, {30, 20}, {20, 20}, {20, 10}, {10, 10}, {10, 20}, {0, 20}}
	if got := lineIntervals(0, 10, 1, 0, [][][2]float64{u}); !near(got, [][2]float64{{0, 10}, {20, 30}}) {
		t.Errorf("U floor: %v", got)
	}
	// A diamond island whose bottom apex touches the line y=4 must not split
	// the line at all.
	outer := square(0, 0, 20, 10)
	diamond := [][2]float64{{10, 4}, {12, 6}, {10, 8}, {8, 6}}
	if got := lineIntervals(0, 4, 1, 0, [][][2]float64{outer, diamond}); !near(got, [][2]float64{{0, 20}}) {
		t.Errorf("touching island: %v", got)
	}
	// A notch apex pointing down onto the line from above (V cut into the top
	// edge reaching y=4) touches but does not cross.
	v := [][2]float64{{100, 0}, {110, 0}, {110, 8}, {106, 8}, {105, 4}, {104, 8}, {100, 8}}
	if got := lineIntervals(0, 4, 1, 0, [][][2]float64{v}); !near(got, [][2]float64{{100, 110}}) {
		t.Errorf("notch apex: %v", got)
	}
}

func TestHatchSpacingNotRescaled(t *testing.T) {
	// The offset (0, 0.5) is already in drawing units: 0..10 → 21 lines.
	pl := dxf.HatchPatternLine{Angle: 0, OffsetY: 0.5}
	segs := patternLineSegments([][][2]float64{square(0, 0, 10, 10)}, pl, 0.1)
	ys := map[float64]bool{}
	for _, s := range segs {
		ys[s[1]] = true
	}
	if len(ys) < 19 || len(ys) > 21 {
		t.Errorf("got %d distinct lines, want ~20 (spacing 0.5)", len(ys))
	}
}

func parseHatch(t *testing.T, body string) *dxf.Hatch {
	t.Helper()
	s := "0\nSECTION\n2\nENTITIES\n0\nHATCH\n5\nA1\n100\nAcDbEntity\n8\n0\n100\nAcDbHatch\n" +
		"10\n0.0\n20\n0.0\n30\n0.0\n210\n0.0\n220\n0.0\n230\n1.0\n" + body + "0\nENDSEC\n0\nEOF\n"
	d, err := dxf.ReadFromReader(strings.NewReader(s))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, e := range d.Entities {
		if h, ok := e.(*dxf.Hatch); ok {
			return h
		}
	}
	t.Fatal("no hatch parsed")
	return nil
}

// Edge-type boundary: a half disc (line + CCW arc). The arc must be
// flattened along the curve, its center must not become a vertex.
func TestHatchEdgeBoundaryWithArc(t *testing.T) {
	h := parseHatch(t, "2\nSOLID\n70\n1\n71\n0\n91\n1\n92\n1\n93\n2\n"+
		"72\n1\n10\n-10.0\n20\n0.0\n11\n10.0\n21\n0.0\n"+ // line (-10,0)→(10,0)
		"72\n2\n10\n0.0\n20\n0.0\n40\n10.0\n50\n0.0\n51\n180.0\n73\n1\n"+ // arc 0°→180° r=10
		"97\n0\n75\n0\n76\n1\n98\n1\n10\n0.0\n20\n5.0\n")
	polys := hatchPolygons(h, 0.01)
	if len(polys) != 1 || len(polys[0]) < 10 {
		t.Fatalf("polygons = %v, want one flattened half disc", polys)
	}
	for _, v := range polys[0] {
		if r := math.Hypot(v[0], v[1]); v[1] > 1e-9 && math.Abs(r-10) > 0.02 {
			t.Errorf("vertex %v is off the arc (r=%v)", v, r)
		}
	}
	// The seed point (0,5) belongs to the seed list, not the boundary.
	for _, v := range polys[0] {
		if v == [2]float64{0, 5} {
			t.Errorf("seed point leaked into the boundary: %v", polys[0])
		}
	}
}

func TestDashIntervalPhaseAndDots(t *testing.T) {
	var got [][2]float64
	emit := func(a, b float64) { got = append(got, [2]float64{a, b}) }

	// dash 1, gap 1, dot, gap 1 → period 3, phased from t=0.
	dashInterval(1.5, 7, []float64{1, -1, 0, -1}, 0.01, emit)
	want := [][2]float64{{2, 2.01}, {3, 4}, {5, 5.01}, {6, 7}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if math.Abs(got[i][0]-want[i][0]) > 1e-9 || math.Abs(got[i][1]-want[i][1]) > 1e-9 {
			t.Errorf("piece %d = %v, want %v", i, got[i], want[i])
		}
	}

	// No dashes: one continuous piece.
	got = nil
	dashInterval(0, 5, nil, 0.01, emit)
	if len(got) != 1 || got[0] != [2]float64{0, 5} {
		t.Errorf("continuous = %v", got)
	}
}

func TestHatchAlongLineStagger(t *testing.T) {
	// Offset (0.5, 1): every next line is shifted half a period along X.
	pl := dxf.HatchPatternLine{Angle: 0, OffsetX: 0.5, OffsetY: 1, Dashes: []float64{0.5, -0.5}}
	segs := patternLineSegments([][][2]float64{square(0, -0.25, 4, 2.5)}, pl, 0.01)
	starts := map[float64][]float64{}
	for _, s := range segs {
		starts[s[1]] = append(starts[s[1]], s[0])
	}
	// Row y=0 starts dashes at 0,1,2,3; row y=1 at 0.5,1.5,...
	if len(starts[0]) == 0 || len(starts[1]) == 0 || starts[0][0] != 0 || starts[1][0] != 0.5 {
		t.Errorf("row starts: y0=%v y1=%v", starts[0], starts[1])
	}
}
