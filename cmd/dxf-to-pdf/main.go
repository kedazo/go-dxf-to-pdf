package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/kedazo/go-dxf-to-pdf/converter"
)

type CLI struct {
	Input       string   `arg:"" help:"Input DXF or DWG file path."`
	Output      string   `arg:"" optional:"" help:"Output file path (not needed with --info or --list-layers)."`
	Scale       string   `optional:"" help:"Scale ratio (e.g. 1:100, 1:50, 1:1). Required for model space output; layouts are plotted 1:1 on their own sheet."`
	Layout      string   `optional:"" help:"Paper space layout to plot (name, see --info), or 'model'. Default: the layout the drawing was saved on, if it has viewports, else model space."`
	Paper       string   `default:"A4" help:"Paper size: A0-A4 or WxH in mm (e.g. 400x300)."`
	Margin      float64  `default:"10" help:"Margin in mm (uniform on all sides)."`
	Align       string   `default:"center" enum:"center,bottom-left,top-left" help:"Drawing alignment on page."`
	Layers      []string `optional:"" help:"Include only these layers (comma-separated, case-insensitive, globs like 'Wall*' allowed). A layer also selects its ArchiCAD pen sublayers (X_Pen_No__N) and the full content of blocks inserted on it. The page is sized to the selection."`
	Tile        bool     `help:"Tile drawing across multiple pages if it exceeds paper size."`
	Dwg2Dxf     string   `optional:"" help:"Path to dwg2dxf binary (for DWG files). Default: auto-detect from PATH."`
	DebugBBox   bool     `help:"Draw red bounding box rectangle on the output for debugging."`
	Crop        string   `optional:"" help:"Crop to bounding box in drawing units: minX,minY,maxX,maxY"`
	AutoPaper   bool     `help:"Auto-size paper to fit drawing at the given scale."`
	FontDir     string   `optional:"" help:"Directory with TrueType fonts, searched before the system font directories. Text uses the drawing's font file if found, else metric-compatible Liberation fonts, else DejaVu (see --info)."`
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
				fmt.Printf("%-30s %s  color=#%02X%02X%02X%s\n",
					l.Name, layerCounts(l), l.Color.R, l.Color.G, l.Color.B, layerNotes(l))
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
				fmt.Printf("  %-30s %s%s\n", l.Name, layerCounts(l), layerNotes(l))
			}
		}

		if len(info.Layouts) > 0 {
			fmt.Printf("\nLayouts:\n")
			for _, l := range info.Layouts {
				active := ""
				if l.Active {
					active = "  (active, plotted by default)"
				}
				fmt.Printf("  %-30s %.0f x %.0f mm, %d viewport(s)%s\n", l.Name, l.Width, l.Height, len(l.Viewports), active)
				for _, v := range l.Viewports {
					var notes []string
					if v.Scale > 0 {
						notes = append(notes, fmt.Sprintf("%.4g paper units per model unit", v.Scale))
					}
					if v.TwistDeg != 0 {
						notes = append(notes, fmt.Sprintf("turned %.2f°", v.TwistDeg))
					}
					if v.Clipped {
						notes = append(notes, "clipped to an outline")
					}
					if v.FrozenLayers > 0 {
						notes = append(notes, fmt.Sprintf("%d layer(s) frozen", v.FrozenLayers))
					}
					fmt.Printf("    viewport: %s\n", strings.Join(notes, ", "))
				}
			}
		}

		if len(info.Xrefs) > 0 {
			fmt.Printf("\nExternal references:\n")
			for _, x := range info.Xrefs {
				state := "→ " + x.Path
				if x.Error != "" {
					state = "NOT SHOWN: " + x.Error
				}
				fmt.Printf("  %s (%s) %s\n", x.Name, x.Ref, state)
			}
		}

		if len(info.LineTypes) > 0 {
			fmt.Printf("\nLine types: %s\n", strings.Join(info.LineTypes, ", "))
		}

		if len(info.TextStyles) > 0 {
			fmt.Printf("\nText styles (font → drawn with):\n")
			for _, s := range info.TextStyles {
				path, comp := converter.FontSubstitute(s.Font, s.Family, s.Bold, s.Italic, cli.FontDir)
				sub := filepath.Base(path)
				if comp != 1 {
					sub += fmt.Sprintf(" at %.0f%% width", comp*100)
				}
				font := s.Font
				if s.Family != "" {
					font += " (" + s.Family
					if s.Bold {
						font += " bold"
					}
					if s.Italic {
						font += " italic"
					}
					font += ")"
				}
				fmt.Printf("  %-24s %-36s → %s\n", s.Name, font, sub)
			}
		}

		if len(info.Blocks) > 0 {
			fmt.Printf("\nBlocks:\n")
			for _, b := range info.Blocks {
				fmt.Printf("  %s\n", b)
			}
		}

		if len(info.Unsupported) > 0 {
			types := make([]string, 0, len(info.Unsupported))
			for t := range info.Unsupported {
				types = append(types, t)
			}
			sort.Strings(types)
			fmt.Printf("\nUnsupported entities (not drawn):\n")
			for _, t := range types {
				fmt.Printf("  %-20s %d\n", t, info.Unsupported[t])
			}
		}

		if len(info.Warnings) > 0 {
			fmt.Printf("\nParser warnings: %d\n", len(info.Warnings))
			for i, w := range info.Warnings {
				if i == 5 {
					fmt.Printf("  ... and %d more\n", len(info.Warnings)-5)
					break
				}
				fmt.Printf("  %s\n", w)
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
		Layout:      cli.Layout,
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

// layerCounts formats a layer's top-level and in-block entity counts.
func layerCounts(l converter.LayerInfo) string {
	s := fmt.Sprintf("%4d entities", l.EntityCount)
	if l.BlockEntityCount > 0 {
		s += fmt.Sprintf(" (+%d in blocks)", l.BlockEntityCount)
	}
	return s
}

func layerNotes(l converter.LayerInfo) string {
	s := ""
	if !l.Visible {
		s += " (frozen)"
	}
	if l.Undeclared {
		s += " (not in layer table)"
	}
	return s
}
