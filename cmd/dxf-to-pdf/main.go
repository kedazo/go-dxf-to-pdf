package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	"github.com/kedazo/go-dxf-to-pdf/converter"
)

type CLI struct {
	Input   string   `arg:"" help:"Input DXF or DWG file path."`
	Output  string   `arg:"" optional:"" help:"Output PDF file path (not needed with --info or --list-layers)."`
	Scale   string   `optional:"" help:"Scale ratio (e.g. 1:100, 1:50, 1:1). Required for conversion."`
	Paper   string   `default:"A4" help:"Paper size: A0-A4 or WxH in mm (e.g. 400x300)."`
	Margin  float64  `default:"10" help:"Margin in mm (uniform on all sides)."`
	Align   string   `default:"center" enum:"center,bottom-left,top-left" help:"Drawing alignment on page."`
	Layers  []string `optional:"" help:"Include only these layers (comma-separated)."`
	Tile    bool     `help:"Tile drawing across multiple pages if it exceeds paper size."`
	Dwg2Dxf   string `optional:"" help:"Path to dwg2dxf binary (for DWG files). Default: auto-detect from PATH."`
	DebugBBox bool   `help:"Draw red bounding box rectangle on the PDF for debugging."`
	Crop      string `optional:"" help:"Crop to bounding box in drawing units: minX,minY,maxX,maxY"`
	AutoPaper bool   `help:"Auto-size paper to fit drawing at the given scale."`
	FontDir   string `optional:"" help:"Directory containing DejaVuSans*.ttf font files (default: /usr/share/fonts/truetype/dejavu on Linux, executable dir on Windows)."`

	Info       bool `help:"Print drawing info (units, size, entity count, blocks) and exit."`
	ListLayers bool `help:"List all layers and exit."`
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
		Scale:  cli.Scale,
		Paper:  cli.Paper,
		Margin: cli.Margin,
		Align:  cli.Align,
		Layers: cli.Layers,
		Tile:    cli.Tile,
		Dwg2Dxf:   cli.Dwg2Dxf,
		DebugBBox: cli.DebugBBox,
		Crop:      cli.Crop,
		AutoPaper: cli.AutoPaper,
		FontDir:   cli.FontDir,
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
