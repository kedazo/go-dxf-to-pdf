package converter

import dxf "github.com/kedazo/dxf-go"

const defaultLineWidthMM = 0.25

// hairlineMM is drawn for line weight 0, the thinnest pen.
const hairlineMM = 0.05

// LineWeightToMM converts a DXF LineWeight value to millimeters.
// DXF line weights are stored in 1/100 mm units; 0 is the thinnest pen.
// Special values: -3 (Standard), -1 (ByLayer), -2 (ByBlock).
func LineWeightToMM(lw dxf.LineWeight) float64 {
	v := int16(lw)
	switch {
	case v == 0:
		return hairlineMM
	case v < 0:
		return defaultLineWidthMM
	}
	return float64(v) / 100.0
}

// ResolveLineWeight resolves ByLayer/ByBlock line weights to mm. byBlock is
// the enclosing INSERT's line weight; hasLayer tells whether the entity's
// layer exists (ByLayer on a missing layer gets the default).
func ResolveLineWeight(entityLW, layerLW dxf.LineWeight, hasLayer bool, byBlock float64) float64 {
	switch entityLW {
	case dxf.LineWeightByLayer:
		if hasLayer {
			return LineWeightToMM(layerLW)
		}
		return defaultLineWidthMM
	case dxf.LineWeightByBlock:
		return byBlock
	default:
		return LineWeightToMM(entityLW)
	}
}
