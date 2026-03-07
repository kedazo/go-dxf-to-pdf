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
| `--info` | | Print drawing metadata and exit |
| `--list-layers` | | List all layers and exit |

## DWG Support

DWG files are converted to DXF using `dwg2dxf` from [LibreDWG](https://github.com/LibreDWG/libredwg). The binary is auto-detected from `PATH`, or you can specify it with `--dwg-2-dxf`.

Download LibreDWG: https://github.com/LibreDWG/libredwg/releases/tag/0.13.3.7906

On Debian/Ubuntu:

```bash
sudo apt install libredwg-utils
```

## System Requirements

- **DejaVu Sans font** — required for Unicode text rendering
  - Path: `/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf`
  - Install: `sudo apt install fonts-dejavu-core`

## License

MIT
