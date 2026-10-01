package converter

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	dxf "github.com/kedazo/dxf-go"
)

// orphanedFontSpec matches an orphaned font spec fragment at the START of MText content,
// left over from group code boundary splits. MText content is split at 255-byte boundaries,
// which can cut \fFontName|b0|i0|c238|p0; mid-way. The orphaned tail looks like:
// "al Narrow|b0|i0|c238|p0;" at the very beginning of the string.
var orphanedFontSpec = regexp.MustCompile(`^[^;|\\]*\|[bi]\d\|[bi]\d\|c\d+\|p\d+;`)

// MTextStyle represents the formatting state at a point in MText content.
type MTextStyle struct {
	FontName  string
	Bold      bool
	Italic    bool
	Height         float64 // 0 = use default; absolute height in drawing units
	HeightRelative float64 // 0 = not set; multiplier of default height (from \H0.66x;)
	WidthFactor float64 // 0 = use default (1.0)
	ColorR    int
	ColorG    int
	ColorB    int
	HasColor  bool // true once \C or \c set an explicit color (otherwise the entity color applies)
	Underline bool
	Overstrike bool
	Strikethrough bool
}

// MTextSegment is a piece of text with uniform style.
type MTextSegment struct {
	Text  string
	Style MTextStyle
	NewLine bool // true = start a new line before this segment
}

