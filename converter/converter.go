package converter

import (
	"bytes"
	"fmt"
	"io"
	"os"

	dxf "github.com/ixmilia/dxf-go"
	"golang.org/x/text/encoding/charmap"
)

type Options struct {
	Scale   string   // e.g. "1:100"
	Paper   string   // e.g. "A4", "A3", "400x300"
	Margin  float64  // mm, uniform on all sides
	Align   string   // "center", "bottom-left", "top-left"
	Layers  []string // nil = all layers
	Tile    bool
	Dwg2Dxf     string  // explicit path to dwg2dxf binary (empty = auto-detect)
	DebugBBox   bool    // draw bounding box rectangle on the output
	Crop        string  // explicit bounding box "minX,minY,maxX,maxY" in drawing units (overrides auto-detection)
	AutoPaper   bool    // calculate paper size from drawing dimensions at the given scale
	FontDir     string  // path to directory containing DejaVuSans*.ttf files (empty = default)
	Format      string  // output format: "pdf", "png", "jpg" (default: auto from extension)
	DPI         float64 // raster output DPI (default: 300)
	Transparent bool    // transparent PNG background (default: false)
}

type Result struct {
	Pages       int
	BoundingBox BBox
	Units       string  // detected drawing units (e.g. "meters", "millimeters")
	UnitFactor  float64 // conversion factor from drawing units to mm
}

// LayerInfo describes a single layer in the drawing.
type LayerInfo struct {
	Name       string
	Color      RGB
	Visible    bool
	EntityCount int
}

// DrawingInfo contains metadata about a DXF/DWG drawing.
type DrawingInfo struct {
	Units       string
	UnitFactor  float64
	BoundingBox BBox
	Layers      []LayerInfo
	Blocks      []string
	EntityCount int
}

// Inspect reads a DXF/DWG file and returns metadata without converting.
func Inspect(inputPath string, dwg2dxf string) (*DrawingInfo, error) {
	drawing, err := loadDrawing(inputPath, dwg2dxf)
	if err != nil {
		return nil, err
	}

	blockMap := make(map[string]*dxf.Block)
	for i := range drawing.Blocks {
		blockMap[drawing.Blocks[i].Name] = &drawing.Blocks[i]
	}

	// Count entities per layer
	layerCounts := make(map[string]int)
	for _, ent := range drawing.Entities {
		layerCounts[ent.Layer()]++
	}

	// Build layer info
	layers := make([]LayerInfo, 0, len(drawing.Layers))
	for _, l := range drawing.Layers {
		// DXF layer flags: bit 1 = frozen
		// Note: negative color traditionally means "layer off", but dwg2dxf
		// often exports all colors as negative regardless, so we use abs for color
		// and only check flags for frozen state.
		frozen := l.Flags&1 != 0
		colorVal := int16(l.Color)
		if colorVal < 0 {
			colorVal = -colorVal
		}
		layers = append(layers, LayerInfo{
			Name:        l.Name,
			Color:       ACIToRGB(colorVal),
			Visible:     !frozen,
			EntityCount: layerCounts[l.Name],
		})
	}

	// Block names (skip model/paper space internal blocks)
	var blockNames []string
	for _, b := range drawing.Blocks {
		if b.Name != "" && b.Name[0] != '*' {
			blockNames = append(blockNames, b.Name)
		}
	}

	bbox := ComputeBoundingBox(drawing.Entities, blockMap)

	return &DrawingInfo{
		Units:       UnitsName(drawing.Header.DefaultDrawingUnits),
		UnitFactor:  UnitsToMM(drawing.Header.DefaultDrawingUnits),
		BoundingBox: bbox,
		Layers:      layers,
		Blocks:      blockNames,
		EntityCount: len(drawing.Entities),
	}, nil
}

// loadDrawing handles DWG conversion and DXF parsing.
func loadDrawing(inputPath string, dwg2dxfBin string) (*dxf.Drawing, error) {
	dxfPath := inputPath

	if IsDWG(inputPath) {
		bin, err := FindDwg2Dxf(dwg2dxfBin)
		if err != nil {
			return nil, err
		}
		tmpDxf, err := ConvertDWGtoDXF(inputPath, bin)
		if err != nil {
			return nil, err
		}
		defer os.Remove(tmpDxf)
		dxfPath = tmpDxf
	}

	drawing, err := readDxfFile(dxfPath)
	if err != nil {
		return nil, fmt.Errorf("reading DXF: %w", err)
	}
	return &drawing, nil
}

// Convert converts a DXF or DWG file to PDF, PNG, or JPG.
func Convert(inputPath, outputPath string, opts Options) (*Result, error) {
	drawing, err := loadDrawing(inputPath, opts.Dwg2Dxf)
	if err != nil {
		return nil, err
	}
	return convertDrawing(drawing, outputPath, opts)
}

