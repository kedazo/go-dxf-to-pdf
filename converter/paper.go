package converter

import (
	"fmt"
	"strconv"
	"strings"

	dxf "github.com/ixmilia/dxf-go"
)

type PaperSize struct {
	Name   string
	Width  float64 // mm
	Height float64 // mm
}

var PredefinedPapers = map[string]PaperSize{
	// ISO A series (mm)
	"A0":  {Name: "A0", Width: 841, Height: 1189},
	"A1":  {Name: "A1", Width: 594, Height: 841},
	"A2":  {Name: "A2", Width: 420, Height: 594},
	"A3":  {Name: "A3", Width: 297, Height: 420},
	"A4":  {Name: "A4", Width: 210, Height: 297},
	"A5":  {Name: "A5", Width: 148, Height: 210},
	"A6":  {Name: "A6", Width: 105, Height: 148},
	"A7":  {Name: "A7", Width: 74, Height: 105},
	"A8":  {Name: "A8", Width: 52, Height: 74},
	"A9":  {Name: "A9", Width: 37, Height: 52},
	"A10": {Name: "A10", Width: 26, Height: 37},

	// ISO B series (mm)
	"B0":  {Name: "B0", Width: 1000, Height: 1414},
	"B1":  {Name: "B1", Width: 707, Height: 1000},
	"B2":  {Name: "B2", Width: 500, Height: 707},
	"B3":  {Name: "B3", Width: 353, Height: 500},
	"B4":  {Name: "B4", Width: 250, Height: 353},
	"B5":  {Name: "B5", Width: 176, Height: 250},
	"B6":  {Name: "B6", Width: 125, Height: 176},
	"B7":  {Name: "B7", Width: 88, Height: 125},
	"B8":  {Name: "B8", Width: 62, Height: 88},
	"B9":  {Name: "B9", Width: 44, Height: 62},
	"B10": {Name: "B10", Width: 31, Height: 44},

	// ISO C series — envelopes (mm)
	"C0": {Name: "C0", Width: 917, Height: 1297},
	"C1": {Name: "C1", Width: 648, Height: 917},
	"C2": {Name: "C2", Width: 458, Height: 648},
	"C3": {Name: "C3", Width: 324, Height: 458},
	"C4": {Name: "C4", Width: 229, Height: 324},
	"C5": {Name: "C5", Width: 162, Height: 229},
	"C6": {Name: "C6", Width: 114, Height: 162},

	// US / Imperial (mm, converted from inches)
	"LETTER":      {Name: "Letter", Width: 215.9, Height: 279.4},     // 8.5 x 11 in
	"LEGAL":       {Name: "Legal", Width: 215.9, Height: 355.6},      // 8.5 x 14 in
	"TABLOID":     {Name: "Tabloid", Width: 279.4, Height: 431.8},    // 11 x 17 in (aka Ledger)
	"LEDGER":      {Name: "Ledger", Width: 431.8, Height: 279.4},     // 17 x 11 in (landscape Tabloid)
	"EXECUTIVE":   {Name: "Executive", Width: 184.15, Height: 266.7}, // 7.25 x 10.5 in
	"STATEMENT":   {Name: "Statement", Width: 139.7, Height: 215.9},  // 5.5 x 8.5 in (aka Half Letter)
	"JUNIORLEGAL": {Name: "JuniorLegal", Width: 127, Height: 203.2},  // 5 x 8 in
	"FOLIO":       {Name: "Folio", Width: 215.9, Height: 330.2},      // 8.5 x 13 in
	"GOVTLETTER":  {Name: "GovtLetter", Width: 203.2, Height: 266.7}, // 8 x 10.5 in
	"GOVTLEGAL":   {Name: "GovtLegal", Width: 215.9, Height: 330.2},  // 8.5 x 13 in

	// ANSI series (mm)
	"ANSI A": {Name: "ANSI A", Width: 215.9, Height: 279.4}, // = Letter
	"ANSI B": {Name: "ANSI B", Width: 279.4, Height: 431.8}, // = Tabloid
	"ANSI C": {Name: "ANSI C", Width: 431.8, Height: 558.8},
	"ANSI D": {Name: "ANSI D", Width: 558.8, Height: 863.6},
	"ANSI E": {Name: "ANSI E", Width: 863.6, Height: 1117.6},

	// Architectural series (mm)
	"ARCH A":  {Name: "Arch A", Width: 228.6, Height: 304.8},  // 9 x 12 in
	"ARCH B":  {Name: "Arch B", Width: 304.8, Height: 457.2},  // 12 x 18 in
	"ARCH C":  {Name: "Arch C", Width: 457.2, Height: 609.6},  // 18 x 24 in
	"ARCH D":  {Name: "Arch D", Width: 609.6, Height: 914.4},  // 24 x 36 in
	"ARCH E":  {Name: "Arch E", Width: 914.4, Height: 1219.2}, // 36 x 48 in
	"ARCH E1": {Name: "Arch E1", Width: 762, Height: 1066.8},  // 30 x 42 in
}