// ParseMText parses MText content into styled segments.
// Handles all standard MText formatting codes:
//   \f - font change          \H - text height        \W - width factor
//   \C - ACI color            \c - RGB color          \P - paragraph break
//   \L/\l - underline on/off  \O/\o - overstrike      \K/\k - strikethrough
//   \S - stacking/fractions   \A - alignment           \Q - oblique angle
//   \T - tracking             \p - paragraph format    \N - new column
//   {} - style grouping       %%c %%d %%p - special chars
func ParseMText(s string) []MTextSegment {
	// First clean up orphaned font specs from group code boundary splits
	s = orphanedFontSpec.ReplaceAllString(s, "")

	// Handle MText content truncated by group code 3/1 boundary splits:
	// Strip orphaned prefix (content before first unmatched '}')
	// and orphaned suffix (content after last unmatched '{')
	s = cleanMTextBoundary(s)

	var segments []MTextSegment
	var styleStack []MTextStyle
	style := MTextStyle{WidthFactor: 1.0}
	var buf strings.Builder

	flush := func(newLine bool) {
		t := buf.String()
		if t != "" || newLine {
			segments = append(segments, MTextSegment{Text: t, Style: style, NewLine: newLine})
			buf.Reset()
		}
	}

	i := 0
	for i < len(s) {
		// Style grouping with braces
		if s[i] == '{' {
			i++
			// Push current style onto stack
			styleStack = append(styleStack, style)
			// Skip font/formatting spec after opening brace up to ';'
			// e.g. {\fArial|b0|i0|c238|p0;text}
			if i < len(s) && s[i] == '\\' {
				// Let the backslash handler below process it
				continue
			}
			continue
		}
		if s[i] == '}' {
			i++
			flush(false)
			// Pop style
			if len(styleStack) > 0 {
				style = styleStack[len(styleStack)-1]
				styleStack = styleStack[:len(styleStack)-1]
			}
			continue
		}

		// Special chars: %%c (Ø diameter), %%d (° degree), %%p (± plus/minus)
		if s[i] == '%' && i+2 < len(s) && s[i+1] == '%' {
			switch s[i+2] {
			case 'c', 'C':
				buf.WriteRune('Ø')
				i += 3
				continue
			case 'd', 'D':
				buf.WriteRune('°')
				i += 3
				continue
			case 'p', 'P':
				buf.WriteRune('±')
				i += 3
				continue
			case '%':
				buf.WriteByte('%')
				i += 3
				continue
			}
		}

		// Backslash formatting codes
		if s[i] == '\\' && i+1 < len(s) {
			ch := s[i+1]
			switch ch {
			case 'P': // Paragraph break (new line)
				flush(false)
				flush(true)
				i += 2
				continue

			case 'N': // New column — treat as new line
				flush(false)
				flush(true)
				i += 2
				continue

			case 'p': // Paragraph formatting (\pxi,qi,...;) — skip params
				i += 2
				skipToSemicolon(s, &i)
				continue

			case 'f': // Font change: \fFontName|b#|i#|c#|p#;
				flush(false)
				i += 2
				fontSpec := readToSemicolon(s, &i)
				parseFontSpec(fontSpec, &style)
				continue

			case 'F': // Font file: \FFontFile; (older format)
				flush(false)
				i += 2
				skipToSemicolon(s, &i)
				continue

			case 'H': // Text height: \H1.5; (absolute) or \H0.66x; (relative)
				flush(false)
				i += 2
				val := readToSemicolon(s, &i)
				if strings.HasSuffix(val, "x") {
					val = strings.TrimSuffix(val, "x")
					if h, err := parseFloat(val); err == nil && h > 0 {
						style.HeightRelative = h
						style.Height = 0
					}
				} else {
					if h, err := parseFloat(val); err == nil && h > 0 {
						style.Height = h
						style.HeightRelative = 0
					}
				}
				continue

			case 'W': // Width factor: \W0.8; or \W0.8x;
				i += 2
				val := readToSemicolon(s, &i)
				val = strings.TrimSuffix(val, "x")
				if w, err := parseFloat(val); err == nil && w > 0 {
					style.WidthFactor = w
				}
				continue

			case 'C': // ACI color: \C1;
				flush(false)
				i += 2
				val := readToSemicolon(s, &i)
				// \C0; (ByBlock) and \C256; (ByLayer) mean "back to the entity color".
				if idx, err := parseInt(val); err == nil && idx > 0 && idx < 256 {
					rgb := ACIToRGB(int16(idx))
					style.ColorR = int(rgb.R)
					style.ColorG = int(rgb.G)
					style.ColorB = int(rgb.B)
					style.HasColor = true
				} else if err == nil {
					style.HasColor = false
				}
				continue

			case 'c': // RGB color: \c16711680; (24-bit integer)
				flush(false)
				i += 2
				val := readToSemicolon(s, &i)
				if c, err := parseInt(val); err == nil {
					style.ColorR = (c >> 16) & 0xFF
					style.ColorG = (c >> 8) & 0xFF
					style.ColorB = c & 0xFF
					style.HasColor = true
				}
				continue

			case 'S': // Stacking/fractions: \Snum^denom; or \Snum/denom; or \Snum#denom;
				i += 2
				val := readToSemicolon(s, &i)
				// ^ = superscript/subscript (num is super, denom is sub)
				// / = fraction with horizontal bar
				// # = fraction with diagonal bar
				if idx := strings.IndexByte(val, '^'); idx >= 0 {
					// Superscript/subscript: just show both parts without separator
					num := strings.TrimSpace(val[:idx])
					denom := strings.TrimSpace(val[idx+1:])
					buf.WriteString(num)
					if denom != "" {
						buf.WriteString(denom)
					}
				} else {
					for _, sep := range []byte{'/', '#'} {
						if idx := strings.IndexByte(val, sep); idx >= 0 {
							buf.WriteString(val[:idx])
							buf.WriteByte('/')
							buf.WriteString(val[idx+1:])
							val = ""
							break
						}
					}
					if val != "" {
						buf.WriteString(val)
					}
				}
				continue

			case 'A': // Alignment: \A0; \A1; \A2;
				i += 2
				skipToSemicolon(s, &i)
				// TODO: implement text alignment (bottom/center/top)
				continue

			case 'Q': // Oblique angle: \Q30;
				i += 2
				skipToSemicolon(s, &i)
				// TODO: implement text slant — fpdf has no direct support
				continue

			case 'T': // Character tracking/spacing: \T2;
				i += 2
				skipToSemicolon(s, &i)
				// TODO: implement character spacing — fpdf has no direct support
				continue

			case 'L': // Start underline
				flush(false)
				style.Underline = true
				i += 2
				continue
			case 'l': // Stop underline
				flush(false)
				style.Underline = false
				i += 2
				continue

			case 'O': // Start overstrike
				flush(false)
				style.Overstrike = true
				i += 2
				continue
			case 'o': // Stop overstrike
				flush(false)
				style.Overstrike = false
				i += 2
				continue

			case 'K': // Start strikethrough
				flush(false)
				style.Strikethrough = true
				i += 2
				continue
			case 'k': // Stop strikethrough
				flush(false)
				style.Strikethrough = false
				i += 2
				continue

			case 'X': // Paragraph wrap on dimension line — ignore
				i += 2
				continue

			case '~': // Non-breaking space
				buf.WriteByte(' ')
				i += 2
				continue

			case 'U': // Unicode escape: \U+00E1
				if r, n := parseUnicodeEscape(s[i:]); n > 0 {
					buf.WriteRune(r)
					i += n
					continue
				}

			case '\\': // Literal backslash
				buf.WriteByte('\\')
				i += 2
				continue

			case '{': // Literal opening brace
				buf.WriteByte('{')
				i += 2
				continue

			case '}': // Literal closing brace
				buf.WriteByte('}')
				i += 2
				continue
			}
		}

		// ^J = newline (alternate paragraph break)
		if s[i] == '^' && i+1 < len(s) && s[i+1] == 'J' {
			flush(false)
			flush(true)
			i += 2
			continue
		}

		// Regular character
		buf.WriteByte(s[i])
		i++
	}

	flush(false)
	return segments
}

