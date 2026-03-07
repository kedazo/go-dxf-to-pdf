package converter

import dxf "github.com/ixmilia/dxf-go"

const defaultLineWidthMM = 0.25

// LineWeightToMM converts a DXF LineWeight value to millimeters.
// DXF line weights are stored in 1/100 mm units.
// Special values: -3 (Standard), -2 (ByLayer), -1 (ByBlock).
func LineWeightToMM(lw dxf.LineWeight) float64 {
	v := int16(lw)
	if v <= 0 {
		return defaultLineWidthMM
	}
	return float64(v) / 100.0
}

// ResolveLineWeight resolves ByLayer/ByBlock line weights.
func ResolveLineWeight(entityLW, layerLW dxf.LineWeight) float64 {
	v := int16(entityLW)
	switch v {
	case -3: // Standard
		return defaultLineWidthMM
	case -2: // ByLayer
		return LineWeightToMM(layerLW)
	case -1: // ByBlock
		return defaultLineWidthMM
	default:
		return LineWeightToMM(entityLW)
	}
}
