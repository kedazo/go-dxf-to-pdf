package converter

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	dxf "github.com/kedazo/dxf-go"
)

// Door and window evidence for --emit-wall-segments (schema v3). Like
// wallstructure.go this only EXTRACTS what the drawing says; BuildingDesigner
// decides which wall an opening sits in, its size and its swing.
//
//   - openings: every INSERT whose block name or layer names a door or window,
//     at any nesting depth (ArchiCAD nests them inside the "Wall_N_M" block of
//     their host wall), with the block's local extent mapped to the scene;
//   - arcs: the ARCs inside those blocks (door swings) and loose ARCs on door
//     or window layers, as three scene points each;
//   - gaps (wallsegments.go): the spans mergeGroup bridged inside a wall.

// XMLOpening is one door/window INSERT. Corners are the block-local extent
// (text left out) mapped through the instance transform: c0→c1 runs along the
// block's +X axis, c0→c3 along its +Y axis, so rotation, scale and mirroring
// are implied. NameWidth/NameHeight are sizes read from the block name (m).
type XMLOpening struct {
	ID         int     `xml:"id,attr"`
	Block      string  `xml:"block,attr"`
	Layer      string  `xml:"layer,attr"`
	Kind       string  `xml:"kind,attr"` // door | window | unknown
	Elem       int     `xml:"elem,attr"`
	Depth      int     `xml:"depth,attr"` // 0 = top-level INSERT
	X          float64 `xml:"x,attr"`
	Y          float64 `xml:"y,attr"`
	Corners    string  `xml:"corners,attr,omitempty"`
	NameWidth  float64 `xml:"nameWidth,attr,omitempty"`
	NameHeight float64 `xml:"nameHeight,attr,omitempty"`
}

// XMLArc is an ARC that may be a door swing: its centre, radius and start,
// middle and end points in scene meters (start → end runs the arc's way).
type XMLArc struct {
	Opening int     `xml:"opening,attr"` // owning <opening> id, -1 = loose on an opening layer
	Layer   string  `xml:"layer,attr"`
	Elem    int     `xml:"elem,attr"`
	CX      float64 `xml:"cx,attr"`
	CY      float64 `xml:"cy,attr"`
	R       float64 `xml:"r,attr"`
	X0      float64 `xml:"x0,attr"`
	Y0      float64 `xml:"y0,attr"`
	XM      float64 `xml:"xm,attr"`
	YM      float64 `xml:"ym,attr"`
	X1      float64 `xml:"x1,attr"`
	Y1      float64 `xml:"y1,attr"`
}

// XMLGap is a span of a detected wall that mergeGroup bridged (a door or
// window gap in both faces). T0/T1 are distances from the wall's x1,y1.
type XMLGap struct {
	Wall int     `xml:"wall,attr"` // <wall id>
	T0   float64 `xml:"t0,attr"`
	T1   float64 `xml:"t1,attr"`
	X1   float64 `xml:"x1,attr"`
	Y1   float64 `xml:"y1,attr"`
	X2   float64 `xml:"x2,attr"`
	Y2   float64 `xml:"y2,attr"`
}

type rawOpening struct {
	block, layer, kind string
	elem, depth        int
	x, y               float64
	corners            [][2]float64 // world, nil when the block has no extent
	nameW, nameH       float64
}

type rawArc struct {
	opening, elem int
	layer         string
	cx, cy, r     float64 // world centre, radius in DXF units
	pts           [3][2]float64
}