// cleanMTextBoundary strips orphaned content from MText that was split
// at group code 3/1 boundaries (255-byte chunks). If the text starts with
// content from a previous chunk (no matching '{' for a leading '}'), strip
// everything up to and including that '}'. Similarly strip trailing content
// after a final unmatched '{'.
func cleanMTextBoundary(s string) string {
	// Strip orphaned prefix: if there's a '}' with no preceding '{',
	// this is leftover content from a previous group code chunk.
	// Strip everything up to and including that unmatched '}'.
	depth := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++ // skip escaped char
			continue
		}
		if s[i] == '{' {
			depth++
		} else if s[i] == '}' {
			if depth == 0 {
				// Unmatched '}' — strip everything up to and including it
				s = s[i+1:]
				break
			}
			depth--
		}
	}

	return s
}

// parseFontSpec parses "FontName|b0|i1|c238|p0" into style.
func parseFontSpec(spec string, style *MTextStyle) {
	parts := strings.Split(spec, "|")
	if len(parts) > 0 {
		style.FontName = parts[0]
	}
	for _, p := range parts[1:] {
		if len(p) >= 2 {
			switch p[0] {
			case 'b':
				style.Bold = p[1] == '1'
			case 'i':
				style.Italic = p[1] == '1'
			}
		}
	}
}

func readToSemicolon(s string, i *int) string {
	start := *i
	for *i < len(s) && s[*i] != ';' {
		*i++
	}
	val := s[start:*i]
	if *i < len(s) {
		*i++ // skip ';'
	}
	return val
}

func skipToSemicolon(s string, i *int) {
	for *i < len(s) && s[*i] != ';' {
		*i++
	}
	if *i < len(s) {
		*i++ // skip ';'
	}
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}

func parseInt(s string) (int, error) {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	return v, err
}

// drawCtx is the state inherited from enclosing INSERTs while walking
// entities: the block-local → world transform plus the INSERT's resolved
// style, which entities with ByBlock color/lineweight and entities on layer
// "0" take over.
type drawCtx struct {
	m     affine
	depth int
	layer string  // effective layer for block entities on layer "0" ("" = top level)
	color RGB     // ByBlock color
	lw    float64 // ByBlock line weight (mm)

	filter   *layerFilter // --layers selection (nil = all)
	selected bool         // an enclosing INSERT is on a selected layer
}

// maxBlockDepth guards against runaway (or cyclic) block nesting.
const maxBlockDepth = 10

var topCtx = drawCtx{m: identityAffine, lw: defaultLineWidthMM}

// filtered returns the top-level context for a layer selection.
func filtered(f *layerFilter) drawCtx {
	ctx := topCtx
	ctx.filter = f
	return ctx
}

// keeps reports whether ent passes the layer selection: it is on a selected
// layer itself, or it is part of a block inserted on one. INSERTs are always
// walked, so block content on selected (pen sub)layers shows up even when the
// INSERT sits on another layer.
func (ctx drawCtx) keeps(ent dxf.Entity) bool {
	return ctx.selected || ctx.filter.match(ctx.effectiveLayer(ent))
}

// effectiveLayer returns the layer an entity behaves as being on: entities
// on layer "0" inside a block inherit the INSERT's layer.
func (ctx drawCtx) effectiveLayer(ent dxf.Entity) string {
	if l := ent.Layer(); l != "0" || ctx.layer == "" {
		return l
	}
	return ctx.layer
}

// child returns the context for the entities of a block inserted by ins
// with instance transform local.
func (ctx drawCtx) child(ins dxf.Entity, local affine, layers map[string]dxf.Layer) drawCtx {
	c := ctx.inner(ins, local)
	c.color, c.lw = resolveStyle(ins, layers, ctx)
	return c
}

