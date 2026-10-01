package converter

import (
	"bytes"
	"strconv"
	"sync"

	dxf "github.com/ixmilia/dxf-go"
)

// The DXF parser appends a HATCH's seed points (group 98 followed by 10/20
// pairs, written after the boundary data) to the last boundary path when the
// hatch has no pattern definition, i.e. for SOLID fills. Each leaked seed
// pulls a wedge out of the fill towards the seed (often the origin). Until
// the parser handles seed points itself, trim them using the seed counts
// scanned from the raw file. A one-time self-test turns this off as soon as
// the parser no longer leaks.

// seedLeakProbe is a minimal SOLID hatch: a 3-vertex boundary plus one seed.
const seedLeakProbe = "0\nSECTION\n2\nENTITIES\n0\nHATCH\n5\nA1\n100\nAcDbEntity\n8\n0\n" +
	"100\nAcDbHatch\n10\n0.0\n20\n0.0\n30\n0.0\n2\nSOLID\n70\n1\n71\n0\n91\n1\n" +
	"92\n2\n72\n0\n73\n1\n93\n3\n10\n0.0\n20\n0.0\n10\n1.0\n20\n0.0\n10\n0.0\n20\n1.0\n97\n0\n" +
	"75\n0\n76\n1\n98\n1\n10\n5.0\n20\n5.0\n0\nENDSEC\n0\nEOF\n"

var parserLeaksHatchSeeds = sync.OnceValue(func() bool {
	d, err := dxf.ReadFromReader(bytes.NewReader([]byte(seedLeakProbe)))
	if err != nil {
		return false
	}
	for _, e := range d.Entities {
		if h, ok := e.(*dxf.Hatch); ok && len(h.Paths) == 1 {
			return len(h.Paths[0].Vertices) > 3
		}
	}
	return false
})

// scanHatchSeedCounts returns the number of seed points of every HATCH in a
// text DXF that has any, keyed by entity handle.
func scanHatchSeedCounts(data []byte) map[dxf.Handle]int {
	seeds := make(map[dxf.Handle]int)
	if !bytes.Contains(data, []byte("HATCH")) {
		return seeds
	}

	var (
		inHatch bool
		handle  dxf.Handle
		count   int
	)
	flush := func() {
		if inHatch && count > 0 {
			seeds[handle] = count
		}
		inHatch, handle, count = false, 0, 0
	}

	for pos := 0; pos < len(data); {
		code, next := nextLine(data, pos)
		value, after := nextLine(data, next)
		pos = after
		switch string(code) {
		case "0":
			flush()
			inHatch = string(value) == "HATCH"
		case "5":
			if inHatch {
				if h, err := strconv.ParseUint(string(value), 16, 64); err == nil {
					handle = dxf.Handle(h)
				}
			}
		case "98":
			if inHatch {
				count, _ = strconv.Atoi(string(value))
			}
		}
	}
	flush()
	return seeds
}

// nextLine returns the trimmed line starting at pos and the start of the
// following line.
func nextLine(data []byte, pos int) ([]byte, int) {
	if pos >= len(data) {
		return nil, len(data)
	}
	end := bytes.IndexByte(data[pos:], '\n')
	if end < 0 {
		return bytes.TrimSpace(data[pos:]), len(data)
	}
	return bytes.TrimSpace(data[pos : pos+end]), pos + end + 1
}

// trimLeakedHatchSeeds removes leaked seed vertices from the last boundary
// path of every pattern-less hatch (top level and inside blocks).
func trimLeakedHatchSeeds(d *dxf.Drawing, seeds map[dxf.Handle]int) {
	if len(seeds) == 0 {
		return
	}
	trim := func(ents []dxf.Entity) {
		for _, e := range ents {
			h, ok := e.(*dxf.Hatch)
			if !ok || len(h.PatternLines) > 0 || len(h.Paths) == 0 {
				continue
			}
			n := seeds[h.Handle()]
			last := &h.Paths[len(h.Paths)-1]
			if n > 0 && len(last.Vertices)-n >= 3 {
				last.Vertices = last.Vertices[:len(last.Vertices)-n]
			}
		}
	}
	trim(d.Entities)
	for i := range d.Blocks {
		trim(d.Blocks[i].Entities)
	}
}