// Name words, lower-case. Exclusions win: markers (ArchiCAD "konszignáció"
// stamps), labels and floor symbols ("padló nyílásszimbólum") are not
// openings; a bare "floor" would drop every Revit name ("…-1st Floor Plan").
// There is deliberately no bare "nyílás"/"opening": "Simple Door Opening" is
// found by "door", the floor symbol must not be.
var (
	openingExclude = []string{"konszign", "marker", "címke", "cimke", "label", "tag", "text",
		"anno", "jel -", "jel-", "padló", "padlo", "floor opening"}
	openingDoorWords    = []string{"ajtó", "ajto", "door", "kapu", "bejárat", "bejarat"}
	openingWindowWords  = []string{"ablak", "window", "glaz"}
	openingGenericWords = []string{"nyílászáró", "nyilaszaro"}

	// "AJT1 egyszárnyú", "AJT2 kétszárnyú": ArchiCAD's abbreviated door blocks
	// (a ZWCAD plan). Only at a word start -- "fajta" (kind) and "rajta" (on
	// it) hold "ajt" too.
	openingDoorAbbrev = regexp.MustCompile(`(?:^|[^\pL])ajt`)
)

// openingKind classifies an INSERT (block name and effective layer) or, with
// an empty name, a layer: "door", "window" or "unknown" (a generic
// door-or-window word). The name decides first, then the layer; door words
// beat window words ("ajtó felülvilágítóval" is a door with a fanlight).
func openingKind(name, layer string) (string, bool) {
	n, l := strings.ToLower(name), strings.ToLower(layer)
	if anySubstr(n, openingExclude) || anySubstr(l, openingExclude) || strings.HasPrefix(n, "wall_") {
		return "", false
	}
	for _, s := range []string{n, l} {
		switch {
		case anySubstr(s, openingDoorWords), openingDoorAbbrev.MatchString(s):
			return "door", true
		case anySubstr(s, openingWindowWords):
			return "window", true
		case anySubstr(s, openingGenericWords):
			return "unknown", true
		}
	}
	return "", false
}

var (
	// Revit writes feet-inches with ' and " replaced: "2_-4_ X 6_-8_" = 2'-4" × 6'-8".
	sizeFtIn = regexp.MustCompile(`(\d+)_-(\d+)_\s*[xX]\s*(\d+)_-(\d+)_`)
	// "90x210" (cm), "900x2100" (mm), "90/210".
	sizeMetric = regexp.MustCompile(`(?:^|\D)(\d{2,4})\s*[xX×/]\s*(\d{2,4})(?:\D|$)`)
)

// parseOpeningSize reads a width × height (m) from a block name.
func parseOpeningSize(name string) (w, h float64, ok bool) {
	if m := sizeFtIn.FindStringSubmatch(name); m != nil {
		f := func(ft, in string) float64 {
			a, _ := strconv.Atoi(ft)
			b, _ := strconv.Atoi(in)
			return (float64(a)*12 + float64(b)) * 0.0254
		}
		w, h = f(m[1], m[2]), f(m[3], m[4])
	} else if m := sizeMetric.FindStringSubmatch(name); m != nil {
		f := func(s string) float64 {
			v, _ := strconv.Atoi(s)
			if v >= 1000 {
				return float64(v) / 1000
			}
			return float64(v) / 100
		}
		w, h = f(m[1]), f(m[2])
	} else {
		return 0, 0, false
	}
	if w < 0.3 || w > 6 || h < 0.3 || h > 4 {
		return 0, 0, false
	}
	return round6(w), round6(h), true
}

// blockLocalBox returns the extent of a block's content in its own
// coordinates, leaving out text (attribute definitions, labels) and
// dimensions, which would stretch the opening. ctx is the context of the
// block's content (its transform is ignored). Memoised per block.
func (s *wallStructure) blockLocalBox(blk *dxf.Block, blocks map[string]*dxf.Block, ctx drawCtx) BBox {
	if bb, ok := s.boxes[blk.Name]; ok {
		return bb
	}
	bb := NewBBox()
	lctx := ctx
	lctx.m = identityAffine
	for _, be := range blk.Entities {
		switch be.(type) {
		case *dxf.Text, *dxf.MText, *dxf.AttributeDefinition, *dxf.Attribute:
			continue
		}
		if _, isDim := be.(dxf.Dimension); isDim {
			continue
		}
		expandBBoxForEntity(&bb, be, blocks, lctx)
	}
	if s.boxes == nil {
		s.boxes = map[string]BBox{}
	}
	s.boxes[blk.Name] = bb
	return bb
}

