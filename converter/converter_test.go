package converter

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	dxf "github.com/ixmilia/dxf-go"
)

func TestParsePaperSize(t *testing.T) {
	tests := []struct {
		input string
		w, h  float64
		err   bool
	}{
		{"A4", 210, 297, false},
		{"a4", 210, 297, false},
		{"A3", 297, 420, false},
		{"A0", 841, 1189, false},
		{"400x300", 400, 300, false},
		{"100.5x200.5", 100.5, 200.5, false},
		{"bad", 0, 0, true},
		{"0x100", 0, 0, true},
	}
	for _, tt := range tests {
		p, err := ParsePaperSize(tt.input)
		if tt.err {
			if err == nil {
				t.Errorf("ParsePaperSize(%q) expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParsePaperSize(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if p.Width != tt.w || p.Height != tt.h {
			t.Errorf("ParsePaperSize(%q) = %vx%v, want %vx%v", tt.input, p.Width, p.Height, tt.w, tt.h)
		}
	}
}

func TestParseScale(t *testing.T) {
	tests := []struct {
		input string
		want  float64
		err   bool
	}{
		{"1:100", 0.01, false},
		{"1:1", 1.0, false},
		{"1:50", 0.02, false},
		{"2:1", 2.0, false},
		{"bad", 0, true},
		{"0:100", 0, true},
		{"1:0", 0, true},
	}
	for _, tt := range tests {
		got, err := ParseScale(tt.input)
		if tt.err {
			if err == nil {
				t.Errorf("ParseScale(%q) expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseScale(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("ParseScale(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestBoundingBox(t *testing.T) {
	bb := NewBBox()
	bb.Expand(10, 20)
	bb.Expand(-5, 30)
	bb.Expand(100, -10)

	if bb.MinX != -5 || bb.MinY != -10 || bb.MaxX != 100 || bb.MaxY != 30 {
		t.Errorf("BBox = %+v", bb)
	}
	if bb.Width() != 105 || bb.Height() != 40 {
		t.Errorf("BBox size = %vx%v", bb.Width(), bb.Height())
	}
}

func TestTransform(t *testing.T) {
	bbox := BBox{MinX: 0, MinY: 0, MaxX: 10000, MaxY: 5000}
	paper := PaperSize{Width: 210, Height: 297}
	tr := NewTransform(bbox, 0.01, paper, 10, AlignCenter, true)

	// At scale 1:100, drawing is 100mm x 50mm
	// Landscape A4: 297x210, printable 277x190
	// Centered offset: (277-100)/2=88.5, (190-50)/2=70

	// Origin (0,0) in DXF -> should map to some position
	x := tr.X(0)
	y := tr.Y(5000) // top of drawing -> should be at MarginTop + OffsetY

	if x < 10 || y < 10 {
		t.Errorf("Transform origin at (%.2f, %.2f), expected positive", x, y)
	}

	// Distance check
	d := tr.Dist(10000) // 10000 DXF units at 1:100 = 100mm
	if math.Abs(d-100) > 0.01 {
		t.Errorf("Dist(10000) = %.2f, want 100", d)
	}
}

func TestShouldUseLandscape(t *testing.T) {
	wide := BBox{MinX: 0, MinY: 0, MaxX: 200, MaxY: 100}
	tall := BBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 200}
	paper := PaperSize{Width: 210, Height: 297}

	if !ShouldUseLandscape(wide, paper) {
		t.Error("expected landscape for wide drawing")
	}
	if ShouldUseLandscape(tall, paper) {
		t.Error("expected portrait for tall drawing")
	}
}

func TestACIColor(t *testing.T) {
	red := ACIToRGB(1)
	if red.R != 255 || red.G != 0 || red.B != 0 {
		t.Errorf("ACI 1 = %+v, want red", red)
	}

	black := ACIToRGB(7)
	if black.R != 0 || black.G != 0 || black.B != 0 {
		t.Errorf("ACI 7 = %+v, want black", black)
	}

	oob := ACIToRGB(-1)
	if oob.R != 0 || oob.G != 0 || oob.B != 0 {
		t.Errorf("ACI -1 = %+v, want black", oob)
	}
}

func TestParseAlignment(t *testing.T) {
	if ParseAlignment("center") != AlignCenter {
		t.Error("expected AlignCenter")
	}
	if ParseAlignment("bottom-left") != AlignBottomLeft {
		t.Error("expected AlignBottomLeft")
	}
	if ParseAlignment("top-left") != AlignTopLeft {
		t.Error("expected AlignTopLeft")
	}
	if ParseAlignment("unknown") != AlignCenter {
		t.Error("expected AlignCenter for unknown")
	}
}

func TestTileGrid(t *testing.T) {
	grid := ComputeTileGrid(500, 300, 190, 277)
	if grid.Cols != 3 || grid.Rows != 2 {
		t.Errorf("TileGrid = %dx%d, want 3x2", grid.Cols, grid.Rows)
	}

	grid2 := ComputeTileGrid(100, 100, 190, 277)
	if grid2.Cols != 1 || grid2.Rows != 1 {
		t.Errorf("TileGrid = %dx%d, want 1x1", grid2.Cols, grid2.Rows)
	}
}

func TestConvertSimpleDXF(t *testing.T) {
	// Create a simple DXF with a 100x100 square
	drawing := dxf.NewDrawing()

	line1 := dxf.NewLine()
	line1.P1 = dxf.Point{X: 0, Y: 0, Z: 0}
	line1.P2 = dxf.Point{X: 100, Y: 0, Z: 0}
	drawing.Entities = append(drawing.Entities, line1)

	line2 := dxf.NewLine()
	line2.P1 = dxf.Point{X: 100, Y: 0, Z: 0}
	line2.P2 = dxf.Point{X: 100, Y: 100, Z: 0}
	drawing.Entities = append(drawing.Entities, line2)

	line3 := dxf.NewLine()
	line3.P1 = dxf.Point{X: 100, Y: 100, Z: 0}
	line3.P2 = dxf.Point{X: 0, Y: 100, Z: 0}
	drawing.Entities = append(drawing.Entities, line3)

	line4 := dxf.NewLine()
	line4.P1 = dxf.Point{X: 0, Y: 100, Z: 0}
	line4.P2 = dxf.Point{X: 0, Y: 0, Z: 0}
	drawing.Entities = append(drawing.Entities, line4)

	circle := dxf.NewCircle()
	circle.Center = dxf.Point{X: 50, Y: 50, Z: 0}
	circle.Radius = 30
	drawing.Entities = append(drawing.Entities, circle)

	text := dxf.NewText()
	text.Location = dxf.Point{X: 10, Y: 10, Z: 0}
	text.Value = "Test"
	text.Height = 5
	drawing.Entities = append(drawing.Entities, text)

	// Write DXF to temp file
	tmpDir := t.TempDir()
	dxfPath := filepath.Join(tmpDir, "test.dxf")
	pdfPath := filepath.Join(tmpDir, "test.pdf")

	err := drawing.SaveFile(dxfPath)
	if err != nil {
		t.Fatalf("saving DXF: %v", err)
	}

	// Convert
	result, err := Convert(dxfPath, pdfPath, Options{
		Scale:  "1:1",
		Paper:  "A4",
		Margin: 10,
		Align:  "center",
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	if result.Pages != 1 {
		t.Errorf("Pages = %d, want 1", result.Pages)
	}

	// Check PDF exists and is non-empty
	info, err := os.Stat(pdfPath)
	if err != nil {
		t.Fatalf("PDF not created: %v", err)
	}
	if info.Size() == 0 {
		t.Error("PDF is empty")
	}
}

func TestConvertTiled(t *testing.T) {
	drawing := dxf.NewDrawing()

	// Large drawing: 10000x5000 units
	line := dxf.NewLine()
	line.P1 = dxf.Point{X: 0, Y: 0, Z: 0}
	line.P2 = dxf.Point{X: 10000, Y: 5000, Z: 0}
	drawing.Entities = append(drawing.Entities, line)

	tmpDir := t.TempDir()
	dxfPath := filepath.Join(tmpDir, "big.dxf")
	pdfPath := filepath.Join(tmpDir, "big.pdf")

	err := drawing.SaveFile(dxfPath)
	if err != nil {
		t.Fatalf("saving DXF: %v", err)
	}

	result, err := Convert(dxfPath, pdfPath, Options{
		Scale:  "1:10",
		Paper:  "A4",
		Margin: 10,
		Tile:   true,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	// At 1:10, drawing is 1000x500mm. A4 printable ~190x277mm (landscape).
	// Should need multiple pages.
	if result.Pages <= 1 {
		t.Errorf("Pages = %d, want > 1", result.Pages)
	}

	info, err := os.Stat(pdfPath)
	if err != nil {
		t.Fatalf("PDF not created: %v", err)
	}
	if info.Size() == 0 {
		t.Error("PDF is empty")
	}
}

func TestLayerFilter(t *testing.T) {
	drawing := dxf.NewDrawing()

	line1 := dxf.NewLine()
	line1.SetLayer("Walls")
	line1.P1 = dxf.Point{X: 0, Y: 0, Z: 0}
	line1.P2 = dxf.Point{X: 100, Y: 0, Z: 0}
	drawing.Entities = append(drawing.Entities, line1)

	line2 := dxf.NewLine()
	line2.SetLayer("Doors")
	line2.P1 = dxf.Point{X: 0, Y: 100, Z: 0}
	line2.P2 = dxf.Point{X: 100, Y: 100, Z: 0}
	drawing.Entities = append(drawing.Entities, line2)

	tmpDir := t.TempDir()
	dxfPath := filepath.Join(tmpDir, "layers.dxf")
	pdfPath := filepath.Join(tmpDir, "layers.pdf")

	err := drawing.SaveFile(dxfPath)
	if err != nil {
		t.Fatalf("saving DXF: %v", err)
	}

	// Convert with only Walls layer
	result, err := Convert(dxfPath, pdfPath, Options{
		Scale:  "1:1",
		Paper:  "A4",
		Margin: 10,
		Layers: []string{"Walls"},
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.Pages != 1 {
		t.Errorf("Pages = %d, want 1", result.Pages)
	}
}

func TestStripMTextFormatting(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Hello", "Hello"},
		{`{\fArial;Hello}`, "Hello"},
		{`Line1\PLine2`, "Line1 Line2"},
		{`{\fArial|b1;Bold}`, "Bold"},
	}
	for _, tt := range tests {
		got := stripMTextFormatting(tt.input)
		if got != tt.want {
			t.Errorf("stripMTextFormatting(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
