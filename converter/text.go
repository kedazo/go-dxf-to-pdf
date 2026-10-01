package converter

import (
	"math"
	"strconv"
	"strings"

	dxf "github.com/ixmilia/dxf-go"
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

// mtextRotationDeg returns the MTEXT rotation in degrees. The X-axis direction
// vector (group 11), when present, takes precedence over the rotation angle
// (group 50, radians).
func mtextRotationDeg(m *dxf.MText) float64 {
	if x := m.XAxisDirection; x.X != 0 || x.Y != 0 {
		return math.Atan2(x.Y, x.X) * 180 / math.Pi
	}
	return m.RotationAngle * 180 / math.Pi
}
