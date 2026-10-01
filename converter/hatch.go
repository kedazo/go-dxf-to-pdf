package converter

import (
	"math"
	"sort"

	dxf "github.com/kedazo/dxf-go"
)

// enableHatch controls whether HATCH entities are rendered.
const enableHatch = true

// Guards against degenerate pattern data (e.g. a near-zero line spacing on a
// large boundary) producing millions of segments.
const (
	maxHatchLinesPerFamily = 20000
	maxDashesPerInterval   = 100000
)

// Boundary path flags (group 92).
const (
	hatchPathExternal  = 1
	hatchPathOutermost = 16
)

// hatchPolygons returns the hatch boundary loops that can enclose an area,
// in the hatch's object coordinate system. Curved edges and bulges are
// flattened with the given tolerance (drawing units; <= 0 = relative).
// With the "outermost" and "entire" island styles only the outer loops
// take part, so inner islands are filled over as AutoCAD does.
func hatchPolygons(h *dxf.Hatch, tolerance float64) [][][2]float64 {
	keep := func(pathType int) bool { return true }
	switch h.Style {
	case dxf.HatchStyleOutermost:
		keep = func(pathType int) bool { return pathType&(hatchPathExternal|hatchPathOutermost) != 0 }
	case dxf.HatchStyleEntire:
		keep = func(pathType int) bool { return pathType&hatchPathExternal != 0 }
	}
	// If no loop carries the flags the style relies on, fall back to all.
	anyKept := false
	for i := range h.Paths {
		anyKept = anyKept || keep(h.Paths[i].PathType)
	}

	polys := make([][][2]float64, 0, len(h.Paths))
	for i := range h.Paths {
		if anyKept && !keep(h.Paths[i].PathType) {
			continue
		}
		if poly, _ := h.Paths[i].Polygon(tolerance); len(poly) >= 3 {
			polys = append(polys, poly)
		}
	}
	return polys
}

// hatchFillColor returns the color for a solid hatch. Gradient fills are
// approximated by their first color stop; otherwise the entity color is used.
func hatchFillColor(h *dxf.Hatch, entity RGB) RGB {
	if g := h.Gradient; g != nil && g.IsGradient && len(g.Colors) > 0 {
		c := g.Colors[0]
		switch {
		case c.HasTrueColor: // 0 is black
			return RGB{uint8(c.TrueColor >> 16), uint8(c.TrueColor >> 8), uint8(c.TrueColor)}
		case c.Color > 0 && c.Color < 256:
			return ACIToRGB(int16(c.Color))
		}
	}
	return entity
}

// generateHatchFillLines generates all fill lines for a pattern hatch.
// Returns line segments in the hatch's object coordinates as [x1, y1, x2, y2].
// All boundary loops are clipped together with the even-odd rule, so islands
// and holes stay empty. dotLen is the length used for zero-length (dot)
// dashes and tolerance the curve flattening tolerance, in the same units.
func generateHatchFillLines(h *dxf.Hatch, dotLen, tolerance float64) [][4]float64 {
	if h.SolidFill || len(h.PatternLines) == 0 {
		return nil
	}
	polys := hatchPolygons(h, tolerance)
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
//
// An edge counts as a crossing only if its end points lie strictly on
// different sides of the line, with points on the line counted as below it.
// This consistent tie-break keeps the even-odd pairing right when the line
// touches a vertex or runs along an edge.
func lineIntervals(ox, oy, dirX, dirY float64, polys [][][2]float64) [][2]float64 {
	var params []float64
	for _, poly := range polys {
		n := len(poly)
		// signed distance of a vertex from the line (left = positive)
		side := func(v [2]float64) float64 { return dirX*(v[1]-oy) - dirY*(v[0]-ox) }
		for i := 0; i < n; i++ {
			a, b := poly[i], poly[(i+1)%n]
			sa, sb := side(a), side(b)
			if (sa > 0) == (sb > 0) {
				continue
			}
			// crossing point a + (b-a)·f, as a parameter along the line
			f := sa / (sa - sb)
			px := a[0] + (b[0]-a[0])*f
			py := a[1] + (b[1]-a[1])*f
			params = append(params, (px-ox)*dirX+(py-oy)*dirY)
		}
	}
	if len(params) < 2 {
		return nil
	}
	sort.Float64s(params)

	var out [][2]float64
	for i := 0; i+1 < len(params); i += 2 {
		if params[i+1]-params[i] <= 1e-10 {
			continue
		}
		// A line touching a vertex from inside splits at a single point;
		// join such pieces back together.
		if last := len(out) - 1; last >= 0 && params[i]-out[last][1] <= 1e-10 {
			out[last][1] = params[i+1]
			continue
		}
		out = append(out, [2]float64{params[i], params[i+1]})
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