// inner is child without the style: transform, nesting and layer selection
// only (enough for extents).
func (ctx drawCtx) inner(ins dxf.Entity, local affine) drawCtx {
	c := ctx
	c.m = ctx.m.mul(local)
	c.depth = ctx.depth + 1
	c.layer = ctx.effectiveLayer(ins)
	c.selected = ctx.keeps(ins)
	return c
}

// resolveStyle resolves an entity's color and line weight: true color wins,
// then ACI; ByLayer uses the (effective) layer, ByBlock the enclosing INSERT.
func resolveStyle(entity dxf.Entity, layers map[string]dxf.Layer, ctx drawCtx) (RGB, float64) {
	layer, hasLayer := layers[ctx.effectiveLayer(entity)]

	var rgb RGB
	entColor := entity.Color()
	switch {
	case entity.HasColor24Bit():
		rgb = trueColorRGB(entity.Color24Bit())
	case entColor == dxf.ByLayer():
		if hasLayer && layer.HasColor24Bit {
			rgb = trueColorRGB(layer.Color24Bit)
		} else if hasLayer {
			// A negative layer color only means "layer off"; keep the hue.
			idx := int16(layer.Color)
			if idx < 0 {
				idx = -idx
			}
			rgb = ACIToRGB(idx)
		}
	case entColor == dxf.ByBlock():
		rgb = ctx.color
	default:
		rgb = ACIToRGB(int16(entColor))
	}

	return rgb, ResolveLineWeight(entity.LineWeight(), layer.LineWeight, hasLayer, ctx.lw)
}

// trueColorRGB splits a 0xRRGGBB true color.
func trueColorRGB(c int) RGB {
	return RGB{uint8(c >> 16), uint8(c >> 8), uint8(c)}
}

// ComputeBoundingBox computes the bounding box of all entities in world coordinates.
// Entities inside INSERT blocks are mapped through the accumulated insert
// transform so the bbox captures their actual placement.
func ComputeBoundingBox(entities []dxf.Entity, blocks map[string]*dxf.Block) BBox {
	return selectionBBox(entities, blocks, nil)
}

// selectionBBox is the bounding box of the entities passing the layer
// selection f (nil = all).
func selectionBBox(entities []dxf.Entity, blocks map[string]*dxf.Block, f *layerFilter) BBox {
	bb := NewBBox()
	ctx := filtered(f)
	for _, ent := range entities {
		expandBBoxForEntity(&bb, ent, blocks, ctx)
	}
	return bb
}

// entityBoxes returns the world bbox of every top-level entity (of its parts
// passing the layer selection f). Entities without a known extent get an
// empty bbox (see cullEntities).
func entityBoxes(entities []dxf.Entity, blocks map[string]*dxf.Block, f *layerFilter) []BBox {
	boxes := make([]BBox, len(entities))
	ctx := filtered(f)
	for i, ent := range entities {
		boxes[i] = NewBBox()
		expandBBoxForEntity(&boxes[i], ent, blocks, ctx)
	}
	return boxes
}

// cullEntities returns the entities whose bbox intersects box. Entities
// without a known extent are kept, so culling never drops geometry.
func cullEntities(entities []dxf.Entity, boxes []BBox, box BBox) []dxf.Entity {
	out := make([]dxf.Entity, 0, len(entities))
	for i, ent := range entities {
		b := boxes[i]
		if b.MinX > b.MaxX || (b.MaxX >= box.MinX && b.MinX <= box.MaxX && b.MaxY >= box.MinY && b.MinY <= box.MaxY) {
			out = append(out, ent)
		}
	}
	return out
}

// curve geometry shared by rendering and bbox computation

// circleArcAxes returns the world center and conjugate semi-axes of a circle
// (radius r around local cx, cy) under m.
func circleArcAxes(m affine, cx, cy, r float64) (wcx, wcy, ux, uy, vx, vy float64) {
	wcx, wcy = m.apply(cx, cy)
	ux, uy = m.applyVec(r, 0)
	vx, vy = m.applyVec(0, r)
	return
}

// arcParams returns the CCW parameter range of an ARC (angles in degrees).
func arcParams(startDeg, endDeg float64) (t0, t1 float64) {
	return ccwRange(startDeg*math.Pi/180, endDeg*math.Pi/180)
}

