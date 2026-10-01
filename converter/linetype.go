package converter

import (
	"math"
	"strings"

	dxf "github.com/kedazo/dxf-go"
)

// Paper sizes for linetype rendering.
const (
	dotMM       = 0.1 // a linetype dot (length 0) is drawn this long
	minPeriodMM = 0.3 // patterns repeating faster than this are drawn solid
)

// lineTypes holds the LTYPE table's dash patterns and the global scale.
type lineTypes struct {
	patterns map[string][]float64 // upper-cased name → DXF lengths (+dash, −gap, 0 dot)
	scale    float64              // $LTSCALE
	byName   map[string][]float64 // pattern lookups by the name as written (one conversion, one goroutine)
}

func newLineTypes(d *dxf.Drawing) *lineTypes {
	lt := &lineTypes{patterns: make(map[string][]float64), scale: d.Header.LineTypeScale}
	if lt.scale <= 0 {
		lt.scale = 1
	}
	for _, t := range d.LineTypes {
		if len(t.DashDotSpaceLengths) > 0 {
			lt.patterns[strings.ToUpper(t.Name)] = t.DashDotSpaceLengths
		}
	}
	return lt
}

// pattern returns the DXF dash lengths of a line type (nil = continuous).
func (lt *lineTypes) pattern(name string) []float64 {
	if lt == nil || name == "" || len(lt.patterns) == 0 {
		return nil
	}
	// Every entity asks: remember the answer instead of upper-casing again.
	if p, ok := lt.byName[name]; ok {
		return p
	}
	if lt.byName == nil {
		lt.byName = make(map[string][]float64)
	}
	p := lt.patterns[strings.ToUpper(name)]
	lt.byName[name] = p
	return p
}

// lineTypeName returns the line type an entity is drawn with: BYLAYER takes
// the (effective) layer's, BYBLOCK the enclosing INSERT's.
func (ctx drawCtx) lineTypeName(ent dxf.Entity, layers map[string]dxf.Layer) string {
	name := ent.LineTypeName()
	switch {
	case name == "" || strings.EqualFold(name, "BYLAYER"):
		if l, ok := layers[ctx.effectiveLayer(ent)]; ok {
			return l.LineTypeName
		}
		return ""
	case strings.EqualFold(name, "BYBLOCK"):
		return ctx.ltBlock
	}
	return name
}

// dashPattern turns DXF linetype lengths (+dash, −gap, 0 dot), scaled by
// scale (paper mm per drawing unit), into an alternating dash/gap list for
// Path.Dash, starting with a dash, plus the offset into that list where the
// DXF pattern starts. It returns nil (draw solid) for continuous patterns and
// for patterns too fine to show on paper.
func dashPattern(lengths []float64, scale float64) (offset float64, dashes []float64) {
	var segs []float64 // merged run lengths
	var on []bool      // run kinds
	for _, v := range lengths {
		isOn, l := v >= 0, math.Abs(v)*scale
		if v == 0 {
			l = dotMM
		}
		if math.IsNaN(l) || math.IsInf(l, 0) {
			return 0, nil
		}
		if n := len(segs); n > 0 && on[n-1] == isOn {
			segs[n-1] += l
		} else {
			segs, on = append(segs, l), append(on, isOn)
		}
	}
	// The pattern repeats: a last run of the same kind as the first joins it,
	// so the pattern now starts that much into the first run.
	if n := len(segs); n > 1 && on[0] == on[n-1] {
		offset = segs[n-1]
		segs[0] += segs[n-1]
		segs, on = segs[:n-1], on[:n-1]
	}
	if len(segs) < 2 {
		return 0, nil // all dash (continuous) or all gap
	}
	period := 0.0
	for _, s := range segs {
		period += s
	}
	if period < minPeriodMM {
		return 0, nil
	}
	// Path.Dash lists start with a dash: rotate a leading gap to the end.
	if !on[0] {
		offset = math.Mod(offset-segs[0]+period, period)
		segs = append(segs[1:], segs[0])
	}
	return offset, segs
}