func ParsePaperSize(s string) (PaperSize, error) {
	upper := strings.ToUpper(s)
	if p, ok := PredefinedPapers[upper]; ok {
		return p, nil
	}
	parts := strings.SplitN(s, "x", 2)
	if len(parts) != 2 {
		return PaperSize{}, fmt.Errorf("invalid paper size %q: use the name of a paper size (e.g. Letter or \"ANSI A\") or WxH in mm (e.g. 400x300)", s)
	}
	w, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil || w <= 0 {
		return PaperSize{}, fmt.Errorf("invalid paper width %q", parts[0])
	}
	h, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil || h <= 0 {
		return PaperSize{}, fmt.Errorf("invalid paper height %q", parts[1])
	}
	return PaperSize{Name: s, Width: w, Height: h}, nil
}

func ParseScale(s string) (float64, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid scale %q: use format N:M (e.g. 1:100)", s)
	}
	num, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil || num <= 0 {
		return 0, fmt.Errorf("invalid scale numerator %q", parts[0])
	}
	den, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil || den <= 0 {
		return 0, fmt.Errorf("invalid scale denominator %q", parts[1])
	}
	return num / den, nil
}

// ParseCrop parses a crop specification "minX,minY,maxX,maxY" into a BBox.
func ParseCrop(s string) (BBox, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return BBox{}, fmt.Errorf("invalid crop %q: use minX,minY,maxX,maxY", s)
	}
	vals := make([]float64, 4)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return BBox{}, fmt.Errorf("invalid crop value %q: %w", p, err)
		}
		vals[i] = v
	}
	return BBox{MinX: vals[0], MinY: vals[1], MaxX: vals[2], MaxY: vals[3]}, nil
}

// UnitsToMM returns the conversion factor from DXF drawing units to millimeters.
// Returns 1.0 if units are already mm or unitless.
func UnitsToMM(units dxf.Units) float64 {
	switch units {
	case dxf.UnitsInches:
		return 25.4
	case dxf.UnitsFeet:
		return 304.8
	case dxf.UnitsMiles:
		return 1_609_344
	case dxf.UnitsMillimeters:
		return 1
	case dxf.UnitsCentimeters:
		return 10
	case dxf.UnitsMeters:
		return 1000
	case dxf.UnitsKilometers:
		return 1_000_000
	case dxf.UnitsMicroinches:
		return 0.0000254
	case dxf.UnitsMils:
		return 0.0254
	case dxf.UnitsYards:
		return 914.4
	default:
		return 1
	}
}

// UnitsName returns a human-readable name for the drawing units.
func UnitsName(units dxf.Units) string {
	switch units {
	case dxf.UnitsInches:
		return "inches"
	case dxf.UnitsFeet:
		return "feet"
	case dxf.UnitsMiles:
		return "miles"
	case dxf.UnitsMillimeters:
		return "millimeters"
	case dxf.UnitsCentimeters:
		return "centimeters"
	case dxf.UnitsMeters:
		return "meters"
	case dxf.UnitsKilometers:
		return "kilometers"
	case dxf.UnitsYards:
		return "yards"
	default:
		return "unitless"
	}
}