// ccwRange returns the CCW parameter range from t0 to t1 (radians), with
// t1 in (t0, t0+2π]: equal angles mean a full turn. Non-finite angles give
// an empty range.
func ccwRange(t0, t1 float64) (float64, float64) {
	if math.IsNaN(t0) || math.IsInf(t0, 0) || math.IsNaN(t1) || math.IsInf(t1, 0) {
		return 0, 0
	}
	sweep := math.Mod(t1-t0, 2*math.Pi)
	if sweep <= 0 {
		sweep += 2 * math.Pi
	}
	t0 = math.Mod(t0, 2*math.Pi)
	return t0, t0 + sweep
}

// ellipseAxes returns the local conjugate semi-axes and parameter range of an
// ELLIPSE. The minor axis is normal × major (scaled by the ratio), so ellipses
// with a negative normal run the other way round.
func ellipseAxes(e *dxf.Ellipse) (ux, uy, vx, vy, t0, t1 float64) {
	n := e.Normal
	if n.X == 0 && n.Y == 0 && n.Z == 0 {
		n.Z = 1
	}
	mj := e.MajorAxis
	ux, uy = mj.X, mj.Y
	vx = e.MinorAxisRatio * (n.Y*mj.Z - n.Z*mj.Y)
	vy = e.MinorAxisRatio * (n.Z*mj.X - n.X*mj.Z)

	t0, t1 = e.StartAngle, e.EndAngle
	if (t0 == 0 && t1 == 0) || math.Abs(t1-t0-2*math.Pi) < 1e-9 {
		return ux, uy, vx, vy, 0, 2 * math.Pi
	}
	t0, t1 = ccwRange(t0, t1)
	return ux, uy, vx, vy, t0, t1
}

// polyline2DAffine returns the OCS transform of a POLYLINE: 2D polylines
// live in their OCS, 3D polylines and meshes in world coordinates.
func polyline2DAffine(p *dxf.Polyline) affine {
	if p.Is3DPolyline() || p.Is3DPolygonMesh() || p.IsPolyfaceMesh() {
		return identityAffine
	}
	return ocsAffine(p.Normal, p.Location.Z)
}

func expandEllipticArc(bb *BBox, cx, cy, ux, uy, vx, vy, t0, t1 float64) {
	if t1-t0 >= 2*math.Pi-1e-9 {
		hw, hh := math.Hypot(ux, vx), math.Hypot(uy, vy)
		bb.Expand(cx-hw, cy-hh)
		bb.Expand(cx+hw, cy+hh)
		return
	}
	for _, p := range ellipticArcPoints(cx, cy, ux, uy, vx, vy, t0, t1, 32) {
		bb.Expand(p[0], p[1])
	}
}

