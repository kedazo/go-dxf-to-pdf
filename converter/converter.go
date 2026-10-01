package converter

import (
	"bytes"
	"fmt"
	"io"
	"os"

	dxf "github.com/kedazo/dxf-go"
	"golang.org/x/text/encoding"
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
	if !IsDWG(inputPath) {
		drawing, err := readDxfFile(inputPath)
		if err != nil {
			return nil, fmt.Errorf("reading DXF: %w", err)
		}
		return &drawing, nil
	}

	bin, err := FindDwg2Dxf(dwg2dxfBin)
	if err != nil {
		return nil, err
	}

	// First attempt: full conversion. This preserves $DWGCODEPAGE and
	// $INSUNITS, which we need for text-encoding and unit auto-detection.
	tmpDxf, err := ConvertDWGtoDXF(inputPath, bin, false)
	if err != nil {
		return nil, err
	}
	drawing, err := readDxfFile(tmpDxf)
	os.Remove(tmpDxf)
	if err == nil {
		return &drawing, nil
	}

	// Fallback: some DWGs (e.g. AutoCAD 2018/AC1032 from LibreDWG) emit a
	// degraded HEADER section that the strict DXF parser rejects. Retry with a
	// minimal header, which omits the broken variables.
	fmt.Fprintf(os.Stderr, "warning: full DXF parse failed (%v); retrying with minimal header (dwg2dxf -m)\n", err)
	tmpMin, minErr := ConvertDWGtoDXF(inputPath, bin, true)
	if minErr != nil {
		return nil, fmt.Errorf("reading DXF: %w (minimal-header retry failed: %v)", err, minErr)
	}
	defer os.Remove(tmpMin)
	minDrawing, minErr := readDxfFile(tmpMin)
	if minErr != nil {
		return nil, fmt.Errorf("reading DXF: %w (minimal-header retry: %v)", err, minErr)
	}
	return &minDrawing, nil
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
	// The encoding scan and the parse both need the data, so keep it.
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading DXF: %w", err)
	}
	hints, err := sniffDxfEncoding(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("reading DXF: %w", err)
	}
	var enc encoding.Encoding = encoding.Nop
	if cm := hints.fallbackEncoding(); cm != nil {
		enc = cm
	}
	drawing, err := dxf.ReadFromReaderWithEncoding(bytes.NewReader(data), enc)
	if err != nil {
		return nil, fmt.Errorf("reading DXF: %w", err)
	}
	return convertDrawing(&drawing, pdfPath, opts)
}

// readDxfFile reads a DXF file, streaming it through the parser. Text
// decoding is left to the parser except for the cases fallbackEncoding
// covers (DXF files from Central/Eastern European CAD software are often
// Windows-1250 without saying so properly).
func readDxfFile(path string) (dxf.Drawing, error) {
	f, err := os.Open(path)
	if err != nil {
		return dxf.Drawing{}, err
	}
	hints, err := sniffDxfEncoding(f)
	f.Close()
	if err != nil {
		return dxf.Drawing{}, err
	}
	if cm := hints.fallbackEncoding(); cm != nil {
		return dxf.ReadFileWithEncoding(path, cm)
	}
	return dxf.ReadFile(path)
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

	format := opts.Format
	if format == "" {
		format = formatFromExtension(pdfPath)
	}
	isPDF := format == "pdf"

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
		r.SetBatching(isPDF)
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
		r.SetBatching(isPDF)
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
	renderer.SetBatching(isPDF)
	totalPages := grid.Cols * grid.Rows
	boxes := entityBoxes(drawing.Entities, blockMap)

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

			// Only entities touching this tile (plus a little slack for line
			// widths), clipped to the printable area.
			slack := 5 / effectiveScale // 5 mm on paper, in drawing units
			cullBox := BBox{
				MinX: tileBBox.MinX - slack, MinY: tileBBox.MinY - slack,
				MaxX: tileBBox.MaxX + slack, MaxY: tileBBox.MaxY + slack,
			}
			renderer.SetClipRect(margin, margin, printW, printH)
			RenderEntities(renderer, cullEntities(drawing.Entities, boxes, cullBox), layerMap, blockMap, layerFilter)
			renderer.ClipEnd()
			if !opts.Transparent {
				renderer.MaskOutside(margin, margin, printW, printH)
			}

			DrawCropMarks(renderer, margin, pw, ph)
		}
	}

	if err := renderer.Save(pdfPath, opts.Format, opts.DPI, opts.Transparent); err != nil {
		return nil, fmt.Errorf("saving PDF: %w", err)
	}

	return &Result{Pages: totalPages, BoundingBox: bbox, Units: unitName, UnitFactor: unitFactor}, nil
}
