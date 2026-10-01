package converter

import (
	"testing"

	dxf "github.com/ixmilia/dxf-go"
)

// The probe's seed point must never end up as a boundary vertex, whether or
// not the parser itself leaks seeds.
func TestSolidHatchSeedPointsNotInBoundary(t *testing.T) {
	d, err := parseDxfData([]byte(seedLeakProbe))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var h *dxf.Hatch
	for _, e := range d.Entities {
		if hh, ok := e.(*dxf.Hatch); ok {
			h = hh
		}
	}
	if h == nil || len(h.Paths) != 1 {
		t.Fatalf("hatch not parsed: %+v", h)
	}
	if v := h.Paths[0].Vertices; len(v) != 3 {
		t.Errorf("boundary = %v, want the 3 boundary vertices only (no seed point)", v)
	}
}

func TestScanHatchSeedCounts(t *testing.T) {
	data := []byte("  0\r\nHATCH\r\n  5\r\n1F\r\n 98\r\n     2\r\n  0\r\nLINE\r\n  5\r\n20\r\n 98\r\n 7\r\n  0\r\nHATCH\r\n  5\r\n21\r\n  0\r\nEOF\r\n")
	got := scanHatchSeedCounts(data)
	if len(got) != 1 || got[dxf.Handle(0x1F)] != 2 {
		t.Errorf("seeds = %v, want map[0x1F:2]", got)
	}
}
