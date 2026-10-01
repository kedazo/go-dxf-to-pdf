package converter

import (
	"strings"
	"testing"

	dxf "github.com/kedazo/dxf-go"
)

// A two-element MLINE (offsets +0.5 and -0.5) along the X axis: two
// parallel lines.
func TestMLineElements(t *testing.T) {
	ml := parseMLine(t, mlineVertex("0.0", "74\n2\n41\n0.5\n41\n0.0\n")+mlineVertex("10.0", "74\n2\n41\n0.5\n41\n0.0\n"))
	lines := mlineElements(ml)
	if len(lines) != 2 {
		t.Fatalf("elements = %v, want 2", lines)
	}
	want := [][2]dxf.Point{{{X: 0, Y: 0.5}, {X: 10, Y: 0.5}}, {{X: 0, Y: -0.5}, {X: 10, Y: -0.5}}}
	for j := range want {
		for i := range want[j] {
			if p := lines[j][i]; !near(p.X, want[j][i].X) || !near(p.Y, want[j][i].Y) {
				t.Errorf("element %d vertex %d = %v, want %v", j, i, p, want[j][i])
			}
		}
	}
	if bb := ComputeBoundingBox([]dxf.Entity{ml}, nil); bb != (BBox{MinX: 0, MinY: -0.5, MaxX: 10, MaxY: 0.5}) {
		t.Errorf("bbox = %+v", bb)
	}

}

// mlineVertex is a MLINE vertex at (x, 0) on the X axis: its first element
// has the given 74/41 group, the second offset -0.5 and no breaks.
func mlineVertex(x, first string) string {
	return "11\n" + x + "\n21\n0.0\n31\n0.0\n12\n1.0\n22\n0.0\n32\n0.0\n13\n0.0\n23\n1.0\n33\n0.0\n" +
		first + "75\n0\n" +
		"74\n2\n41\n-0.5\n41\n0.0\n75\n0\n"
}

func parseMLine(t *testing.T, vertices string) *dxf.MLine {
	t.Helper()
	s := "0\nSECTION\n2\nENTITIES\n0\nMLINE\n5\nA1\n100\nAcDbEntity\n8\n0\n100\nAcDbMline\n" +
		"2\nSTANDARD\n40\n1.0\n70\n0\n71\n1\n72\n2\n73\n2\n10\n0.0\n20\n0.0\n30\n0.0\n210\n0.0\n220\n0.0\n230\n1.0\n" +
		vertices + "0\nENDSEC\n0\nEOF\n"
	d, err := dxf.ReadFromReader(strings.NewReader(s))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ml, ok := d.Entities[0].(*dxf.MLine)
	if !ok {
		t.Fatalf("parsed %T, want *dxf.MLine", d.Entities[0])
	}
	return ml
}

// An element cut with MLEDIT has more parameters than its neighbour: the
// positions where it starts, stops and starts again (0, 3, 5), then it runs
// to the segment's end.
func TestMLineCutElement(t *testing.T) {
	ml := parseMLine(t, mlineVertex("0.0", "74\n4\n41\n0.5\n41\n0.0\n41\n3.0\n41\n5.0\n")+
		mlineVertex("10.0", "74\n2\n41\n0.5\n41\n0.0\n"))
	lines := mlineElements(ml)
	if len(lines) != 3 {
		t.Fatalf("pieces = %v, want the cut element's two and the other element", lines)
	}
	want := [][2]float64{{0, 3}, {5, 10}, {0, 10}}
	ys := []float64{0.5, 0.5, -0.5}
	for k, w := range want {
		l := lines[k]
		if len(l) != 2 || !near(l[0].X, w[0]) || !near(l[1].X, w[1]) || !near(l[0].Y, ys[k]) {
			t.Errorf("piece %d = %v, want x %v..%v at y %v", k, l, w[0], w[1], ys[k])
		}
	}
	if p := mlinePieces([]float64{0, 1, 3}, 10); len(p) != 1 || p[0] != [2]float64{1, 3} {
		t.Errorf("start 1, stop 3 = %v, want [1 3] and nothing after", p)
	}
	// An AutoCAD tee joint: the crossing MLINE fills 3.462783..5.060990.
	if p := mlinePieces([]float64{-2.130943, 0, 3.462783, 5.060990}, 10); len(p) != 2 ||
		p[0] != [2]float64{0, 3.462783} || p[1] != [2]float64{5.060990, 10} {
		t.Errorf("tee joint = %v, want [0 3.462783] [5.06099 10]", p)
	}
}
