package converter

import (
	"encoding/xml"
	"math"
	"os"
	"path/filepath"
	"testing"

	dxf "github.com/ixmilia/dxf-go"
)

// addLine appends a LINE on the given layer. Coordinates are in millimeters
// (the test drawings declare millimeter units, so 1 mm -> 0.001 scene meters).
func addLine(d *dxf.Drawing, layer string, x1, y1, x2, y2 float64) {
	l := dxf.NewLine()
	l.SetLayer(layer)
	l.P1 = dxf.Point{X: x1, Y: y1, Z: 0}
	l.P2 = dxf.Point{X: x2, Y: y2, Z: 0}
	d.Entities = append(d.Entities, l)
}

// emitAndParse writes the drawing to a temp DXF, runs EmitWallSegments, and
// returns the parsed document plus the run summary.
func emitAndParse(t *testing.T, d *dxf.Drawing, opts WallSegmentsOptions) (WallSegmentsDoc, *WallSegmentsResult) {
	t.Helper()
	tmpDir := t.TempDir()
	dxfPath := filepath.Join(tmpDir, "in.dxf")
	xmlPath := filepath.Join(tmpDir, "out.xml")

	if err := d.SaveFile(dxfPath); err != nil {
		t.Fatalf("saving DXF: %v", err)
	}
	res, err := EmitWallSegments(dxfPath, xmlPath, opts)
	if err != nil {
		t.Fatalf("EmitWallSegments: %v", err)
	}
	data, err := os.ReadFile(xmlPath)
	if err != nil {
		t.Fatalf("reading XML: %v", err)
	}
	var doc WallSegmentsDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal XML: %v", err)
	}
	return doc, res
}

func TestEmitWallSegmentsParallelPair(t *testing.T) {
	d := dxf.NewDrawing()
	d.Header.DefaultDrawingUnits = dxf.UnitsMillimeters
	// Two parallel faces 300 mm apart over 4000 mm -> one 0.30 m thick, 4 m wall.
	addLine(d, "WALL", 0, 0, 4000, 0)
	addLine(d, "WALL", 0, 300, 4000, 300)
	// A lone diagonal: long enough to be a candidate but with no parallel partner.
	addLine(d, "WALL", 0, 0, 1000, 1000)

	doc, _ := emitAndParse(t, d, WallSegmentsOptions{})

	if doc.Meta.Units != "meters" || doc.Meta.YAxis != "down" {
		t.Errorf("meta = %+v, want units=meters yAxis=down", doc.Meta)
	}
	if doc.Meta.UnitToMeters <= 0 {
		t.Errorf("unitToMeters = %v, want > 0", doc.Meta.UnitToMeters)
	}
	if len(doc.Segments) != 3 {
		t.Fatalf("segments = %d, want 3", len(doc.Segments))
	}
	if len(doc.Walls) != 1 {
		t.Fatalf("walls = %d, want 1\n%+v", len(doc.Walls), doc.Walls)
	}

	w := doc.Walls[0]
	if math.Abs(w.Thickness-0.30) > 0.01 {
		t.Errorf("thickness = %.4f, want ~0.30", w.Thickness)
	}
	if math.Abs(w.Length-4.0) > 0.05 {
		t.Errorf("length = %.4f, want ~4.0", w.Length)
	}
	if w.Layer != "WALL" {
		t.Errorf("layer = %q, want WALL", w.Layer)
	}
	// Centerline sits midway between the two faces (scene Y, midway at 0.85 m).
	if math.Abs(w.Y1-0.85) > 0.02 || math.Abs(w.Y2-0.85) > 0.02 {
		t.Errorf("centerline Y = (%.4f, %.4f), want ~0.85", w.Y1, w.Y2)
	}
}

func TestEmitWallSegmentsLayerRestrict(t *testing.T) {
	d := dxf.NewDrawing()
	d.Header.DefaultDrawingUnits = dxf.UnitsMillimeters
	// Two distinct, non-blacklisted layers each forming a pair.
	addLine(d, "WALL", 0, 0, 4000, 0)
	addLine(d, "WALL", 0, 300, 4000, 300)
	addLine(d, "EXTRA", 0, 2000, 4000, 2000)
	addLine(d, "EXTRA", 0, 2300, 4000, 2300)

	// Without filter: both pairs detected.
	all, _ := emitAndParse(t, d, WallSegmentsOptions{})
	if len(all.Walls) != 2 {
		t.Fatalf("unfiltered walls = %d, want 2", len(all.Walls))
	}

	// Restrict to WALL: only one pair, and no EXTRA segments leak through.
	only, _ := emitAndParse(t, d, WallSegmentsOptions{Layers: []string{"WALL"}})
	if len(only.Walls) != 1 {
		t.Fatalf("filtered walls = %d, want 1", len(only.Walls))
	}
	for _, s := range only.Segments {
		if s.Layer != "WALL" {
			t.Errorf("segment layer = %q, want only WALL", s.Layer)
		}
	}
}

func TestEmitWallSegmentsTooFarNoPair(t *testing.T) {
	d := dxf.NewDrawing()
	d.Header.DefaultDrawingUnits = dxf.UnitsMillimeters
	// 2000 mm = 2 m apart, beyond the default 0.60 m max thickness -> no wall.
	addLine(d, "WALL", 0, 0, 4000, 0)
	addLine(d, "WALL", 0, 2000, 4000, 2000)

	doc, _ := emitAndParse(t, d, WallSegmentsOptions{})
	if len(doc.Segments) != 2 {
		t.Errorf("segments = %d, want 2", len(doc.Segments))
	}
	if len(doc.Walls) != 0 {
		t.Errorf("walls = %d, want 0", len(doc.Walls))
	}
}