// ConvertReader converts a DXF from a reader to a PDF writer.
func ConvertReader(r io.Reader, pdfPath string, opts Options) (*Result, error) {
	drawing, err := dxf.ReadFromReader(r)
	if err != nil {
		return nil, fmt.Errorf("reading DXF: %w", err)
	}
	return convertDrawing(&drawing, pdfPath, opts)
}

// readDxfFile reads a DXF file, auto-detecting the text encoding.
// DXF files from Central/Eastern European CAD software often use Windows-1250.
// We detect this by checking for $DWGCODEPAGE or high bytes, and convert to UTF-8.
func readDxfFile(path string) (dxf.Drawing, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return dxf.Drawing{}, err
	}

	// Detect encoding from $DWGCODEPAGE header or presence of high bytes with c238 markers
	enc := detectDxfEncoding(data)
	if enc != nil {
		data, err = enc.NewDecoder().Bytes(data)
		if err != nil {
			return dxf.Drawing{}, fmt.Errorf("decoding DXF text: %w", err)
		}
	}

	return dxf.ReadFromReader(bytes.NewReader(data))
}

// detectDxfEncoding checks the raw DXF bytes for encoding hints.
// Returns nil if the file appears to be UTF-8 or ASCII.
func detectDxfEncoding(data []byte) *charmap.Charmap {
	// Check for $DWGCODEPAGE header
	s := string(data)
	if idx := bytes.Index(data, []byte("$DWGCODEPAGE")); idx >= 0 {
		// Look for the codepage value (next non-blank value after group code 3)
		rest := s[idx:]
		if cp := extractCodepage(rest); cp != "" {
			switch cp {
			case "ANSI_1250":
				return charmap.Windows1250
			case "ANSI_1251":
				return charmap.Windows1251
			case "ANSI_1252":
				return charmap.Windows1252
			case "ANSI_1253":
				return charmap.Windows1253
			case "ANSI_1254":
				return charmap.Windows1254
			case "ANSI_1255":
				return charmap.Windows1255
			case "ANSI_1256":
				return charmap.Windows1256
			case "ANSI_1257":
				return charmap.Windows1257
			}
		}
	}

	// Fallback: if file has high bytes and contains c238 MText markers, assume Windows-1250
	hasHigh := false
	for _, b := range data {
		if b >= 0x80 {
			hasHigh = true
			break
		}
	}
	if hasHigh && bytes.Contains(data, []byte("c238")) {
		return charmap.Windows1250
	}

	return nil
}

// extractCodepage extracts the codepage value from text starting at $DWGCODEPAGE.
func extractCodepage(s string) string {
	// Format: $DWGCODEPAGE\n  3\nANSI_1250\n
	lines := bytes.Split([]byte(s), []byte{'\n'})
	for i := 0; i < len(lines)-2; i++ {
		trimmed := bytes.TrimSpace(lines[i])
		if string(trimmed) == "$DWGCODEPAGE" {
			// Next line should be group code 3, line after is the value
			if i+2 < len(lines) {
				return string(bytes.TrimSpace(lines[i+2]))
			}
		}
	}
	return ""
}