// expandBBoxForEntity expands the bounding box with the given entity, mapped
// through ctx.m (block-local → world), if it passes the layer selection.
func expandBBoxForEntity(bb *BBox, ent dxf.Entity, blocks map[string]*dxf.Block, ctx drawCtx) {
	if ctx.depth > maxBlockDepth || !ent.IsVisible() {
		return
	}
	if _, isInsert := ent.(*dxf.Insert); !isInsert && !ctx.keeps(ent) {
		return
	}
	m := ctx.m

	expand := func(t affine, x, y float64) {
		bb.Expand(t.apply(x, y))
	}

	switch e := ent.(type) {
	case *dxf.Line:
		expand(m, e.P1.X, e.P1.Y)
		expand(m, e.P2.X, e.P2.Y)
	case *dxf.Circle:
		cx, cy, ux, uy, vx, vy := circleArcAxes(m.mul(ocsAffine(e.Normal, e.Center.Z)), e.Center.X, e.Center.Y, e.Radius)
		expandEllipticArc(bb, cx, cy, ux, uy, vx, vy, 0, 2*math.Pi)
	case *dxf.Arc:
		cx, cy, ux, uy, vx, vy := circleArcAxes(m.mul(ocsAffine(e.Normal, e.Center.Z)), e.Center.X, e.Center.Y, e.Radius)
		t0, t1 := arcParams(e.StartAngle, e.EndAngle)
		expandEllipticArc(bb, cx, cy, ux, uy, vx, vy, t0, t1)
	case *dxf.Ellipse:
		lux, luy, lvx, lvy, t0, t1 := ellipseAxes(e)
		cx, cy := m.apply(e.Center.X, e.Center.Y)
		ux, uy := m.applyVec(lux, luy)
		vx, vy := m.applyVec(lvx, lvy)
		expandEllipticArc(bb, cx, cy, ux, uy, vx, vy, t0, t1)
	case *dxf.LWPolyline:
		om := m.mul(ocsAffine(e.ExtrusionDirection, e.Elevation()))
		for _, v := range e.Vertices {
			expand(om, v.X, v.Y)
		}
	case *dxf.Polyline:
		// Use the edges, not the raw vertices: polyface face records sit at
		// (0,0,0) and would drag the bbox to the origin.
		om := m.mul(polyline2DAffine(e))
		for _, edge := range polylineEdges(e) {
			expand(om, edge.X1, edge.Y1)
			expand(om, edge.X2, edge.Y2)
		}
	case *dxf.Spline:
		for _, cp := range e.ControlPoints {
			expand(m, cp.Point.X, cp.Point.Y)
		}
	case *dxf.Text:
		expand(m.mul(ocsAffine(e.Normal, e.Location.Z)), e.Location.X, e.Location.Y)
	case *dxf.MText:
		expand(m, e.InsertionPoint.X, e.InsertionPoint.Y)
	case *dxf.ModelPoint:
		expand(m, e.Location.X, e.Location.Y)
	case *dxf.Solid:
		om := m.mul(ocsAffine(e.ExtrusionDirection, e.FirstCorner.Z))
		for _, p := range []dxf.Point{e.FirstCorner, e.SecondCorner, e.ThirdCorner, e.FourthCorner} {
			expand(om, p.X, p.Y)
		}
	case *dxf.Insert:
		if blk, ok := blocks[e.Name]; ok {
			for _, local := range insertInstances(e, blk.BasePoint) {
				cctx := ctx.inner(e, local)
				for _, be := range blk.Entities {
					expandBBoxForEntity(bb, be, blocks, cctx)
				}
			}
		}
		actx := ctx.inner(e, identityAffine)
		for i := range e.Attributes {
			expandBBoxForEntity(bb, &e.Attributes[i], blocks, actx)
		}
	case *dxf.Attribute:
		if !e.IsInvisible() {
			expand(m.mul(ocsAffine(e.Normal, e.Location.Z)), e.Location.X, e.Location.Y)
		}
	case *dxf.Hatch:
		om := m.mul(ocsAffine(e.ExtrusionDirection, e.Elevation()))
		for _, poly := range hatchPolygons(e, 0) {
			for _, v := range poly {
				expand(om, v[0], v[1])
			}
		}

	default:
		// DIMENSION entities reference an anonymous block containing their
		// geometry in world coordinates (relative to the dimension's space).
		if dim, ok := ent.(dxf.Dimension); ok {
			if blk, ok := blocks[dim.BlockName()]; ok {
				dctx := ctx.inner(ent, identityAffine)
				dctx.selected = true // the dimension itself passed the selection
				for _, be := range blk.Entities {
					expandBBoxForEntity(bb, be, blocks, dctx)
				}
			}
		}
	}
}

// RenderEntities renders all DXF entities to the PDF renderer.
func RenderEntities(r *Renderer, entities []dxf.Entity, layers map[string]dxf.Layer,
	blocks map[string]*dxf.Block, sel *layerFilter) {

	ctx := filtered(sel)
	for _, ent := range entities {
		renderEntity(r, ent, layers, blocks, ctx)
	}
}

