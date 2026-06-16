# go-dxf-to-pdf

A Go CLI tool and library to convert DXF/DWG files to properly scaled PDF output.

## Features

- Scale-accurate PDF output (e.g. 1:100, 1:50, 1:1)
- Standard paper sizes (A0–A4) or custom dimensions
- Auto-paper mode: sizes the page to fit the drawing
- Multi-page tiling for large drawings
- Layer filtering
- Cropping to a bounding box
- HATCH pattern rendering (wall cross-sections, insulation, etc.)
- MText formatting (bold, italic, underline, stacking, Unicode)
- Wall recognition: export detected walls + raw line segments to XML (`--emit-wall-segments`)
- DWG support via LibreDWG

## Installation

```bash
go install github.com/kedazo/go-dxf-to-pdf/cmd/dxf-to-pdf@latest
```

Or build from source:

```bash
git clone https://github.com/kedazo/go-dxf-to-pdf.git
cd go-dxf-to-pdf
go build ./cmd/dxf-to-pdf/
```

## Usage

```bash
# Convert DXF to PDF at 1:100 scale on A3 paper
dxf-to-pdf input.dxf output.pdf --scale 1:100 --paper A3

# Auto-size paper to fit the drawing
dxf-to-pdf input.dxf output.pdf --scale 1:50 --auto-paper

# Convert DWG file (requires dwg2dxf)
dxf-to-pdf input.dwg output.pdf --scale 1:100 --paper A4

# Include only specific layers
dxf-to-pdf input.dxf output.pdf --scale 1:1 --layers "Walls,Doors"

# Tile across multiple pages
dxf-to-pdf input.dxf output.pdf --scale 1:1 --paper A4 --tile

# Inspect drawing metadata
dxf-to-pdf input.dxf --info
dxf-to-pdf input.dxf --list-layers

# Recognize walls and export them (+ raw segments) to XML
dxf-to-pdf input.dxf --emit-wall-segments walls.xml --scale 1:100
```

## Options

| Flag | Default | Description |
|------|---------|-------------|
| `--scale` | *(required)* | Scale ratio, e.g. `1:100`, `1:50`, `1:1` |
| `--paper` | `A4` | Paper size: `A0`–`A4` or `WxH` in mm (e.g. `400x300`) |
| `--auto-paper` | | Calculate paper size from drawing dimensions |
| `--margin` | `10` | Margin in mm (uniform on all sides) |
| `--align` | `center` | Drawing alignment: `center`, `bottom-left`, `top-left` |
| `--layers` | *(all)* | Include only these layers (comma-separated) |
| `--tile` | | Tile drawing across multiple pages |
| `--crop` | | Crop to bounding box: `minX,minY,maxX,maxY` |
| `--dwg-2-dxf` | *(auto)* | Path to `dwg2dxf` binary |
| `--font-dir` | *(see below)* | Directory containing DejaVuSans*.ttf font files |
| `--info` | | Print drawing metadata and exit |
| `--list-layers` | | List all layers and exit |
| `--emit-wall-segments` | | Recognize walls, write XML, and exit (see below) |

## Wall recognition (`--emit-wall-segments`)

An emit-and-exit mode (like `--info`) that parses the drawing, explodes its geometry (including
nested INSERT blocks), and writes an XML file containing:

- `<segments>` — every raw exploded line, tagged by layer (ground truth for debugging);
- `<walls>` — detected walls as a **centerline + thickness**, found by pairing parallel faces.

Coordinates and lengths are in **meters**, Y-down, with the origin at the drawing's bounding-box
top-left — i.e. they overlay 1:1 a PNG rendered with `--auto-paper --margin 0` (a `<meta>` block
records the exact mapping and `pxPerMeter`). The run prints a short summary (scene size, units,
segment/wall counts, thickness clusters).

```bash
# Defaults work for most files
dxf-to-pdf plan.dwg --emit-wall-segments walls.xml --scale 1:100

# Override units for unitless exports (e.g. SketchUp)
dxf-to-pdf plan.dwg --emit-wall-segments walls.xml --source-unit mm
dxf-to-pdf plan.dwg --emit-wall-segments walls.xml --unit-scale 0.0571

# Only detect walls on specific layers (positive filter), or extend the blacklist
dxf-to-pdf plan.dwg --emit-wall-segments walls.xml --wall-layers wall,fal
dxf-to-pdf plan.dwg --emit-wall-segments walls.xml --exclude-layers grid,hatch
```

### Wall options

| Flag | Default | Description |
|------|---------|-------------|
| `--wall-min-thickness` | `0.03` | Min wall thickness in meters (pair lower bound) |
| `--wall-max-thickness` | `0.60` | Max wall thickness in meters (pair upper bound) |
| `--wall-min-length` | `0.30` | Min candidate face length in meters |
| `--wall-angle-tol` | `5` | Parallel-pair angle tolerance in degrees |
| `--wall-merge-gap` | `0.05` | Collinear midline merge tolerance in meters |
| `--wall-bridge-gap` | `1.20` | Max door/window gap to bridge when merging |
| `--wall-include-curves` | | Sample ARC/CIRCLE into raw segments (never paired) |
| `--source-unit` | *(from file)* | Override unit: `mm`/`cm`/`m`/`in`/`ft` |
| `--unit-scale` | | Override DXF-unit→meters factor (wins over `--source-unit`) |
| `--no-default-blacklist` | | Disable the built-in junk-layer blacklist |
| `--exclude-layers` | | Extra layer-name substrings to exclude from detection |
| `--wall-layers` | | Only detect walls on layers matching these substrings |

**Layer filtering.** By default a built-in blacklist keeps furniture, MEP, annotation, openings,
room zones and finishes out of `<walls>` (raw `<segments>` always keeps every layer). Layers whose
name contains `wall`/`fal`/structural terms are always kept. See [`blacklisting.md`](blacklisting.md)
for the full pattern lists and cross-CAD-app analysis.

**Limitations.** Single-line (no double-face) walls aren't detected; unitless files need
`--source-unit`/`--unit-scale`; flattened 3D mesh exports (e.g. some SketchUp DWGs) have no clean
faces to pair; very new DWG (AC1032/2018+) may not decode via LibreDWG.

## DWG Support

DWG files are converted to DXF using `dwg2dxf` from [LibreDWG](https://github.com/LibreDWG/libredwg). The binary is auto-detected from `PATH`, or you can specify it with `--dwg-2-dxf`.

Download LibreDWG: https://github.com/LibreDWG/libredwg/releases/tag/0.13.3.7906

On Debian/Ubuntu:

```bash
sudo apt install libredwg-utils
```

## Fonts

The tool requires DejaVu Sans TTF font files for text rendering. Place these files in the font directory:

- `DejaVuSans.ttf`
- `DejaVuSans-Bold.ttf`
- `DejaVuSans-Oblique.ttf`
- `DejaVuSans-BoldOblique.ttf`

**Default font directory:**
- **Linux:** `/usr/share/fonts/truetype/dejavu` (install: `sudo apt install fonts-dejavu-core`)
- **Windows:** the directory containing the `dxf-to-pdf.exe` binary

Override with `--font-dir /path/to/fonts`.

Download fonts: https://dejavu-fonts.github.io/Download.html

## License

MIT