func convertDrawing(drawing *dxf.Drawing, pdfPath string, opts Options) (*Result, error) {
	scale, err := ParseScale(opts.Scale)
	if err != nil {
		return nil, err
	}

	paper, err := ParsePaperSize(opts.Paper)
	if err != nil {
		return nil, err
	}

	margin := opts.Margin
	if margin < 0 {
		margin = 10
	}

	align := ParseAlignment(opts.Align)

	// Detect drawing units and compute the unit-to-mm factor
	unitFactor := UnitsToMM(drawing.Header.DefaultDrawingUnits)
	unitName := UnitsName(drawing.Header.DefaultDrawingUnits)

	// The effective scale converts DXF units to mm on paper:
	// effectiveScale = (unitFactor * scale)
	// e.g. meters + 1:100 → 1000 * 0.01 = 10 mm per DXF unit
	effectiveScale := unitFactor * scale

	// Build layer lookup
	layerMap := make(map[string]dxf.Layer)
	for _, l := range drawing.Layers {
		layerMap[l.Name] = l
	}

	// Build block lookup
	blockMap := make(map[string]*dxf.Block)
	for i := range drawing.Blocks {
		blockMap[drawing.Blocks[i].Name] = &drawing.Blocks[i]
	}

	// Layer filter
	var layerFilter map[string]bool
	if len(opts.Layers) > 0 {
		layerFilter = make(map[string]bool)
		for _, l := range opts.Layers {
			layerFilter[l] = true
		}
	}

	// Compute bounding box
	bbox := ComputeBoundingBox(drawing.Entities, blockMap)
	if bbox.Width() <= 0 || bbox.Height() <= 0 {
		return nil, fmt.Errorf("empty drawing or no renderable entities")
	}

	// Override bounding box if explicit crop is provided
	if opts.Crop != "" {
		cropBBox, err := ParseCrop(opts.Crop)
		if err != nil {
			return nil, err
		}
		bbox = cropBBox
	}

	// Auto-paper: calculate paper size from drawing dimensions at the given scale
	if opts.AutoPaper {
		drawW := bbox.Width() * effectiveScale
		drawH := bbox.Height() * effectiveScale
		paper = PaperSize{
			Name:   "auto",
			Width:  drawW + 2*margin,
			Height: drawH + 2*margin,
		}
		// Paper is exactly sized — render directly without landscape swap or auto-fit
		r := NewRenderer(paper, false, margin, opts.FontDir)
		t := NewTransform(bbox, effectiveScale, paper, margin, ParseAlignment(opts.Align), false)
		r.SetTransform(t)
		RenderEntities(r, drawing.Entities, layerMap, blockMap, layerFilter)
		if opts.DebugBBox {
			r.DrawDebugBBox(bbox)
		}
		if err := r.Save(pdfPath, opts.Format, opts.DPI, opts.Transparent); err != nil {
			return nil, fmt.Errorf("saving PDF: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Paper size: %.0f x %.0f mm\n", paper.Width, paper.Height)
		return &Result{Pages: 1, BoundingBox: bbox, Units: unitName, UnitFactor: unitFactor}, nil
	}

	landscape := ShouldUseLandscape(bbox, paper)

	if !opts.Tile {
		// Single page — if drawing exceeds paper, scale down to fit
		pw, ph := paper.Width, paper.Height
		if landscape {
			pw, ph = ph, pw
		}
		printW := pw - 2*margin
		printH := ph - 2*margin
		drawW := bbox.Width() * effectiveScale
		drawH := bbox.Height() * effectiveScale
		fitScale := effectiveScale
		if drawW > printW || drawH > printH {
			fitX := printW / bbox.Width()
			fitY := printH / bbox.Height()
			fitScale = fitX
			if fitY < fitX {
				fitScale = fitY
			}
			fmt.Fprintf(os.Stderr, "Note: drawing exceeds paper at requested scale, auto-fitting (effective scale ~1:%.0f)\n",
				unitFactor/fitScale)
		}
		t := NewTransform(bbox, fitScale, paper, margin, align, landscape)
		r := NewRenderer(paper, landscape, margin, opts.FontDir)
		r.SetTransform(t)
		RenderEntities(r, drawing.Entities, layerMap, blockMap, layerFilter)

		if opts.DebugBBox {
			r.DrawDebugBBox(bbox)
		}

		if err := r.Save(pdfPath, opts.Format, opts.DPI, opts.Transparent); err != nil {
			return nil, fmt.Errorf("saving PDF: %w", err)
		}
		return &Result{Pages: 1, BoundingBox: bbox, Units: unitName, UnitFactor: unitFactor}, nil
	}

	// Tiled output
	pw, ph := paper.Width, paper.Height
	if landscape {
		pw, ph = ph, pw
	}
	printW := pw - 2*margin
	printH := ph - 2*margin

	drawW := bbox.Width() * effectiveScale
	drawH := bbox.Height() * effectiveScale

	grid := ComputeTileGrid(drawW, drawH, printW, printH)

	renderer := NewRenderer(paper, landscape, margin, opts.FontDir)
	totalPages := grid.Cols * grid.Rows

	for row := 0; row < grid.Rows; row++ {
		for col := 0; col < grid.Cols; col++ {
			if row > 0 || col > 0 {
				renderer.AddPage()
			}

			// Compute the DXF region this tile covers
			tileMinX := bbox.MinX + float64(col)*printW/effectiveScale
			tileMaxY := bbox.MaxY - float64(row)*printH/effectiveScale

			tileBBox := BBox{
				MinX: tileMinX,
				MinY: tileMaxY - printH/effectiveScale,
				MaxX: tileMinX + printW/effectiveScale,
				MaxY: tileMaxY,
			}

			t := NewTransform(tileBBox, effectiveScale, paper, margin, AlignTopLeft, landscape)
			renderer.SetTransform(t)

			renderer.SetClipRect(margin, margin, printW, printH)
			RenderEntities(renderer, drawing.Entities, layerMap, blockMap, layerFilter)
			renderer.ClipEnd()

			DrawCropMarks(renderer, margin, pw, ph)
		}
	}

	if err := renderer.Save(pdfPath, opts.Format, opts.DPI, opts.Transparent); err != nil {
		return nil, fmt.Errorf("saving PDF: %w", err)
	}

	return &Result{Pages: totalPages, BoundingBox: bbox, Units: unitName, UnitFactor: unitFactor}, nil
}
