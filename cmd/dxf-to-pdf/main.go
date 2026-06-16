package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/kedazo/go-dxf-to-pdf/converter"
)

type CLI struct {
	Input       string   `arg:"" help:"Input DXF or DWG file path."`
	Output      string   `arg:"" optional:"" help:"Output file path (not needed with --info or --list-layers)."`
	Scale       string   `optional:"" help:"Scale ratio (e.g. 1:100, 1:50, 1:1). Required for conversion."`
	Paper       string   `default:"A4" help:"Paper size: A0-A4 or WxH in mm (e.g. 400x300)."`
	Margin      float64  `default:"10" help:"Margin in mm (uniform on all sides)."`
	Align       string   `default:"center" enum:"center,bottom-left,top-left" help:"Drawing alignment on page."`
	Layers      []string `optional:"" help:"Include only these layers (comma-separated)."`
	Tile        bool     `help:"Tile drawing across multiple pages if it exceeds paper size."`
	Dwg2Dxf     string   `optional:"" help:"Path to dwg2dxf binary (for DWG files). Default: auto-detect from PATH."`
	DebugBBox   bool     `help:"Draw red bounding box rectangle on the output for debugging."`
	Crop        string   `optional:"" help:"Crop to bounding box in drawing units: minX,minY,maxX,maxY"`
	AutoPaper   bool     `help:"Auto-size paper to fit drawing at the given scale."`
	FontDir     string   `optional:"" help:"Directory containing DejaVuSans*.ttf font files (default: /usr/share/fonts/truetype/dejavu on Linux, executable dir on Windows)."`
	Format      string   `optional:"" help:"Output format: pdf, png, jpg (default: auto from file extension)."`
	DPI         float64  `default:"300" help:"DPI for raster output (PNG/JPG). Default: 300."`
	Transparent bool     `help:"Transparent PNG background (useful for layer compositing)."`
	Info        bool     `help:"Print drawing info (units, size, entity count, blocks) and exit."`
	ListLayers  bool     `help:"List all layers and exit."`

	EmitWallSegments  string  `optional:"" help:"Emit raw segments + detected walls to an XML file and exit."`
	WallMinThickness  float64 `default:"0.03" help:"Min wall thickness in meters (wall-pair lower bound)."`
	WallMaxThickness  float64 `default:"0.60" help:"Max wall thickness in meters (wall-pair upper bound)."`
	WallMinLength     float64 `default:"0.30" help:"Min candidate face length in meters (drops furniture ticks)."`
	WallAngleTol      float64 `default:"5" help:"Parallel-pair angle tolerance in degrees."`
	WallMergeGap      float64 `default:"0.05" help:"Collinear midline merge tolerance in meters."`
	WallBridgeGap     float64 `default:"1.20" help:"Max door/window gap to bridge when merging walls (meters)."`
	WallIncludeCurves bool    `help:"Include sampled ARC/CIRCLE chords in raw segments (never paired)."`

	SourceUnit         string   `optional:"" help:"Override source unit for wall emit: mm|cm|m|in|ft."`
	UnitScale          float64  `optional:"" help:"Override DXF-unit->meters factor for wall emit (wins over --source-unit)."`
	NoDefaultBlacklist bool     `help:"Disable the built-in junk-layer blacklist for wall detection."`
	ExcludeLayers      []string `optional:"" help:"Extra layer-name substrings to exclude from wall detection (comma-separated)."`
	WallLayers         []string `optional:"" help:"Only detect walls on layers matching these substrings (positive filter)."`
}

