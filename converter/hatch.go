package converter

import (
	"math"
	"sort"

	dxf "github.com/ixmilia/dxf-go"
)

// enableHatch controls whether HATCH entities are rendered.
const enableHatch = true

// Guards against degenerate pattern data (e.g. a near-zero line spacing on a
// large boundary) producing millions of segments.
const (
	maxHatchLinesPerFamily = 20000
	maxDashesPerInterval   = 100000
)

// hatchPolygons returns the hatch boundary loops that can enclose an area.
func hatchPolygons(h *dxf.Hatch) [][][2]float64 {
	polys := make([][][2]float64, 0, len(h.Paths))
	for _, p := range h.Paths {
		if len(p.Vertices) >= 3 {
			polys = append(polys, p.Vertices)
		}
	}
	return polys
}

// generateHatchFillLines generates all fill lines for a pattern hatch.
// Returns line segments in the hatch's coordinates as [x1, y1, x2, y2].
// All boundary loops are clipped together with the even-odd rule, so islands
// and holes stay empty. dotLen is the length used for zero-length (dot)
// dashes, in the same units.
func generateHatchFillLines(h *dxf.Hatch, dotLen float64) [][4]float64 {
	if h.SolidFill || len(h.PatternLines) == 0 {
		return nil
	}
	polys := hatchPolygons(h)
	if len(polys) == 0 {
		return nil
	}

	var allLines [][4]float64
	for _, pl := range h.PatternLines {
		allLines = append(allLines, patternLineSegments(polys, pl, dotLen)...)
	}
	return allLines
}

// patternLineSegments generates one pattern line family clipped to the
// boundary loops.
//
// The pattern data stored in a HATCH entity is already scaled and rotated
// (angle, base point and offset are in drawing coordinates), so no further
// pattern scale is applied. Line k of the family passes through
// base + k·offset; the offset's component along the line staggers successive
// lines (brick-like patterns), and dashes are phased from that origin.
func patternLineSegments(polys [][][2]float64, pl dxf.HatchPatternLine, dotLen float64) [][4]float64 {
	angleRad := pl.Angle * math.Pi / 180.0
	dirY, dirX := math.Sincos(angleRad)
	perpX, perpY := -dirY, dirX

	spacing := pl.OffsetX*perpX + pl.OffsetY*perpY
	if math.Abs(spacing) < 1e-10 {
		return nil
	}

	// Range of line indices covering the boundary.
	minProj, maxProj := math.Inf(1), math.Inf(-1)
	for _, poly := range polys {
		for _, v := range poly {
			proj := v[0]*perpX + v[1]*perpY
			minProj = math.Min(minProj, proj)
			maxProj = math.Max(maxProj, proj)
		}
	}
	baseProj := pl.BaseX*perpX + pl.BaseY*perpY
	k0 := (minProj - baseProj) / spacing
	k1 := (maxProj - baseProj) / spacing
	if k0 > k1 {
		k0, k1 = k1, k0
	}
	startIdx, endIdx := int(math.Floor(k0)), int(math.Ceil(k1))
	if endIdx-startIdx > maxHatchLinesPerFamily {
		return nil
	}

	var result [][4]float64
	for k := startIdx; k <= endIdx; k++ {
		ox := pl.BaseX + float64(k)*pl.OffsetX
		oy := pl.BaseY + float64(k)*pl.OffsetY
		emit := func(t0, t1 float64) {
			result = append(result, [4]float64{
				ox + t0*dirX, oy + t0*dirY,
				ox + t1*dirX, oy + t1*dirY,
			})
		}
		for _, iv := range lineIntervals(ox, oy, dirX, dirY, polys) {
			dashInterval(iv[0], iv[1], pl.Dashes, dotLen, emit)
		}
	}
	return result
}

// lineIntervals intersects the infinite line origin + t·dir with all boundary
// loops and returns the parameter intervals inside them (even-odd rule).
func lineIntervals(ox, oy, dirX, dirY float64, polys [][][2]float64) [][2]float64 {
	var params []float64
	for _, poly := range polys {
		n := len(poly)
		for i := 0; i < n; i++ {
			j := (i + 1) % n
			ex := poly[j][0] - poly[i][0]
			ey := poly[j][1] - poly[i][1]

			// Solve: o + t·dir = poly[i] + s·e
			denom := dirX*ey - dirY*ex
			if math.Abs(denom) < 1e-12 {
				continue // parallel
			}
			dx := poly[i][0] - ox
			dy := poly[i][1] - oy
			t := (dx*ey - dy*ex) / denom
			s := (dx*dirY - dy*dirX) / denom

			// Half-open edge test so a line through a shared vertex is counted once.
			if s >= 0 && s < 1 {
				params = append(params, t)
			}
		}
	}
	if len(params) < 2 {
		return nil
	}
	sort.Float64s(params)

	var out [][2]float64
	for i := 0; i+1 < len(params); i += 2 {
		if params[i+1]-params[i] > 1e-10 {
			out = append(out, [2]float64{params[i], params[i+1]})
		}
	}
	return out
}

// dashInterval emits the drawn parts of [t0, t1] for a dash pattern phased
// from t = 0. Positive values are dashes, negative gaps, zero a dot (drawn
// with length dotLen, taking no space in the pattern). An empty pattern is a
// continuous line.
func dashInterval(t0, t1 float64, dashes []float64, dotLen float64, emit func(a, b float64)) {
	period := 0.0
	for _, d := range dashes {
		period += math.Abs(d)
	}
	if len(dashes) == 0 || period < 1e-12 || (t1-t0)/period*float64(len(dashes)) > maxDashesPerInterval {
		emit(t0, t1)
		return
	}

	p := math.Floor(t0/period) * period
	for p < t1 {
		for _, d := range dashes {
			if p >= t1 {
				break
			}
			switch {
			case d > 0:
				if a, b := math.Max(p, t0), math.Min(p+d, t1); b > a {
					emit(a, b)
				}
			case d == 0:
				if p >= t0 {
					emit(p, math.Min(p+dotLen, t1))
				}
			}
			p += math.Abs(d)
		}
	}
}
