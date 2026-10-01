package converter

import (
	"strings"
	"testing"

	dxf "github.com/kedazo/dxf-go"
)

// A two-element MLINE (offsets +0.5 and -0.5) along the X axis: two
// parallel lines.
func TestMLineElements(t *testing.T) {
	vertex := func(x string) string {
		return "11\n" + x + "\n21\n0.0\n31\n0.0\n12\n1.0\n22\n0.0\n32\n0.0\n13\n0.0\n23\n1.0\n33\n0.0\n" +
			"74\n2\n41\n0.5\n41\n0.0\n75\n0\n" + // element 1: offset 0.5, no breaks
			"74\n2\n41\n-0.5\n41\n0.0\n75\n0\n" // element 2: offset -0.5
	}
	s := "0\nSECTION\n2\nENTITIES\n0\nMLINE\n5\nA1\n100\nAcDbEntity\n8\n0\n100\nAcDbMline\n" +
		"2\nSTANDARD\n40\n1.0\n70\n0\n71\n1\n72\n2\n73\n2\n10\n0.0\n20\n0.0\n30\n0.0\n210\n0.0\n220\n0.0\n230\n1.0\n" +
		vertex("0.0") + vertex("10.0") + "0\nENDSEC\n0\nEOF\n"
	d, err := dxf.ReadFromReader(strings.NewReader(s))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ml, ok := d.Entities[0].(*dxf.MLine)
	if !ok {
		t.Fatalf("parsed %T, want *dxf.MLine", d.Entities[0])
	}
	lines, ok := mlineElements(ml)
	if !ok || len(lines) != 2 {
		t.Fatalf("elements = %v (ok %v), want 2", lines, ok)
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

	// Parameters that don't divide evenly are not guessed at.
	ml.Parameters = ml.Parameters[:len(ml.Parameters)-1]
	if _, ok := mlineElements(ml); ok {
		t.Error("an uneven parameter list must be refused")
	}
}
