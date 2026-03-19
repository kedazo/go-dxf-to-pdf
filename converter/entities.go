package converter

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	dxf "github.com/ixmilia/dxf-go"
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
				if idx, err := parseInt(val); err == nil {
					rgb := ACIToRGB(int16(idx))
					style.ColorR = int(rgb.R)
					style.ColorG = int(rgb.G)
					style.ColorB = int(rgb.B)
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

// entityStyle resolves the color and line weight for an entity, considering layer inheritance.
func entityStyle(entity dxf.Entity, layers map[string]dxf.Layer) (RGB, float64) {
	layerName := entity.Layer()
	layer, hasLayer := layers[layerName]

	// Resolve color
	entColor := entity.Color()
	var rgb RGB
	colorVal := int16(entColor)
	switch {
	case entColor == dxf.ByLayer():
		if hasLayer {
			rgb = ACIToRGB(int16(layer.Color))
		} else {
			rgb = RGB{0, 0, 0}
		}
	case entColor == dxf.ByBlock():
		rgb = RGB{0, 0, 0}
	default:
		rgb = ACIToRGB(colorVal)
	}

	// Resolve line weight
	var lw float64
	if hasLayer {
		lw = ResolveLineWeight(entity.LineWeight(), layer.LineWeight)
	} else {
		lw = LineWeightToMM(entity.LineWeight())
	}

	return rgb, lw
}

// ComputeBoundingBox computes the bounding box of all entities in world coordinates.
// For top-level entities, the insert transform is identity (offset=0, scale=1, rotation=0).
// For entities inside INSERT blocks, the accumulated insert transform converts
// block-local coordinates to world coordinates so the bbox captures the actual placement.
func ComputeBoundingBox(entities []dxf.Entity, blocks map[string]*dxf.Block) BBox {
	bb := NewBBox()
	for _, ent := range entities {
		expandBBoxForEntity(&bb, ent, blocks, 0, 0, 0, 0, 0, 1, 1, 0)
	}
	return bb
}

// expandBBoxForEntity expands the bounding box with the given entity's coordinates.
// The insert transform parameters represent the accumulated transformation:
//   - baseX, baseY: block base point to subtract before scaling
//   - insX, insY: translation offset (world-space insert position)
//   - scX, scY: scale factors (product of all nested insert scales)
//   - rotDeg: rotation in degrees (sum of all nested insert rotations)
// For top-level entities these are all zero/identity (0,0, 0,0, 1,1, 0).
func expandBBoxForEntity(bb *BBox, ent dxf.Entity, blocks map[string]*dxf.Block,
	depth int, baseX, baseY, insX, insY, scX, scY, rotDeg float64) {
	if depth > 10 || !ent.IsVisible() {
		return
	}

	// wx transforms a point from block-local to world coordinates.
	wx := func(x, y float64) (float64, float64) {
		return applyInsert(x, y, baseX, baseY, insX, insY, scX, scY, rotDeg)
	}

	switch e := ent.(type) {
	case *dxf.Line:
		x1, y1 := wx(e.P1.X, e.P1.Y)
		x2, y2 := wx(e.P2.X, e.P2.Y)
		bb.Expand(x1, y1)
		bb.Expand(x2, y2)
	case *dxf.Circle:
		cx, cy := wx(e.Center.X, e.Center.Y)
		r := e.Radius * math.Abs(scX)
		bb.Expand(cx-r, cy-r)
		bb.Expand(cx+r, cy+r)
	case *dxf.Arc:
		cx, cy := wx(e.Center.X, e.Center.Y)
		r := e.Radius * math.Abs(scX)
		bb.Expand(cx-r, cy-r)
		bb.Expand(cx+r, cy+r)
	case *dxf.Ellipse:
		cx, cy := wx(e.Center.X, e.Center.Y)
		majorLen := math.Sqrt(e.MajorAxis.X*e.MajorAxis.X+e.MajorAxis.Y*e.MajorAxis.Y) * math.Abs(scX)
		bb.Expand(cx-majorLen, cy-majorLen)
		bb.Expand(cx+majorLen, cy+majorLen)
	case *dxf.LWPolyline:
		for _, v := range e.Vertices {
			x, y := wx(v.X, v.Y)
			bb.Expand(x, y)
		}
	case *dxf.Polyline:
		for _, v := range e.Vertices {
			x, y := wx(v.Location.X, v.Location.Y)
			bb.Expand(x, y)
		}
	case *dxf.Spline:
		for _, cp := range e.ControlPoints {
			x, y := wx(cp.Point.X, cp.Point.Y)
			bb.Expand(x, y)
		}
	case *dxf.Text:
		x, y := wx(e.Location.X, e.Location.Y)
		bb.Expand(x, y)
	case *dxf.MText:
		x, y := wx(e.InsertionPoint.X, e.InsertionPoint.Y)
		bb.Expand(x, y)
	case *dxf.ModelPoint:
		x, y := wx(e.Location.X, e.Location.Y)
		bb.Expand(x, y)
	case *dxf.Solid:
		x1, y1 := wx(e.FirstCorner.X, e.FirstCorner.Y)
		x2, y2 := wx(e.SecondCorner.X, e.SecondCorner.Y)
		x3, y3 := wx(e.ThirdCorner.X, e.ThirdCorner.Y)
		x4, y4 := wx(e.FourthCorner.X, e.FourthCorner.Y)
		bb.Expand(x1, y1)
		bb.Expand(x2, y2)
		bb.Expand(x3, y3)
		bb.Expand(x4, y4)
	case *dxf.Insert:
		// INSERT places a block at a given position with scale and rotation.
		// The transform for block entities is:
		//   world = (entity - blockBasePoint) * insertScale * insertRotation + insertPosition
		// We first transform the INSERT's own position to world coords (applying parent transform),
		// then use the block's base point as the new baseX/baseY for child entities.
		if blk, ok := blocks[e.Name]; ok {
			// Transform INSERT position to world coordinates
			wx, wy := applyInsert(e.Location.X, e.Location.Y, baseX, baseY, insX, insY, scX, scY, rotDeg)
			newScaleX := scX * e.XScaleFactor
			newScaleY := scY * e.YScaleFactor
			newRot := rotDeg + e.Rotation
			for _, be := range blk.Entities {
				expandBBoxForEntity(bb, be, blocks, depth+1,
					blk.BasePoint.X, blk.BasePoint.Y,
					wx, wy, newScaleX, newScaleY, newRot)
			}
		}

	default:
		// DIMENSION entities reference an anonymous block containing their geometry.
		if dim, ok := ent.(dxf.Dimension); ok {
			if blk, ok := blocks[dim.BlockName()]; ok {
				for _, be := range blk.Entities {
					expandBBoxForEntity(bb, be, blocks, depth+1,
						0, 0, 0, 0, 1, 1, 0)
				}
			}
		}
	}
}

// RenderEntities renders all DXF entities to the PDF renderer.
func RenderEntities(r *Renderer, entities []dxf.Entity, layers map[string]dxf.Layer,
	blocks map[string]*dxf.Block, layerFilter map[string]bool) {

	for _, ent := range entities {
		renderEntity(r, ent, layers, blocks, layerFilter, 0, 0, 0, 0, 0, 1, 1, 0)
	}
}

func renderEntity(r *Renderer, ent dxf.Entity, layers map[string]dxf.Layer,
	blocks map[string]*dxf.Block, layerFilter map[string]bool,
	depth int, baseX, baseY, insX, insY, insScaleX, insScaleY, insRotDeg float64) {

	if depth > 10 || !ent.IsVisible() {
		return
	}

	// Layer filter
	if layerFilter != nil {
		if _, ok := layerFilter[ent.Layer()]; !ok {
			return
		}
	}

	rgb, lw := entityStyle(ent, layers)
	r.SetStyle(rgb, lw)

	ai := func(x, y float64) (float64, float64) {
		return applyInsert(x, y, baseX, baseY, insX, insY, insScaleX, insScaleY, insRotDeg)
	}

	switch e := ent.(type) {
	case *dxf.Line:
		x1, y1 := ai(e.P1.X, e.P1.Y)
		x2, y2 := ai(e.P2.X, e.P2.Y)
		r.DrawLine(x1, y1, x2, y2)

	case *dxf.Circle:
		cx, cy := ai(e.Center.X, e.Center.Y)
		r.DrawCircle(cx, cy, e.Radius*math.Abs(insScaleX))

	case *dxf.Arc:
		cx, cy := ai(e.Center.X, e.Center.Y)
		// Transform arc start/end points through the INSERT transform, then
		// recompute angles via atan2. This correctly handles any combination
		// of mirroring, rotation, and scaling.
		rad := e.Radius
		sx := e.Center.X + rad*math.Cos(e.StartAngle*math.Pi/180)
		sy := e.Center.Y + rad*math.Sin(e.StartAngle*math.Pi/180)
		ex := e.Center.X + rad*math.Cos(e.EndAngle*math.Pi/180)
		ey := e.Center.Y + rad*math.Sin(e.EndAngle*math.Pi/180)
		tsx, tsy := ai(sx, sy)
		tex, tey := ai(ex, ey)
		startA := math.Atan2(tsy-cy, tsx-cx) * 180 / math.Pi
		endA := math.Atan2(tey-cy, tex-cx) * 180 / math.Pi
		// Ensure CCW winding: if mirrored, the winding reverses so we swap
		if insScaleX*insScaleY < 0 {
			startA, endA = endA, startA
		}
		if endA <= startA {
			endA += 360
		}
		r.DrawArc(cx, cy, e.Radius*math.Abs(insScaleX), startA, endA)

	case *dxf.Ellipse:
		cx, cy := ai(e.Center.X, e.Center.Y)
		r.DrawEllipse(cx, cy, e.MajorAxis.X*insScaleX, e.MajorAxis.Y*insScaleY,
			e.MinorAxisRatio, e.StartAngle, e.EndAngle)

	case *dxf.LWPolyline:
		renderLWPolyline(r, e, baseX, baseY, insX, insY, insScaleX, insScaleY, insRotDeg)

	case *dxf.Polyline:
		renderPolyline(r, e, baseX, baseY, insX, insY, insScaleX, insScaleY, insRotDeg)

	case *dxf.Spline:
		renderSpline(r, e, baseX, baseY, insX, insY, insScaleX, insScaleY, insRotDeg)

	case *dxf.Text:
		x, y := ai(e.Location.X, e.Location.Y)
		r.DrawText(x, y, e.Value, e.Height*math.Abs(insScaleY), e.Rotation+insRotDeg)

	case *dxf.MText:
		x, y := ai(e.InsertionPoint.X, e.InsertionPoint.Y)
		raw := e.Text
		for _, ext := range e.ExtendedText {
			raw += ext
		}
		segments := ParseMText(raw)
		r.DrawMText(x, y, segments, e.InitialTextHeight*math.Abs(insScaleY), e.RotationAngle*180/math.Pi+insRotDeg)

	case *dxf.ModelPoint:
		x, y := ai(e.Location.X, e.Location.Y)
		r.DrawPoint(x, y)

	case *dxf.Solid:
		x1, y1 := ai(e.FirstCorner.X, e.FirstCorner.Y)
		x2, y2 := ai(e.SecondCorner.X, e.SecondCorner.Y)
		x3, y3 := ai(e.ThirdCorner.X, e.ThirdCorner.Y)
		x4, y4 := ai(e.FourthCorner.X, e.FourthCorner.Y)
		r.SetStyle(rgb, 0)
		r.SetFillColor(rgb)
		r.DrawSolid(x1, y1, x2, y2, x3, y3, x4, y4)

	case *dxf.Hatch:
		if enableHatch && !e.SolidFill && len(e.PatternLines) > 0 {
			r.SetStyle(rgb, 0.05) // thin lines for hatch fill
			lines := generateHatchFillLines(e)
			for _, seg := range lines {
				x1, y1 := ai(seg[0], seg[1])
				x2, y2 := ai(seg[2], seg[3])
				r.DrawLine(x1, y1, x2, y2)
			}
		}

	case *dxf.Insert:
		// Transform INSERT position to world coords, then recurse with block's base point.
		if blk, ok := blocks[e.Name]; ok {
			wx, wy := ai(e.Location.X, e.Location.Y)
			newScaleX := insScaleX * e.XScaleFactor
			newScaleY := insScaleY * e.YScaleFactor
			newRot := insRotDeg + e.Rotation
			for _, be := range blk.Entities {
				renderEntity(r, be, layers, blocks, layerFilter, depth+1,
					blk.BasePoint.X, blk.BasePoint.Y,
					wx, wy, newScaleX, newScaleY, newRot)
			}
		}

	default:
		// DIMENSION entities reference an anonymous block containing their geometry.
		// The block entities are in world coordinates (basePoint=0,0) and may be on
		// sublayers (e.g. "Layer_Pen_No__1"), so we bypass the layer filter here —
		// the dimension entity itself already passed the filter above.
		if dim, ok := ent.(dxf.Dimension); ok {
			if blk, ok := blocks[dim.BlockName()]; ok {
				for _, be := range blk.Entities {
					renderEntity(r, be, layers, blocks, nil, depth+1,
						0, 0, 0, 0, 1, 1, 0)
				}
			}
		}
	}
}

func renderLWPolyline(r *Renderer, e *dxf.LWPolyline, baseX, baseY, insX, insY, scX, scY, rotDeg float64) {
	verts := e.Vertices
	n := len(verts)
	if n < 2 {
		return
	}

	closed := e.IsClosed()
	count := n
	if closed {
		count = n
	}

	for i := 0; i < count; i++ {
		j := (i + 1) % n
		if !closed && j == 0 && i != 0 {
			break
		}
		x1, y1 := applyInsert(verts[i].X, verts[i].Y, baseX, baseY, insX, insY, scX, scY, rotDeg)
		x2, y2 := applyInsert(verts[j].X, verts[j].Y, baseX, baseY, insX, insY, scX, scY, rotDeg)

		if math.Abs(verts[i].Bulge) > 1e-10 {
			bulge := verts[i].Bulge
			// Mirroring (one negative scale) reverses arc direction
			if scX*scY < 0 {
				bulge = -bulge
			}
			r.DrawBulgeArc(x1, y1, x2, y2, bulge)
		} else {
			r.DrawLine(x1, y1, x2, y2)
		}
	}
}

func renderPolyline(r *Renderer, e *dxf.Polyline, baseX, baseY, insX, insY, scX, scY, rotDeg float64) {
	verts := e.Vertices
	if len(verts) < 2 {
		return
	}
	for i := 1; i < len(verts); i++ {
		x1, y1 := applyInsert(verts[i-1].Location.X, verts[i-1].Location.Y, baseX, baseY, insX, insY, scX, scY, rotDeg)
		x2, y2 := applyInsert(verts[i].Location.X, verts[i].Location.Y, baseX, baseY, insX, insY, scX, scY, rotDeg)
		if math.Abs(verts[i-1].Bulge) > 1e-10 {
			bulge := verts[i-1].Bulge
			if scX*scY < 0 {
				bulge = -bulge
			}
			r.DrawBulgeArc(x1, y1, x2, y2, bulge)
		} else {
			r.DrawLine(x1, y1, x2, y2)
		}
	}
}

func renderSpline(r *Renderer, e *dxf.Spline, baseX, baseY, insX, insY, scX, scY, rotDeg float64) {
	cps := make([][2]float64, len(e.ControlPoints))
	for i, cp := range e.ControlPoints {
		x, y := applyInsert(cp.Point.X, cp.Point.Y, baseX, baseY, insX, insY, scX, scY, rotDeg)
		cps[i] = [2]float64{x, y}
	}
	r.DrawSpline(cps, e.DegreeOfCurve, e.KnotValues)
}

// applyInsert transforms a point from block-local coordinates to world coordinates.
// The transform is: (point - basePoint) * scale → rotate → + insertPoint.
// baseX/baseY is the block's base point which must be subtracted before scaling,
// since block entities may store coordinates in world space.
func applyInsert(x, y, baseX, baseY, insX, insY, scX, scY, rotDeg float64) (float64, float64) {
	x = (x - baseX) * scX
	y = (y - baseY) * scY
	if math.Abs(rotDeg) > 0.001 {
		rad := rotDeg * math.Pi / 180
		cos := math.Cos(rad)
		sin := math.Sin(rad)
		x, y = x*cos-y*sin, x*sin+y*cos
	}
	return x + insX, y + insY
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

