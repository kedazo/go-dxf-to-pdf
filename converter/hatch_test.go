package converter

import (
	"math"
	"testing"

	dxf "github.com/ixmilia/dxf-go"
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
