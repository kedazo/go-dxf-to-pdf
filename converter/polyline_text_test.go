package converter

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dxf "github.com/kedazo/dxf-go"
)

func polyVertex(x, y float64, flags int) dxf.Vertex {
	v := *dxf.NewVertex()
	v.Location = dxf.Point{X: x, Y: y}
	v.Flags = flags
	return v
}

func faceRecord(i1, i2, i3, i4 int) dxf.Vertex {
	v := *dxf.NewVertex()
	v.Flags = 128
	v.PolyfaceMeshVertexIndex1 = i1
	v.PolyfaceMeshVertexIndex2 = i2
	v.PolyfaceMeshVertexIndex3 = i3
	v.PolyfaceMeshVertexIndex4 = i4
	return v
}

// squarePolyface is a 10..20 square made of two triangles sharing an
// invisible diagonal (1→3).
func squarePolyface() *dxf.Polyline {
	p := dxf.NewPolyline()
	p.Flags = 64
	p.Vertices = []dxf.Vertex{
		polyVertex(10, 10, 192), polyVertex(20, 10, 192),
		polyVertex(20, 20, 192), polyVertex(10, 20, 192),
		faceRecord(1, 2, -3, 0), // edges 1-2, 2-3 visible; 3-1 hidden
		faceRecord(3, 4, -1, 0), // edges 3-4, 4-1 visible; 1-3 hidden
	}
	return p
}

func TestPolyfaceEdges(t *testing.T) {
	edges := polylineEdges(squarePolyface())
	if len(edges) != 4 {
		t.Fatalf("got %d edges, want 4 (square outline, no hidden diagonal): %+v", len(edges), edges)
	}
	for _, e := range edges {
		for _, c := range []float64{e.X1, e.Y1, e.X2, e.Y2} {
			if c < 10 || c > 20 {
				t.Errorf("edge %+v leaves the mesh (face records must not be drawn)", e)
			}
		}
	}
}

func TestPolyfaceBBoxIgnoresFaceRecords(t *testing.T) {
	bb := ComputeBoundingBox([]dxf.Entity{squarePolyface()}, nil)
	if bb.MinX != 10 || bb.MinY != 10 || bb.MaxX != 20 || bb.MaxY != 20 {
		t.Errorf("bbox = %+v, want (10,10)-(20,20)", bb)
	}
}

func TestPolylineClosedAndSplineFit(t *testing.T) {
	p := dxf.NewPolyline()
	p.Vertices = []dxf.Vertex{polyVertex(0, 0, 0), polyVertex(1, 0, 0), polyVertex(1, 1, 0)}
	if got := len(polylineEdges(p)); got != 2 {
		t.Errorf("open polyline: %d edges, want 2", got)
	}
	p.Flags = 1
	edges := polylineEdges(p)
	if len(edges) != 3 {
		t.Fatalf("closed polyline: %d edges, want 3", len(edges))
	}
	if last := edges[2]; last.X2 != 0 || last.Y2 != 0 {
		t.Errorf("closing edge ends at (%v,%v), want (0,0)", last.X2, last.Y2)
	}

	// Spline frame control points (flag 16) are not part of the curve.
	p.Flags = 0
	p.Vertices = append(p.Vertices, polyVertex(50, 50, 16))
	if got := len(polylineEdges(p)); got != 2 {
		t.Errorf("spline-fit polyline: %d edges, want 2", got)
	}
}

func TestPolygonMeshEdges(t *testing.T) {
	p := dxf.NewPolyline()
	p.Flags = 16
	p.PolygonMeshMVertexCount = 2
	p.PolygonMeshNVertexCount = 3
	for i := 0; i < 2; i++ {
		for j := 0; j < 3; j++ {
			p.Vertices = append(p.Vertices, polyVertex(float64(j), float64(i), 64))
		}
	}
	// 2 rows × 2 segments + 3 columns × 1 segment
	if got := len(polylineEdges(p)); got != 7 {
		t.Errorf("2x3 mesh: %d edges, want 7", got)
	}
}