func main() {
	var cli CLI
	kong.Parse(&cli,
		kong.Name("dxf-to-pdf"),
		kong.Description("Convert DXF/DWG files to scaled PDF output."),
	)

	// Info-only modes
	if cli.Info || cli.ListLayers {
		info, err := converter.Inspect(cli.Input, cli.Dwg2Dxf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		if cli.ListLayers {
			for _, l := range info.Layers {
				vis := ""
				if !l.Visible {
					vis = " (hidden)"
				}
				fmt.Printf("%-30s %4d entities  color=#%02X%02X%02X%s\n",
					l.Name, l.EntityCount, l.Color.R, l.Color.G, l.Color.B, vis)
			}
			return
		}

		// --info
		bb := info.BoundingBox
		fmt.Printf("Units:      %s\n", info.Units)
		fmt.Printf("Entities:   %d\n", info.EntityCount)
		fmt.Printf("Layers:     %d\n", len(info.Layers))
		fmt.Printf("Blocks:     %d\n", len(info.Blocks))
		fmt.Printf("BBox:       (%.4f, %.4f) to (%.4f, %.4f)\n",
			bb.MinX, bb.MinY, bb.MaxX, bb.MaxY)
		fmt.Printf("Size:       %.2f x %.2f %s (%.0f x %.0f mm)\n",
			bb.Width(), bb.Height(), info.Units,
			bb.Width()*info.UnitFactor, bb.Height()*info.UnitFactor)

		if len(info.Layers) > 0 {
			fmt.Printf("\nLayers:\n")
			for _, l := range info.Layers {
				vis := ""
				if !l.Visible {
					vis = " (hidden)"
				}
				fmt.Printf("  %-30s %4d entities%s\n", l.Name, l.EntityCount, vis)
			}
		}

		if len(info.Blocks) > 0 {
			fmt.Printf("\nBlocks:\n")
			for _, b := range info.Blocks {
				fmt.Printf("  %s\n", b)
			}
		}
		return
	}

	// Emit wall segments mode — parse, write XML, exit (no rendering).
	if cli.EmitWallSegments != "" {
		scale := 0.01
		if cli.Scale != "" {
			if s, err := converter.ParseScale(cli.Scale); err == nil {
				scale = s
			}
		}
		res, err := converter.EmitWallSegments(cli.Input, cli.EmitWallSegments, converter.WallSegmentsOptions{
			Dwg2Dxf:            cli.Dwg2Dxf,
			Layers:             cli.Layers,
			MinThickness:       cli.WallMinThickness,
			MaxThickness:       cli.WallMaxThickness,
			MinWallLength:      cli.WallMinLength,
			AngleTolDeg:        cli.WallAngleTol,
			MergeGap:           cli.WallMergeGap,
			BridgeGap:          cli.WallBridgeGap,
			IncludeCurves:      cli.WallIncludeCurves,
			Scale:              scale,
			DPI:                cli.DPI,
			SourceUnit:         cli.SourceUnit,
			UnitScale:          cli.UnitScale,
			NoDefaultBlacklist: cli.NoDefaultBlacklist,
			ExcludeLayers:      cli.ExcludeLayers,
			WallLayers:         cli.WallLayers,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Wrote wall segments to %s\n", cli.EmitWallSegments)
		fmt.Printf("  scene:    %.2f x %.2f m  (units: %s)\n",
			res.SceneWidthMeters, res.SceneHeightMeters, res.Units)
		fmt.Printf("  segments: %d   walls: %d\n", res.SegmentCount, res.WallCount)
		if len(res.ThicknessClusters) > 0 {
			parts := make([]string, 0, len(res.ThicknessClusters))
			for _, b := range res.ThicknessClusters {
				parts = append(parts, fmt.Sprintf("%.2fm×%d", b.Thickness, b.Count))
			}
			fmt.Printf("  thickness clusters: %s\n", strings.Join(parts, ", "))
		}
		if res.UnitWarning != "" {
			fmt.Fprintf(os.Stderr, "warning: %s\n", res.UnitWarning)
		}
		return
	}

	// Conversion mode — require output and scale
	if cli.Output == "" {
		fmt.Fprintf(os.Stderr, "error: output file path is required for conversion\n")
		os.Exit(1)
	}
	if cli.Scale == "" {
		fmt.Fprintf(os.Stderr, "error: --scale is required for conversion\n")
		os.Exit(1)
	}

	result, err := converter.Convert(cli.Input, cli.Output, converter.Options{
		Scale:       cli.Scale,
		Paper:       cli.Paper,
		Margin:      cli.Margin,
		Align:       cli.Align,
		Layers:      cli.Layers,
		Tile:        cli.Tile,
		Dwg2Dxf:     cli.Dwg2Dxf,
		DebugBBox:   cli.DebugBBox,
		Crop:        cli.Crop,
		AutoPaper:   cli.AutoPaper,
		FontDir:     cli.FontDir,
		Format:      cli.Format,
		DPI:         cli.DPI,
		Transparent: cli.Transparent,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Drawing units: %s\n", result.Units)
	bb := result.BoundingBox
	fmt.Printf("Drawing size: %.2f x %.2f %s (%.0f x %.0f mm)\n",
		bb.Width(), bb.Height(), result.Units,
		bb.Width()*result.UnitFactor, bb.Height()*result.UnitFactor)
	fmt.Printf("Converted to %s (%d page(s))\n", cli.Output, result.Pages)
}