func TestEmitWallSegmentsThinWallsDefault(t *testing.T) {
	d := dxf.NewDrawing()
	d.Header.DefaultDrawingUnits = dxf.UnitsMillimeters
	// Faces 40 mm (0.04 m) apart — below the OLD 0.05 default, at/above the new 0.03.
	addLine(d, "WALL", 0, 0, 2000, 0)
	addLine(d, "WALL", 0, 40, 2000, 40)

	// New default min-thickness 0.03 detects the thin wall.
	doc, _ := emitAndParse(t, d, WallSegmentsOptions{})
	if len(doc.Walls) != 1 {
		t.Fatalf("default walls = %d, want 1 (thin-wall regression)", len(doc.Walls))
	}
	if math.Abs(doc.Walls[0].Thickness-0.04) > 0.005 {
		t.Errorf("thickness = %.4f, want ~0.04", doc.Walls[0].Thickness)
	}

	// An explicit higher min-thickness rejects it again.
	doc2, _ := emitAndParse(t, d, WallSegmentsOptions{MinThickness: 0.05})
	if len(doc2.Walls) != 0 {
		t.Errorf("walls with MinThickness 0.05 = %d, want 0", len(doc2.Walls))
	}
}

func TestEmitWallSegmentsMaxThicknessDecoupling(t *testing.T) {
	d := dxf.NewDrawing()
	d.Header.DefaultDrawingUnits = dxf.UnitsMillimeters
	// Short (0.5 m) wall faces 300 mm apart. Under the OLD coupling
	// (minWallLen = MaxThickness), a high max thickness excluded these short
	// faces; with the decoupled MinWallLength (0.30) they survive.
	addLine(d, "WALL", 0, 0, 500, 0)
	addLine(d, "WALL", 0, 300, 500, 300)

	doc, _ := emitAndParse(t, d, WallSegmentsOptions{MaxThickness: 0.8})
	if len(doc.Walls) != 1 {
		t.Fatalf("walls with high MaxThickness = %d, want 1 (decoupling)", len(doc.Walls))
	}
}

func TestEmitWallSegmentsUnitOverride(t *testing.T) {
	newUnitless := func() *dxf.Drawing {
		d := dxf.NewDrawing()
		d.Header.DefaultDrawingUnits = dxf.UnitsUnitless
		// Coordinates in meter-magnitude; only correct if treated as meters.
		addLine(d, "WALL", 0, 0, 4, 0)
		addLine(d, "WALL", 0, 0.3, 4, 0.3)
		return d
	}

	// No override: unitless is assumed mm, the 0.3-unit gap collapses to 0.3 mm
	// -> no wall, and a warning is emitted.
	doc, res := emitAndParse(t, newUnitless(), WallSegmentsOptions{})
	if res.UnitWarning == "" {
		t.Error("expected a unit warning for unitless input without override")
	}
	if len(doc.Walls) != 0 {
		t.Errorf("walls without override = %d, want 0", len(doc.Walls))
	}

	// Override to meters: gap becomes 0.3 m -> one wall, no warning.
	doc2, res2 := emitAndParse(t, newUnitless(), WallSegmentsOptions{SourceUnit: "m"})
	if !res2.UnitsOverridden || res2.UnitWarning != "" {
		t.Errorf("override result = %+v, want overridden & no warning", res2)
	}
	if len(doc2.Walls) != 1 {
		t.Fatalf("walls with --source-unit m = %d, want 1", len(doc2.Walls))
	}
	if math.Abs(doc2.Walls[0].Thickness-0.3) > 0.01 {
		t.Errorf("thickness = %.4f, want ~0.30", doc2.Walls[0].Thickness)
	}
}

func TestEmitWallSegmentsBlacklist(t *testing.T) {
	build := func() *dxf.Drawing {
		d := dxf.NewDrawing()
		d.Header.DefaultDrawingUnits = dxf.UnitsMillimeters
		addLine(d, "A-WALL", 0, 0, 4000, 0)
		addLine(d, "A-WALL", 0, 300, 4000, 300)
		addLine(d, "Beltér - berendezés", 0, 2000, 4000, 2000) // furniture
		addLine(d, "Beltér - berendezés", 0, 2300, 4000, 2300)
		addLine(d, "A-DOOR", 0, 5000, 4000, 5000) // opening
		addLine(d, "A-DOOR", 0, 5300, 4000, 5300)
		return d
	}

	// Default blacklist: only the A-WALL pair becomes a wall...
	doc, _ := emitAndParse(t, build(), WallSegmentsOptions{})
	if len(doc.Walls) != 1 || doc.Walls[0].Layer != "A-WALL" {
		t.Fatalf("default walls = %+v, want 1 on A-WALL", doc.Walls)
	}
	// ...but raw segments still contain every layer (detection-only filtering).
	if len(doc.Segments) != 6 {
		t.Errorf("segments = %d, want 6 (raw keeps all layers)", len(doc.Segments))
	}

	// Disabling the blacklist brings furniture + door pairs back.
	none, _ := emitAndParse(t, build(), WallSegmentsOptions{NoDefaultBlacklist: true})
	if len(none.Walls) != 3 {
		t.Errorf("walls with NoDefaultBlacklist = %d, want 3", len(none.Walls))
	}

	// Positive whitelist: only layers matching "wall".
	white, _ := emitAndParse(t, build(), WallSegmentsOptions{WallLayers: []string{"wall"}})
	if len(white.Walls) != 1 || white.Walls[0].Layer != "A-WALL" {
		t.Errorf("walls with WallLayers=[wall] = %+v, want 1 on A-WALL", white.Walls)
	}
}