func TestPlainText(t *testing.T) {
	tests := map[string]string{
		"plain":               "plain",
		"%%c50":               "Ø50",
		"90%%d":               "90°",
		"%%p0,00":             "±0,00",
		"100%%%":              "100%",
		"%%uunder%%u":         "under",
		`Sz\U+00E1ll\U+00E1s`: "Szállás",
		"%%176":               "°",
	}
	for in, want := range tests {
		if got := plainText(in); got != want {
			t.Errorf("plainText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseMTextUnicodeAndColor(t *testing.T) {
	segs := ParseMText(`\U+0151{\C1;red}\C256;back`)
	if len(segs) != 3 {
		t.Fatalf("got %d segments: %+v", len(segs), segs)
	}
	if segs[0].Text != "ő" || segs[0].Style.HasColor {
		t.Errorf("seg0 = %+v, want 'ő' with entity color", segs[0])
	}
	if segs[1].Text != "red" || !segs[1].Style.HasColor || segs[1].Style.ColorR != 255 {
		t.Errorf("seg1 = %+v, want explicit red", segs[1])
	}
	if segs[2].Text != "back" || segs[2].Style.HasColor {
		t.Errorf("seg2 = %+v, want ByLayer (entity color)", segs[2])
	}
}

func TestResolveTextAnchor(t *testing.T) {
	loc := dxf.Point{X: 1, Y: 2}
	second := dxf.Point{X: 5, Y: 6}

	a := resolveTextAnchor(loc, second, dxf.HorizontalTextJustificationLeft, dxf.VerticalTextJustificationBaseline, 30)
	if a.X != 1 || a.Y != 2 || a.HAlign != 0 || a.VAlign != vAlignBaseline || a.RotationDeg != 30 {
		t.Errorf("left/baseline: %+v", a)
	}

	a = resolveTextAnchor(loc, second, dxf.HorizontalTextJustificationCenter, dxf.VerticalTextJustificationTop, 0)
	if a.X != 5 || a.Y != 6 || a.HAlign != 0.5 || a.VAlign != vAlignTop {
		t.Errorf("center/top: %+v", a)
	}

	a = resolveTextAnchor(loc, second, dxf.HorizontalTextJustificationMiddle, dxf.VerticalTextJustificationBaseline, 0)
	if a.HAlign != 0.5 || a.VAlign != vAlignMiddle {
		t.Errorf("middle: %+v", a)
	}

	a = resolveTextAnchor(dxf.Point{}, dxf.Point{X: 0, Y: 3}, dxf.HorizontalTextJustificationAligned, dxf.VerticalTextJustificationBaseline, 0)
	if math.Abs(a.RotationDeg-90) > 1e-9 || a.X != 0 || a.Y != 0 {
		t.Errorf("aligned: %+v, want rotation 90 at first point", a)
	}
}

func TestMTextRotationFromXAxisDirection(t *testing.T) {
	m := dxf.NewMText()
	m.RotationAngle = 0
	m.XAxisDirection = dxf.Vector{X: 0, Y: 1}
	if got := mtextRotationDeg(m); math.Abs(got-90) > 1e-9 {
		t.Errorf("rotation = %v, want 90", got)
	}
	m.XAxisDirection = dxf.Vector{}
	m.RotationAngle = 90
	if got := mtextRotationDeg(m); math.Abs(got-90) > 1e-9 {
		t.Errorf("rotation from angle = %v, want 90", got)
	}
	// No group 11 in the file: the parser's default direction (1,0,0) must
	// not hide group 50.
	m = dxf.NewMText()
	m.RotationAngle = 30
	if got := mtextRotationDeg(m); math.Abs(got-30) > 1e-9 {
		t.Errorf("rotation with default direction = %v, want 30", got)
	}
}

func TestParseMTextStacksAndEscapes(t *testing.T) {
	segs := ParseMText(`288,47 m{\H0.66x;\S2^ ;}`)
	if len(segs) != 2 || segs[1].Text != "2" || !segs[1].Style.Superscript || segs[1].Style.HeightRelative != 0.66 {
		t.Errorf("superscript: %+v", segs)
	}
	for in, want := range map[string]string{
		`\U+0151r\U+005C`: `őr\`, // a decoded backslash stays text
		`a\\U+0041`:       `a\U+0041`,
		`%%c25`:           "Ø25",
		`a^Jb`:            "a|b",
		`1\P2`:            "1|2",
	} {
		var got strings.Builder
		for _, s := range ParseMText(in) {
			if s.NewLine {
				got.WriteString("|")
			}
			got.WriteString(s.Text)
		}
		if got.String() != want {
			t.Errorf("ParseMText(%q) = %q, want %q", in, got.String(), want)
		}
	}
}

// TestInsertAttributesRoundTrip checks that ATTRIBs on an INSERT survive a
// DXF write/read and are included in the bounding box and conversion.
func TestInsertAttributesRoundTrip(t *testing.T) {
	d := dxf.NewDrawing()
	blk := dxf.NewBlock()
	blk.Name = "ROOM"
	line := dxf.NewLine()
	line.P2 = dxf.Point{X: 1, Y: 0}
	blk.Entities = append(blk.Entities, line)
	d.Blocks = append(d.Blocks, *blk)

	ins := dxf.NewInsert()
	ins.Name = "ROOM"
	ins.Location = dxf.Point{X: 0, Y: 0}
	att := *dxf.NewAttribute()
	att.AttributeTag = "NAME"
	att.Value = "SZOBA 1"
	att.TextHeight = 0.2
	att.Location = dxf.Point{X: 3, Y: 4}
	ins.Attributes = append(ins.Attributes, att)
	ins.HasAttributes = true
	d.Entities = append(d.Entities, ins)

	dir := t.TempDir()
	in := filepath.Join(dir, "in.dxf")
	if err := d.SaveFile(in); err != nil {
		t.Fatalf("save: %v", err)
	}
	read, err := readDxfFile(in)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got *dxf.Insert
	for _, e := range read.Entities {
		if i, ok := e.(*dxf.Insert); ok {
			got = i
		}
	}
	if got == nil || len(got.Attributes) != 1 || got.Attributes[0].Value != "SZOBA 1" {
		t.Fatalf("attributes not read back: %+v", got)
	}

	blocks := map[string]*dxf.Block{}
	for i := range read.Blocks {
		blocks[read.Blocks[i].Name] = &read.Blocks[i]
	}
	bb := ComputeBoundingBox(read.Entities, blocks)
	if bb.MaxX < 3 || bb.MaxY < 4 {
		t.Errorf("bbox %+v does not include the attribute at (3,4)", bb)
	}

	if _, err := Convert(in, filepath.Join(dir, "out.pdf"), Options{Scale: "1:1", Paper: "A4", Margin: 10}); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "out.pdf")); err != nil || fi.Size() == 0 {
		t.Errorf("no PDF output: %v", err)
	}
}
