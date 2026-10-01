package converter

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	dxf "github.com/kedazo/dxf-go"
)

// parseUnicodeEscape decodes a "\U+XXXX" escape at the start of s and returns
// the rune and the number of bytes consumed (0 if s does not start with one).
func parseUnicodeEscape(s string) (rune, int) {
	const prefix = `\U+`
	if len(s) < len(prefix)+4 || !strings.HasPrefix(s, prefix) {
		return 0, 0
	}
	v, err := strconv.ParseUint(s[len(prefix):len(prefix)+4], 16, 32)
	if err != nil {
		return 0, 0
	}
	return rune(v), len(prefix) + 4
}

// decodeTextValue converts the control codes of a single-line TEXT/ATTRIB
// value into plain text: %%c %%d %%p (Ø ° ±), %%% (%), %%nnn (character
// code), \U+XXXX escapes. %%u / %%o (underline / overline toggles) are dropped.
func decodeTextValue(s string) string {
	if !strings.Contains(s, "%%") && !strings.Contains(s, `\U+`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' {
			if r, n := parseUnicodeEscape(s[i:]); n > 0 {
				b.WriteRune(r)
				i += n
				continue
			}
		}
		if s[i] == '%' && i+2 < len(s) && s[i+1] == '%' {
			switch c := s[i+2]; {
			case c == 'c' || c == 'C':
				b.WriteRune('Ø')
				i += 3
				continue
			case c == 'd' || c == 'D':
				b.WriteRune('°')
				i += 3
				continue
			case c == 'p' || c == 'P':
				b.WriteRune('±')
				i += 3
				continue
			case c == 'u' || c == 'U' || c == 'o' || c == 'O':
				i += 3
				continue
			case c == '%':
				b.WriteByte('%')
				i += 3
				continue
			case c >= '0' && c <= '9':
				j := i + 2
				for j < len(s) && j < i+5 && s[j] >= '0' && s[j] <= '9' {
					j++
				}
				if v, err := strconv.Atoi(s[i+2 : j]); err == nil && v > 0 {
					b.WriteRune(rune(v))
					i = j
					continue
				}
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// textAnchor is where and how a single-line TEXT (or ATTRIB) is anchored, in
// the entity's own coordinates.
type textAnchor struct {
	X, Y        float64
	HAlign      float64 // 0 left, 0.5 center, 1 right
	VAlign      textVAlign
	RotationDeg float64
}

// resolveTextAnchor maps DXF TEXT justification to an anchor point. For any
// justification other than left/baseline, DXF places the text at the second
// alignment point (group 11); "aligned" and "fit" span first→second point.
func resolveTextAnchor(loc, second dxf.Point, hj dxf.HorizontalTextJustification,
	vj dxf.VerticalTextJustification, rotationDeg float64) textAnchor {

	a := textAnchor{X: loc.X, Y: loc.Y, RotationDeg: rotationDeg}
	if hj == dxf.HorizontalTextJustificationLeft && vj == dxf.VerticalTextJustificationBaseline {
		return a
	}

	switch hj {
	case dxf.HorizontalTextJustificationAligned, dxf.HorizontalTextJustificationFit:
		// Text runs from the first to the second point; its direction wins
		// over the rotation field.
		if dx, dy := second.X-loc.X, second.Y-loc.Y; dx != 0 || dy != 0 {
			a.RotationDeg = math.Atan2(dy, dx) * 180 / math.Pi
		}
		return a
	case dxf.HorizontalTextJustificationCenter:
		a.HAlign = 0.5
	case dxf.HorizontalTextJustificationRight:
		a.HAlign = 1
	case dxf.HorizontalTextJustificationMiddle:
		a.HAlign = 0.5
		vj = dxf.VerticalTextJustification(vAlignMiddle)
	}
	a.VAlign = textVAlign(vj)

	// Some writers leave group 11 unset; fall back to the first point then.
	if second.X != 0 || second.Y != 0 || (loc.X == 0 && loc.Y == 0) {
		a.X, a.Y = second.X, second.Y
	}
	return a
}

// Text extents for bounding boxes are estimated, not measured (fonts are
// only loaded for drawing): a glyph advances about 0.8 × the cap height,
// descenders reach 0.3 × the cap height below the baseline.
const (
	estCharWidth = 0.8
	estDescent   = 0.3
)

// expandRotatedRect expands bb with the rectangle [x0,x1]×[y0,y1] given
// relative to (ax, ay) and rotated about it by rotDeg, mapped through t.
func expandRotatedRect(bb *BBox, t affine, ax, ay, rotDeg, x0, x1, y0, y1 float64) {
	s, c := math.Sincos(rotDeg * math.Pi / 180)
	for _, p := range [4][2]float64{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}} {
		bb.Expand(t.apply(ax+p[0]*c-p[1]*s, ay+p[0]*s+p[1]*c))
	}
}

// expandTextLine expands bb with the estimated extent of a single-line
// TEXT/ATTRIB anchored at a (entity coordinates, mapped through t).
func expandTextLine(bb *BBox, t affine, value string, height, widthFactor float64, a textAnchor) {
	if height <= 0 {
		bb.Expand(t.apply(a.X, a.Y))
		return
	}
	if widthFactor <= 0 {
		widthFactor = 1
	}
	w := float64(utf8.RuneCountInString(value)) * estCharWidth * height * widthFactor
	x0 := -a.HAlign * w
	var y0, y1 float64
	switch a.VAlign {
	case vAlignBottom:
		y0, y1 = 0, (1+estDescent)*height
	case vAlignMiddle:
		y0, y1 = -(0.5+estDescent)*height, height/2
	case vAlignTop:
		y0, y1 = -(1+estDescent)*height, 0
	default: // baseline
		y0, y1 = -estDescent*height, height
	}
	expandRotatedRect(bb, t, a.X, a.Y, a.RotationDeg, x0, x0+w, y0, y1)
}

// expandMText expands bb with the estimated extent of an MTEXT, laid out as
// DrawMText does (line pitch 5/3 of the height, attachment point anchor).
func expandMText(bb *BBox, m affine, e *dxf.MText) {
	h := e.InitialTextHeight
	if h <= 0 {
		bb.Expand(m.apply(e.InsertionPoint.X, e.InsertionPoint.Y))
		return
	}
	lines, maxRunes, cur := 1, 0, 0
	for _, s := range ParseMText(e.FormattedText()) {
		if s.NewLine {
			lines, cur = lines+1, 0
			continue
		}
		cur += utf8.RuneCountInString(s.Text)
		maxRunes = max(maxRunes, cur)
	}
	spacing := e.LineSpacingFactor
	if spacing <= 0 {
		spacing = 1
	}
	w := float64(maxRunes) * estCharWidth * h
	total := h + float64(lines-1)*h*5/3*spacing
	attach := int(e.AttachmentPoint)
	if attach < 1 || attach > 9 {
		attach = 1
	}
	x0 := -float64((attach-1)%3) / 2 * w
	var y0, y1 float64
	switch (attach - 1) / 3 {
	case 0: // top
		y0, y1 = -total-estDescent*h, 0
	case 1: // middle
		y0, y1 = -total/2-estDescent*h, total/2
	default: // bottom
		y0, y1 = -estDescent*h, total
	}
	expandRotatedRect(bb, m, e.InsertionPoint.X, e.InsertionPoint.Y, mtextRotationDeg(e), x0, x0+w, y0, y1)
}

// mtextRotationDeg returns the MTEXT rotation in degrees. The X-axis direction
// vector (group 11), when present, takes precedence over the rotation angle
// (group 50). The parser defaults a missing direction to (1,0,0), so then
// the angle is used. Group 50 is taken in degrees, as written by ezdxf and
// other libraries producing MTEXT without a direction (the DXF reference
// says radians, but AutoCAD always writes the direction).
func mtextRotationDeg(m *dxf.MText) float64 {
	x := m.XAxisDirection
	if (x.X == 0 && x.Y == 0) || (x.X == 1 && x.Y == 0 && m.RotationAngle != 0) {
		return m.RotationAngle
	}
	return math.Atan2(x.Y, x.X) * 180 / math.Pi
}
