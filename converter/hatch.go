package converter

import (
	"math"
	"sort"

	dxf "github.com/ixmilia/dxf-go"
)

// enableHatch controls whether HATCH entities are rendered.
const enableHatch = true

// generateHatchFillLines generates all fill lines for a hatch entity.
// Returns line segments in DXF world coordinates as [x1, y1, x2, y2].
func generateHatchFillLines(h *dxf.Hatch) [][4]float64 {
	if h.SolidFill || len(h.Paths) == 0 || len(h.PatternLines) == 0 {
		return nil
	}

	var allLines [][4]float64
	for _, path := range h.Paths {
		if len(path.Vertices) < 3 {
			continue
		}
		for _, pl := range h.PatternLines {
			lines := generatePatternLines(path.Vertices, pl, h.PatternScale)
			allLines = append(allLines, lines...)
		}
	}
	return allLines
}

// generatePatternLines generates fill lines for one pattern line definition
// clipped to a polygon boundary.
func generatePatternLines(poly [][2]float64, pl dxf.HatchPatternLine, patScale float64) [][4]float64 {
	if patScale <= 0 {
		patScale = 1.0
	}

	// The offset vector defines the shift between consecutive pattern lines.
	// The perpendicular distance (spacing) is the magnitude of the offset projected
	// perpendicular to the line direction.
	angleRad := pl.Angle * math.Pi / 180.0
	dirX := math.Cos(angleRad)
	dirY := math.Sin(angleRad)

	// Perpendicular direction (left normal)
	perpX := -dirY
	perpY := dirX

	// Spacing = dot product of offset vector with perpendicular direction
	spacing := math.Abs(pl.OffsetX*perpX + pl.OffsetY*perpY) * patScale
	if spacing < 1e-10 {
		return nil
	}

	// Project all polygon vertices onto the perpendicular axis to find range
	minProj := math.Inf(1)
	maxProj := math.Inf(-1)
	for _, v := range poly {
		proj := v[0]*perpX + v[1]*perpY
		if proj < minProj {
			minProj = proj
		}
		if proj > maxProj {
			maxProj = proj
		}
	}

	// Base point projection (anchor for the pattern grid)
	baseProj := (pl.BaseX*patScale)*perpX + (pl.BaseY*patScale)*perpY

	// Find the range of line indices
	startIdx := int(math.Floor((minProj - baseProj) / spacing))
	endIdx := int(math.Ceil((maxProj - baseProj) / spacing))

	// Compute bounding box extent along line direction for line length
	minDir := math.Inf(1)
	maxDir := math.Inf(-1)
	for _, v := range poly {
		d := v[0]*dirX + v[1]*dirY
		if d < minDir {
			minDir = d
		}
		if d > maxDir {
			maxDir = d
		}
	}
	extent := maxDir - minDir

	var result [][4]float64

	// Dash pattern (scaled)
	var dashes []float64
	hasDash := len(pl.Dashes) > 0
	if hasDash {
		dashes = make([]float64, len(pl.Dashes))
		for i, d := range pl.Dashes {
			dashes[i] = d * patScale
		}
	}

	for i := startIdx; i <= endIdx; i++ {
		// Line at perpendicular distance = baseProj + i*spacing
		perpDist := baseProj + float64(i)*spacing

		// A point on this line
		px := perpDist * perpX
		py := perpDist * perpY

		// Line extends along (dirX, dirY) — clip to polygon
		clipped := clipLineToPolygon(px, py, dirX, dirY, extent*2, poly)
		if hasDash {
			for _, seg := range clipped {
				dashed := applyDashPattern(seg, dirX, dirY, dashes)
				result = append(result, dashed...)
			}
		} else {
			result = append(result, clipped...)
		}
	}

	return result
}

// clipLineToPolygon clips an infinite line (defined by a point and direction)
// to a polygon using intersection with all edges. Returns inside segments.
func clipLineToPolygon(px, py, dirX, dirY, extent float64, poly [][2]float64) [][4]float64 {
	// Parameterize the line as P(t) = (px + t*dirX, py + t*dirY)
	// Find all intersections with polygon edges
	var params []float64
	n := len(poly)

	for i := 0; i < n; i++ {
		j := (i + 1) % n
		ex := poly[j][0] - poly[i][0]
		ey := poly[j][1] - poly[i][1]

		// Solve: px + t*dirX = poly[i][0] + s*ex
		//        py + t*dirY = poly[i][1] + s*ey
		denom := dirX*ey - dirY*ex
		if math.Abs(denom) < 1e-12 {
			continue // parallel
		}
		dx := poly[i][0] - px
		dy := poly[i][1] - py
		t := (dx*ey - dy*ex) / denom
		s := (dx*dirY - dy*dirX) / denom

		if s >= -1e-10 && s <= 1.0+1e-10 {
			params = append(params, t)
		}
	}

	if len(params) < 2 {
		return nil
	}

	sort.Float64s(params)

	// Take pairs of intersections — odd-parity rule: inside between 1st-2nd, 3rd-4th, etc.
	var result [][4]float64
	for i := 0; i+1 < len(params); i += 2 {
		t1 := params[i]
		t2 := params[i+1]
		if t2-t1 < 1e-10 {
			continue
		}
		result = append(result, [4]float64{
			px + t1*dirX, py + t1*dirY,
			px + t2*dirX, py + t2*dirY,
		})
	}
	return result
}

// applyDashPattern subdivides a line segment according to a dash pattern.
// Positive dash values = drawn, negative = gap, zero = dot.
func applyDashPattern(seg [4]float64, dirX, dirY float64, dashes []float64) [][4]float64 {
	totalLen := math.Sqrt((seg[2]-seg[0])*(seg[2]-seg[0]) + (seg[3]-seg[1])*(seg[3]-seg[1]))
	if totalLen < 1e-10 {
		return nil
	}

	var result [][4]float64
	pos := 0.0
	dashIdx := 0

	for pos < totalLen {
		d := dashes[dashIdx%len(dashes)]
		absD := math.Abs(d)
		if absD < 1e-10 {
			absD = 0.1 // dot
		}
		end := pos + absD
		if end > totalLen {
			end = totalLen
		}

		if d >= 0 { // draw
			x1 := seg[0] + pos/totalLen*(seg[2]-seg[0])
			y1 := seg[1] + pos/totalLen*(seg[3]-seg[1])
			x2 := seg[0] + end/totalLen*(seg[2]-seg[0])
			y2 := seg[1] + end/totalLen*(seg[3]-seg[1])
			result = append(result, [4]float64{x1, y1, x2, y2})
		}
		// else: gap, skip

		pos = end
		dashIdx++
	}
	return result
}