// addOpening records one instance of an opening INSERT (cctx = the context of
// its block content, ctx.inner of the instance) and returns its id.
func (s *wallStructure) addOpening(e *dxf.Insert, blk *dxf.Block, blocks map[string]*dxf.Block,
	kind, layer string, elem int, cctx drawCtx) int {

	o := rawOpening{block: e.Name, layer: layer, kind: kind, elem: elem, depth: cctx.depth - 1}
	o.x, o.y = cctx.m.apply(blk.BasePoint.X, blk.BasePoint.Y) // the instance's insertion point
	if bb := s.blockLocalBox(blk, blocks, cctx); bb.MinX <= bb.MaxX && bb.MinY <= bb.MaxY {
		for _, c := range [][2]float64{{bb.MinX, bb.MinY}, {bb.MaxX, bb.MinY}, {bb.MaxX, bb.MaxY}, {bb.MinX, bb.MaxY}} {
			x, y := cctx.m.apply(c[0], c[1])
			o.corners = append(o.corners, [2]float64{x, y})
		}
	}
	o.nameW, o.nameH, _ = parseOpeningSize(e.Name)
	s.openings = append(s.openings, o)
	return len(s.openings) - 1
}

// addArc records an ARC (block-local, mapped through m) owned by opening.
func (s *wallStructure) addArc(e *dxf.Arc, m affine, layer string, elem, opening int) {
	om := m.mul(ocsAffine(e.Normal, e.Center.Z))
	t0, t1 := arcParams(e.StartAngle, e.EndAngle)
	if t1 <= t0 || e.Radius <= 0 {
		return
	}
	a := rawArc{opening: opening, elem: elem, layer: layer, r: e.Radius * om.linearScale()}
	a.cx, a.cy = om.apply(e.Center.X, e.Center.Y)
	for i, t := range []float64{t0, (t0 + t1) / 2, t1} {
		a.pts[i][0], a.pts[i][1] = om.apply(e.Center.X+e.Radius*math.Cos(t), e.Center.Y+e.Radius*math.Sin(t))
	}
	s.arcs = append(s.arcs, a)
}

// openingsXML maps the collected openings and arcs to scene meters.
func openingsXML(s *wallStructure, sx, sy func(float64) float64, unitToMeters float64) ([]XMLOpening, []XMLArc) {
	f := func(v float64) string { return strconv.FormatFloat(round6(v), 'f', -1, 64) }
	ops := make([]XMLOpening, 0, len(s.openings))
	for i, o := range s.openings {
		var b strings.Builder
		for j, c := range o.corners {
			if j > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(f(sx(c[0])) + "," + f(sy(c[1])))
		}
		ops = append(ops, XMLOpening{ID: i, Block: o.block, Layer: o.layer, Kind: o.kind,
			Elem: o.elem, Depth: o.depth, X: round6(sx(o.x)), Y: round6(sy(o.y)),
			Corners: b.String(), NameWidth: o.nameW, NameHeight: o.nameH})
	}
	arcs := make([]XMLArc, 0, len(s.arcs))
	for _, a := range s.arcs {
		arcs = append(arcs, XMLArc{Opening: a.opening, Layer: a.layer, Elem: a.elem,
			CX: round6(sx(a.cx)), CY: round6(sy(a.cy)), R: round6(a.r * unitToMeters),
			X0: round6(sx(a.pts[0][0])), Y0: round6(sy(a.pts[0][1])),
			XM: round6(sx(a.pts[1][0])), YM: round6(sy(a.pts[1][1])),
			X1: round6(sx(a.pts[2][0])), Y1: round6(sy(a.pts[2][1]))})
	}
	return ops, arcs
}
