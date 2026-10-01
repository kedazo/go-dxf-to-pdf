package converter

import (
	"math"

	dxf "github.com/ixmilia/dxf-go"
)

// affine is a 2D affine transform:
//
//	x' = a*x + c*y + e
//	y' = b*x + d*y + f
//
// It maps block-local (or OCS) coordinates to world coordinates. Composing
// INSERT transforms as matrices keeps nested blocks correct for any mix of
// mirroring, non-uniform scaling and rotation.
type affine struct {
	a, b, c, d, e, f float64
}

var identityAffine = affine{a: 1, d: 1}

func translateAffine(x, y float64) affine { return affine{a: 1, d: 1, e: x, f: y} }
func scaleAffine(sx, sy float64) affine   { return affine{a: sx, d: sy} }

func rotateAffine(deg float64) affine {
	if deg == 0 {
		return identityAffine
	}
	s, c := math.Sincos(deg * math.Pi / 180)
	return affine{a: c, b: s, c: -s, d: c}
}

func (m affine) apply(x, y float64) (float64, float64) {
	return m.a*x + m.c*y + m.e, m.b*x + m.d*y + m.f
}

// applyVec transforms a direction/extent vector (no translation).
func (m affine) applyVec(x, y float64) (float64, float64) {
	return m.a*x + m.c*y, m.b*x + m.d*y
}

// mul returns m∘n: the transform that applies n first, then m.
func (m affine) mul(n affine) affine {
	return affine{
		a: m.a*n.a + m.c*n.b,
		b: m.b*n.a + m.d*n.b,
		c: m.a*n.c + m.c*n.d,
		d: m.b*n.c + m.d*n.d,
		e: m.a*n.e + m.c*n.f + m.e,
		f: m.b*n.e + m.d*n.f + m.f,
	}
}

// ocsAffine returns the XY projection of the Object Coordinate System for the
// given extrusion direction (DXF "arbitrary axis algorithm"). Entities such as
// ARC, CIRCLE, LWPOLYLINE, TEXT and INSERT store coordinates in their OCS;
// the common non-trivial case is extrusion (0,0,-1), produced by mirroring,
// which maps (x, y) to (-x, y).
//
// z is the entity's OCS elevation (its points' common Z). It only matters for
// tilted normals, where z·N moves the entity in plan (e.g. wall-mounted 3D
// objects whose OCS plane is vertical).
func ocsAffine(n dxf.Vector, z float64) affine {
	l := math.Sqrt(n.X*n.X + n.Y*n.Y + n.Z*n.Z)
	if l < 1e-12 {
		return identityAffine
	}
	nx, ny, nz := n.X/l, n.Y/l, n.Z/l
	if math.Abs(nx) < 1e-12 && math.Abs(ny) < 1e-12 && nz > 0 {
		return identityAffine
	}

	// Ax = (|Nx| < 1/64 && |Ny| < 1/64 ? WorldY : WorldZ) × N, normalized.
	var ax, ay, az float64
	if math.Abs(nx) < 1.0/64 && math.Abs(ny) < 1.0/64 {
		ax, ay, az = nz, 0, -nx // (0,1,0) × N
	} else {
		ax, ay, az = -ny, nx, 0 // (0,0,1) × N
	}
	al := math.Sqrt(ax*ax + ay*ay + az*az)
	ax, ay, az = ax/al, ay/al, az/al
	// Ay = N × Ax
	bx := ny*az - nz*ay
	by := nz*ax - nx*az

	return affine{a: ax, b: ay, c: bx, d: by, e: z * nx, f: z * ny}
}

// insertAffine returns the block-local → parent transform of one INSERT
// instance (col, row select the cell of a MINSERT array):
//
//	OCS · T(location) · R(rotation) · T(col·colSpacing, row·rowSpacing) · S(xScale, yScale) · T(-basePoint)
func insertAffine(ins *dxf.Insert, base dxf.Point, col, row int) affine {
	m := ocsAffine(ins.ExtrusionDirection, ins.Location.Z).
		mul(translateAffine(ins.Location.X, ins.Location.Y)).
		mul(rotateAffine(ins.Rotation))
	if col != 0 || row != 0 {
		m = m.mul(translateAffine(float64(col)*ins.ColumnSpacing, float64(row)*ins.RowSpacing))
	}
	return m.mul(scaleAffine(ins.XScaleFactor, ins.YScaleFactor)).
		mul(translateAffine(-base.X, -base.Y))
}

// maxInsertInstances caps MINSERT arrays so a malformed count can't explode.
const maxInsertInstances = 10000

// insertInstances returns the transform of every instance of an INSERT
// (a single one for plain inserts, cols×rows for MINSERT arrays).
func insertInstances(ins *dxf.Insert, base dxf.Point) []affine {
	cols, rows := max(int(ins.ColumnCount), 1), max(int(ins.RowCount), 1)
	if cols*rows > maxInsertInstances {
		cols, rows = 1, 1
	}
	out := make([]affine, 0, cols*rows)
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			out = append(out, insertAffine(ins, base, col, row))
		}
	}
	return out
}

// textFrame maps a text anchor and rotation (entity-local) through m. It
// returns the world anchor, world rotation, the height scale along the text's
// up direction, and whether the frame is mirrored. For mirrored frames the
// rotation keeps the glyphs readable (upright relative to the up vector) and
// the caller flips the horizontal alignment so the text covers the same area.
func textFrame(m affine, x, y, rotDeg float64) (wx, wy, wrotDeg, hScale float64, mirrored bool) {
	wx, wy = m.apply(x, y)
	s, c := math.Sincos(rotDeg * math.Pi / 180)
	ux, uy := m.applyVec(c, s)  // baseline direction
	vx, vy := m.applyVec(-s, c) // up direction
	hScale = math.Hypot(vx, vy)
	if ux*vy-uy*vx < 0 {
		return wx, wy, math.Atan2(vy, vx)*180/math.Pi - 90, hScale, true
	}
	return wx, wy, math.Atan2(uy, ux) * 180 / math.Pi, hScale, false
}

// bulgeArc converts a bulge segment (p1→p2, bulge = tan(sweep/4)) into a
// circle center, radius, start angle and signed sweep (radians, CCW > 0).
func bulgeArc(x1, y1, x2, y2, bulge float64) (cx, cy, radius, start, sweep float64) {
	dx, dy := x2-x1, y2-y1
	chord := math.Hypot(dx, dy)
	sagitta := bulge * chord / 2
	r := (chord*chord/4 + sagitta*sagitta) / (2 * sagitta) // signed like bulge
	d := r - sagitta                                       // center offset along the left normal
	cx = (x1+x2)/2 - dy/chord*d
	cy = (y1+y2)/2 + dx/chord*d
	return cx, cy, math.Abs(r), math.Atan2(y1-cy, x1-cx), 4 * math.Atan(bulge)
}

// ellipticArcPoints samples c + u·cos(t) + v·sin(t) for t in [t0, t1].
func ellipticArcPoints(cx, cy, ux, uy, vx, vy, t0, t1 float64, n int) [][2]float64 {
	pts := make([][2]float64, 0, n+1)
	for i := 0; i <= n; i++ {
		t := t0 + (t1-t0)*float64(i)/float64(n)
		s, c := math.Sincos(t)
		pts = append(pts, [2]float64{cx + ux*c + vx*s, cy + uy*c + vy*s})
	}
	return pts
}