func renderEntity(r *Renderer, ent dxf.Entity, layers map[string]dxf.Layer,
	blocks map[string]*dxf.Block, ctx drawCtx) {

	if ctx.depth > maxBlockDepth || !ent.IsVisible() {
		return
	}

	// Layer selection; INSERTs are always walked (see keeps).
	if _, isInsert := ent.(*dxf.Insert); !isInsert && !ctx.keeps(ent) {
		return
	}

	rgb, lw := resolveStyle(ent, layers, ctx)
	r.SetStyle(rgb, lw)
	m := ctx.m

	switch e := ent.(type) {
	case *dxf.Line:
		x1, y1 := m.apply(e.P1.X, e.P1.Y)
		x2, y2 := m.apply(e.P2.X, e.P2.Y)
		r.DrawLine(x1, y1, x2, y2)

	case *dxf.Circle:
		cx, cy, ux, uy, vx, vy := circleArcAxes(m.mul(ocsAffine(e.Normal, e.Center.Z)), e.Center.X, e.Center.Y, e.Radius)
		r.DrawEllipticArc(cx, cy, ux, uy, vx, vy, 0, 2*math.Pi)

	case *dxf.Arc:
		cx, cy, ux, uy, vx, vy := circleArcAxes(m.mul(ocsAffine(e.Normal, e.Center.Z)), e.Center.X, e.Center.Y, e.Radius)
		t0, t1 := arcParams(e.StartAngle, e.EndAngle)
		r.DrawEllipticArc(cx, cy, ux, uy, vx, vy, t0, t1)

	case *dxf.Ellipse:
		lux, luy, lvx, lvy, t0, t1 := ellipseAxes(e)
		cx, cy := m.apply(e.Center.X, e.Center.Y)
		ux, uy := m.applyVec(lux, luy)
		vx, vy := m.applyVec(lvx, lvy)
		r.DrawEllipticArc(cx, cy, ux, uy, vx, vy, t0, t1)

	case *dxf.LWPolyline:
		renderLWPolyline(r, e, m.mul(ocsAffine(e.ExtrusionDirection, e.Elevation())))

	case *dxf.Polyline:
		om := m.mul(polyline2DAffine(e))
		for _, edge := range polylineEdges(e) {
			drawEdge(r, om, edge.X1, edge.Y1, edge.X2, edge.Y2, edge.Bulge)
		}

	case *dxf.Spline:
		cps := make([][2]float64, len(e.ControlPoints))
		for i, cp := range e.ControlPoints {
			x, y := m.apply(cp.Point.X, cp.Point.Y)
			cps[i] = [2]float64{x, y}
		}
		r.DrawSpline(cps, e.DegreeOfCurve, e.KnotValues)

	case *dxf.Text:
		renderTextLine(r, m.mul(ocsAffine(e.Normal, e.Location.Z)), decodeTextValue(e.Value), e.Height,
			resolveTextAnchor(e.Location, e.SecondAlignmentPoint,
				e.HorizontalTextJustification, e.VerticalTextJustification, e.Rotation))

	case *dxf.Attribute:
		if e.IsInvisible() {
			return
		}
		if e.MTextFlag&dxf.MTextFlagMultilineAttribute != 0 && e.MText.Text != "" {
			renderMText(r, &e.MText, m)
			return
		}
		renderTextLine(r, m.mul(ocsAffine(e.Normal, e.Location.Z)), decodeTextValue(e.Value), e.TextHeight,
			resolveTextAnchor(e.Location, e.SecondAlignmentPoint,
				e.HorizontalTextJustification, e.VerticalTextJustification, e.Rotation))

	case *dxf.MText:
		renderMText(r, e, m)

	case *dxf.ModelPoint:
		r.DrawPoint(m.apply(e.Location.X, e.Location.Y))

	case *dxf.Solid:
		om := m.mul(ocsAffine(e.ExtrusionDirection, e.FirstCorner.Z))
		x1, y1 := om.apply(e.FirstCorner.X, e.FirstCorner.Y)
		x2, y2 := om.apply(e.SecondCorner.X, e.SecondCorner.Y)
		x3, y3 := om.apply(e.ThirdCorner.X, e.ThirdCorner.Y)
		x4, y4 := om.apply(e.FourthCorner.X, e.FourthCorner.Y)
		r.SetStyle(rgb, 0)
		r.SetFillColor(rgb)
		r.DrawSolid(x1, y1, x2, y2, x3, y3, x4, y4)

	case *dxf.Hatch:
		if !enableHatch {
			break
		}
		// Boundaries and pattern lines are in the hatch's OCS.
		om := m.mul(ocsAffine(e.ExtrusionDirection, e.Elevation()))
		// Paper-size based tolerances: curves flattened to within 0.05 mm,
		// dots drawn ~0.15 mm long, whatever the scale.
		pageScale := r.transform.Scale * om.linearScale()
		tolerance, dotLen := 0.0, 0.15
		if pageScale > 0 {
			tolerance, dotLen = 0.05/pageScale, 0.15/pageScale
		}
		if e.SolidFill {
			polys := hatchPolygons(e, tolerance)
			world := make([][][2]float64, len(polys))
			for i, poly := range polys {
				world[i] = make([][2]float64, len(poly))
				for j, v := range poly {
					x, y := om.apply(v[0], v[1])
					world[i][j] = [2]float64{x, y}
				}
			}
			r.FillPolygons(world, hatchFillColor(e, rgb))
		} else if len(e.PatternLines) > 0 {
			r.SetStyle(rgb, 0.05) // thin lines for hatch fill
			lines := generateHatchFillLines(e, dotLen, tolerance)
			for _, seg := range lines {
				x1, y1 := om.apply(seg[0], seg[1])
				x2, y2 := om.apply(seg[2], seg[3])
				r.DrawLine(x1, y1, x2, y2)
			}
		}

	case *dxf.Insert:
		if blk, ok := blocks[e.Name]; ok {
			for _, local := range insertInstances(e, blk.BasePoint) {
				cctx := ctx.child(e, local, layers)
				for _, be := range blk.Entities {
					renderEntity(r, be, layers, blocks, cctx)
				}
			}
		}
		// ATTRIBs are stored in the INSERT's own coordinate space (already
		// placed), so they take the parent transform, not the block's.
		actx := ctx.child(e, identityAffine, layers)
		for i := range e.Attributes {
			renderEntity(r, &e.Attributes[i], layers, blocks, actx)
		}

	default:
		// DIMENSION entities reference an anonymous block containing their geometry.
		// The block entities are in world coordinates (basePoint=0,0) and may be on
		// sublayers (e.g. "Layer_Pen_No__1"), so the whole block is selected —
		// the dimension entity itself already passed the selection above.
		if dim, ok := ent.(dxf.Dimension); ok {
			if blk, ok := blocks[dim.BlockName()]; ok {
				dctx := ctx.child(ent, identityAffine, layers)
				dctx.selected = true
				for _, be := range blk.Entities {
					renderEntity(r, be, layers, blocks, dctx)
				}
			}
		}
	}
}

