package converter

import dxf "github.com/kedazo/dxf-go"

const defaultLineWidthMM = 0.25

// LineWeightToMM converts a DXF LineWeight value to millimeters.
// DXF line weights are stored in 1/100 mm units.
// Special values: -3 (Standard), -1 (ByLayer), -2 (ByBlock).
func LineWeightToMM(lw dxf.LineWeight) float64 {
	v := int16(lw)
	if v <= 0 {
		return defaultLineWidthMM
	}
	return float64(v) / 100.0
}

// ResolveLineWeight resolves ByLayer/ByBlock line weights.
func ResolveLineWeight(entityLW, layerLW dxf.LineWeight) float64 {
	switch entityLW {
	case dxf.LineWeightStandard:
		return defaultLineWidthMM
	case dxf.LineWeightByLayer:
		return LineWeightToMM(layerLW)
	case dxf.LineWeightByBlock:
		return defaultLineWidthMM
	default:
		return LineWeightToMM(entityLW)
	}
}