// renderTextLine draws a single-line TEXT/ATTRIB whose anchor is given in the
// entity's own coordinates, mapped through m.
func renderTextLine(r *Renderer, m affine, value string, height float64, a textAnchor) {
	x, y, rot, hScale, mirrored := textFrame(m, a.X, a.Y, a.RotationDeg)
	hAlign := a.HAlign
	if mirrored {
		hAlign = 1 - hAlign
	}
	r.DrawText(x, y, value, height*hScale, rot, hAlign, a.VAlign)
}

func renderMText(r *Renderer, e *dxf.MText, m affine) {
	// Long MTEXT is split into 250-char group 3 chunks followed by the final
	// group 1 chunk, so the extended text comes first.
	var raw strings.Builder
	for _, ext := range e.ExtendedText {
		raw.WriteString(ext)
	}
	raw.WriteString(e.Text)
	segments := ParseMText(raw.String())

	x, y, rot, hScale, mirrored := textFrame(m, e.InsertionPoint.X, e.InsertionPoint.Y, mtextRotationDeg(e))
	attach := int(e.AttachmentPoint)
	if mirrored && attach >= 1 && attach <= 9 {
		row, col := (attach-1)/3, (attach-1)%3
		attach = row*3 + (2 - col) + 1
	}
	r.DrawMText(x, y, segments, e.InitialTextHeight*hScale, rot, attach, e.LineSpacingFactor)
}

// drawEdge draws a straight or bulged (arc) polyline edge given in local
// coordinates, mapped through m.
func drawEdge(r *Renderer, m affine, x1, y1, x2, y2, bulge float64) {
	if math.Abs(bulge) > 1e-10 && (x1 != x2 || y1 != y2) {
		lcx, lcy, radius, start, sweep := bulgeArc(x1, y1, x2, y2, bulge)
		cx, cy, ux, uy, vx, vy := circleArcAxes(m, lcx, lcy, radius)
		r.DrawEllipticArc(cx, cy, ux, uy, vx, vy, start, start+sweep)
		return
	}
	wx1, wy1 := m.apply(x1, y1)
	wx2, wy2 := m.apply(x2, y2)
	r.DrawLine(wx1, wy1, wx2, wy2)
}

func renderLWPolyline(r *Renderer, e *dxf.LWPolyline, m affine) {
	verts := e.Vertices
	n := len(verts)
	if n < 2 {
		return
	}
	count := n - 1
	if e.IsClosed() {
		count = n
	}
	for i := 0; i < count; i++ {
		a, b := verts[i], verts[(i+1)%n]
		drawEdge(r, m, a.X, a.Y, b.X, b.Y, a.Bulge)
	}
}


// stripMTextFormatting removes MText formatting codes and returns plain text.
// For styled rendering, use ParseMText instead.
func stripMTextFormatting(s string) string {
	segments := ParseMText(s)
	var result strings.Builder
	for _, seg := range segments {
		if seg.NewLine && result.Len() > 0 {
			result.WriteByte(' ')
		}
		result.WriteString(seg.Text)
	}
	return result.String()
}

