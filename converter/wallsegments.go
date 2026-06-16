package converter

import (
	"encoding/xml"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	dxf "github.com/ixmilia/dxf-go"
)

// WallSegmentsOptions controls the --emit-wall-segments mode: which file/layers
// to read and how the wall-pair heuristic is tuned. All lengths are in meters.
type WallSegmentsOptions struct {
	Dwg2Dxf       string   // path to dwg2dxf binary (DWG input); auto-detected if empty
	Layers        []string // optional collection restrict filter (same semantics as Convert)
	MinThickness  float64  // m, default 0.03 — reject pairs closer than this
	MaxThickness  float64  // m, default 0.60 — reject pairs farther than this
	MinWallLength float64  // m, default 0.30 — min candidate face length (drops furniture ticks)
	AngleTolDeg   float64  // deg, default 5 — parallel-pair angle tolerance
	MergeGap      float64  // m, default 0.05 — collinear midline merge tolerance
	BridgeGap     float64  // m, default 1.20 — max door/window gap to bridge
	IncludeCurves bool     // default false — sample ARC/CIRCLE into raw chords (never paired)
	Scale         float64  // drawing scale (e.g. 0.01 for 1:100), for pxPerMeter meta
	DPI           float64  // raster DPI, for pxPerMeter meta

	// Unit override (mainly for unitless files like SketchUp exports).
	SourceUnit string  // "mm"|"cm"|"m"|"in"|"ft"; overrides the file's declared unit
	UnitScale  float64 // explicit DXF-unit -> meters factor; wins over SourceUnit when > 0

	// Layer filtering for WALL DETECTION ONLY (raw <segments> always keeps every layer).
	NoDefaultBlacklist bool     // disable the built-in junk-layer blacklist
	ExcludeLayers      []string // extra layer-name substrings to exclude from detection
	WallLayers         []string // positive filter: only detect on layers matching these (replaces blacklist)
}

func (o *WallSegmentsOptions) applyDefaults() {
	if o.MinThickness <= 0 {
		o.MinThickness = 0.03
	}
	if o.MaxThickness <= 0 {
		o.MaxThickness = 0.60
	}
	if o.MinWallLength <= 0 {
		o.MinWallLength = 0.30
	}
	if o.AngleTolDeg <= 0 {
		o.AngleTolDeg = 5
	}
	if o.MergeGap <= 0 {
		o.MergeGap = 0.05
	}
	if o.BridgeGap <= 0 {
		o.BridgeGap = 1.20
	}
	if o.Scale <= 0 {
		o.Scale = 0.01
	}
	if o.DPI <= 0 {
		o.DPI = 300
	}
}

// defaultWallBlacklist holds lowercase layer-name substrings that are never walls
// (furniture, MEP, annotation, openings, room zones, garden/site, finishes). It
// mirrors blacklisting.md, which is the source of truth — keep them in sync.
// Collision-prone codes are anchored (e.g. "cols" not "col", "e-elec", "q-cas").
var defaultWallBlacklist = []string{
	// furniture / interior equipment
	"berendezés", "bútor", "furniture", "furnishing", "furn", "appliance",
	"casework", "q-cas", "q-spcq", "equipment", "eqpm",
	// MEP — plumbing / electrical / mechanical
	"sanr", "fixt", "plumb", "e-elec", "e-lite", "electrical", "lighting",
	"hvac", "duct", "mech",
	// annotation / dimensions / labels / symbols
	"méretez", "dimension", "dims", "felirat", "címke", "label", "caption",
	"tag", "note", "title", "jel -", "jel-", "metszet", "section", "marker",
	"rajz és ábra", "symbol", "g-anno", "anno", "detl", "demo", "keyn", "iden",
	"leader", "callout", "text", "stamp", "grid",
	// circulation symbols (stairs/ramps are not walls)
	"strs", "stair", "ramp",
	// openings (real geometry, but not walls)
	"ablak", "ajtó", "door", "window", "glaz", "glass",
	// room / zone / area
	"helyiség", "zone", "room", "spcq",
	// garden / landscape / site
	"kert", "garden", "landscape", "növény", "planting", "vegetation",
	"terep", "terrain", "zöldfelület", "lawn", "shrub", "parkoló", "parking",
	// floor / ceiling / roof finishes
	"clng", "ceiling", "flor", "a-flor", "floor", "padló", "roof", "tető", "hral",
}

// wallKeepList holds lowercase substrings that mark a layer as wall/structural;
// a keep match wins over any blacklist match so real walls are never dropped.
var wallKeepList = []string{
	"wall", "fal", "a-cols", "cols", "column", "oszlop", "pillér",
	"structure", "structural", "bearing", "tartószerkezet", "vázszerkezet",
	"partition",
}

func anySubstr(s string, subs []string) bool {
	for _, sub := range subs {
		if sub != "" && strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func lowerAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToLower(s)
	}
	return out
}

// wallLayerEligible reports whether a layer may contribute to WALL DETECTION.
// Raw <segments> collection ignores this — it always keeps every layer.
func wallLayerEligible(layer string, opts WallSegmentsOptions) bool {
	s := strings.ToLower(layer)
	if len(opts.WallLayers) > 0 { // positive whitelist mode replaces the blacklist
		return anySubstr(s, lowerAll(opts.WallLayers))
	}
	if anySubstr(s, wallKeepList) { // keep wins over drop
		return true
	}
	if !opts.NoDefaultBlacklist && anySubstr(s, defaultWallBlacklist) {
		return false
	}
	if anySubstr(s, lowerAll(opts.ExcludeLayers)) {
		return false
	}
	return true
}

// unitNameToMeters maps a friendly unit alias to its DXF-unit -> meters factor.
func unitNameToMeters(name string) (float64, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "mm", "millimeter", "millimeters":
		return 0.001, true
	case "cm", "centimeter", "centimeters":
		return 0.01, true
	case "m", "meter", "meters":
		return 1, true
	case "in", "inch", "inches":
		return 0.0254, true
	case "ft", "foot", "feet":
		return 0.3048, true
	default:
		return 0, false
	}
}

// ThicknessBin is one (rounded) wall-thickness value and how many walls have it.
type ThicknessBin struct {
	Thickness float64
	Count     int
}

// WallSegmentsResult is a summary of an emit run, for self-diagnosing CLI output.
type WallSegmentsResult struct {
	SegmentCount      int
	WallCount         int
	SceneWidthMeters  float64
	SceneHeightMeters float64
	Units             string // resolved unit name (may note an override)
	UnitsOverridden   bool
	UnitWarning       string // non-empty when units look unreliable (unitless, no override)
	ThicknessClusters []ThicknessBin
}

// WallSegmentsDoc is the root of the emitted XML. Coordinates and lengths are in
// meters; the Y axis points DOWN, matching the PNG the tool renders with
// --auto-paper --margin 0 so segments overlay the raster 1:1.
type WallSegmentsDoc struct {
	XMLName  xml.Name     `xml:"wallExport"`
	Version  string       `xml:"version,attr"`
	Meta     XMLMeta      `xml:"meta"`
	Segments []XMLSegment `xml:"segments>segment"`
	Walls    []XMLWall    `xml:"walls>wall"`
}

// XMLMeta records the coordinate convention and the mapping back to DXF space so
// a consumer can place the data without re-deriving anything.
type XMLMeta struct {
	Units             string  `xml:"units,attr"`             // "meters"
	SourceUnits       string  `xml:"sourceUnits,attr"`       // original DXF units name
	YAxis             string  `xml:"yAxis,attr"`             // "down"
	UnitToMeters      float64 `xml:"unitToMeters,attr"`      // DXF unit -> meters
	OriginDxfX        float64 `xml:"originDxfX,attr"`        // bbox.MinX (scene x origin)
	OriginDxfY        float64 `xml:"originDxfY,attr"`        // bbox.MaxY (scene y origin, top-left)
	BBoxMinX          float64 `xml:"bboxMinX,attr"`          // DXF units
	BBoxMinY          float64 `xml:"bboxMinY,attr"`          // DXF units
	BBoxMaxX          float64 `xml:"bboxMaxX,attr"`          // DXF units
	BBoxMaxY          float64 `xml:"bboxMaxY,attr"`          // DXF units
	SceneWidthMeters  float64 `xml:"sceneWidthMeters,attr"`  //
	SceneHeightMeters float64 `xml:"sceneHeightMeters,attr"` //
	Scale             float64 `xml:"scale,attr"`             // e.g. 0.01 for 1:100
	DPI               float64 `xml:"dpi,attr"`               //
	PxPerMeter        float64 `xml:"pxPerMeter,attr"`        // pixels per scene meter in the PNG
	SegmentCount      int     `xml:"segmentCount,attr"`      //
	WallCount         int     `xml:"wallCount,attr"`         //
}

// XMLSegment is a raw exploded line in scene-meters (debug/ground-truth).
type XMLSegment struct {
	Layer      string  `xml:"layer,attr"`
	X1         float64 `xml:"x1,attr"`
	Y1         float64 `xml:"y1,attr"`
	X2         float64 `xml:"x2,attr"`
	Y2         float64 `xml:"y2,attr"`
	Length     float64 `xml:"length,attr"`
	LineWeight float64 `xml:"lineWeight,attr"` // meters
	Source     string  `xml:"source,attr"`     // LINE | LWPOLYLINE | POLYLINE | ARC | CIRCLE
	Arc        bool    `xml:"arc,attr,omitempty"`
}

// XMLWall is a detected wall: a centerline plus a single thickness scalar.
type XMLWall struct {
	Layer      string  `xml:"layer,attr"`
	X1         float64 `xml:"x1,attr"`
	Y1         float64 `xml:"y1,attr"`
	X2         float64 `xml:"x2,attr"`
	Y2         float64 `xml:"y2,attr"`
	Thickness  float64 `xml:"thickness,attr"` // meters
	Length     float64 `xml:"length,attr"`
	Confidence string  `xml:"confidence,attr"` // "paired"
}

// rawSeg is a collected line in DXF world coordinates (before the scene mapping).
type rawSeg struct {
	X1, Y1, X2, Y2 float64
	Layer          string
	LineWeightMM   float64
	Source         string
	IsArc          bool // polyline bulge chord / sampled curve: kept raw, excluded from pairing
}

// segGeom is a raw segment already mapped to scene-meters, fed to wall detection.
type segGeom struct {
	x1, y1, x2, y2 float64
	layer          string
	length         float64
	isArc          bool
}

// pairWall is a wall produced from a single parallel pair, before collinear merge.
type pairWall struct {
	x1, y1, x2, y2 float64
	thickness      float64
	ux, uy         float64 // unit direction of the centerline
	length         float64
	layer          string
}

// EmitWallSegments parses inputPath (DXF or DWG), explodes its geometry into raw
// line segments, detects wall pairs, and writes both to outPath as XML. It does
// not render a PDF/PNG (emit-and-exit, like Inspect). It returns a summary for
// self-diagnosing CLI output.
func EmitWallSegments(inputPath, outPath string, opts WallSegmentsOptions) (*WallSegmentsResult, error) {
	opts.applyDefaults()

	drawing, err := loadDrawing(inputPath, opts.Dwg2Dxf)
	if err != nil {
		return nil, err
	}

	// Layer / block lookups and optional restrict filter — identical to convertDrawing.
	layerMap := make(map[string]dxf.Layer)
	for _, l := range drawing.Layers {
		layerMap[l.Name] = l
	}
	blockMap := make(map[string]*dxf.Block)
	for i := range drawing.Blocks {
		blockMap[drawing.Blocks[i].Name] = &drawing.Blocks[i]
	}
	var layerFilter map[string]bool
	if len(opts.Layers) > 0 {
		layerFilter = make(map[string]bool)
		for _, l := range opts.Layers {
			layerFilter[l] = true
		}
	}

	unitFactor := UnitsToMM(drawing.Header.DefaultDrawingUnits)
	unitToMeters := unitFactor / 1000.0
	unitName := UnitsName(drawing.Header.DefaultDrawingUnits)

	// Unit override (UnitScale wins over SourceUnit). Warn when units look
	// unreliable (file is unitless and the caller gave no override).
	unitsOverridden := false
	var unitWarning string
	switch {
	case opts.UnitScale > 0:
		unitToMeters = opts.UnitScale
		unitName = fmt.Sprintf("%s (scale override %.6g)", unitName, opts.UnitScale)
		unitsOverridden = true
	case opts.SourceUnit != "":
		if f, ok := unitNameToMeters(opts.SourceUnit); ok {
			unitToMeters = f
			unitName = opts.SourceUnit + " (override)"
			unitsOverridden = true
		} else {
			return nil, fmt.Errorf("invalid --source-unit %q (use mm|cm|m|in|ft)", opts.SourceUnit)
		}
	}
	if !unitsOverridden && unitName == "unitless" {
		unitWarning = "drawing units are unitless; thicknesses/lengths assume 1 unit = 1 mm — " +
			"pass --source-unit or --unit-scale if walls are missing or mis-sized"
	}

	// Same bbox the PNG uses — this is what makes the scene coords PNG-aligned.
	bbox := ComputeBoundingBox(drawing.Entities, blockMap)
	if bbox.Width() <= 0 || bbox.Height() <= 0 {
		return nil, fmt.Errorf("empty drawing or no renderable entities")
	}

	// Collect raw segments in DXF world coordinates (identity insert transform).
	var raws []rawSeg
	for _, ent := range drawing.Entities {
		collectSegments(&raws, ent, layerMap, blockMap, layerFilter, opts,
			0, 0, 0, 0, 0, 1, 1, 0)
	}

	// DXF world -> scene meters (Y-down, PNG-aligned). Mirrors transform.go X/Y
	// with margin/offset = 0, expressed in meters instead of mm.
	sx := func(x float64) float64 { return (x - bbox.MinX) * unitToMeters }
	sy := func(y float64) float64 { return (bbox.MaxY - y) * unitToMeters }

	segments := make([]XMLSegment, 0, len(raws))
	geoms := make([]segGeom, 0, len(raws))
	for _, rs := range raws {
		x1, y1 := sx(rs.X1), sy(rs.Y1)
		x2, y2 := sx(rs.X2), sy(rs.Y2)
		length := math.Hypot(x2-x1, y2-y1)
		segments = append(segments, XMLSegment{
			Layer:      rs.Layer,
			X1:         round6(x1),
			Y1:         round6(y1),
			X2:         round6(x2),
			Y2:         round6(y2),
			Length:     round6(length),
			LineWeight: round6(rs.LineWeightMM / 1000.0),
			Source:     rs.Source,
			Arc:        rs.IsArc,
		})
		geoms = append(geoms, segGeom{
			x1: x1, y1: y1, x2: x2, y2: y2,
			layer: rs.Layer, length: length, isArc: rs.IsArc,
		})
	}

	walls := detectWalls(geoms, opts)

	doc := WallSegmentsDoc{
		Version: "1",
		Meta: XMLMeta{
			Units:             "meters",
			SourceUnits:       unitName,
			YAxis:             "down",
			UnitToMeters:      unitToMeters,
			OriginDxfX:        round6(bbox.MinX),
			OriginDxfY:        round6(bbox.MaxY),
			BBoxMinX:          round6(bbox.MinX),
			BBoxMinY:          round6(bbox.MinY),
			BBoxMaxX:          round6(bbox.MaxX),
			BBoxMaxY:          round6(bbox.MaxY),
			SceneWidthMeters:  round6(bbox.Width() * unitToMeters),
			SceneHeightMeters: round6(bbox.Height() * unitToMeters),
			Scale:             opts.Scale,
			DPI:               opts.DPI,
			// Under auto-paper/margin-0 the unit factor cancels:
			// px = scene_m * scale * dpi * 1000 / 25.4.
			PxPerMeter:   round6(opts.Scale * opts.DPI * 1000.0 / 25.4),
			SegmentCount: len(segments),
			WallCount:    len(walls),
		},
		Segments: segments,
		Walls:    walls,
	}

	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	data := append([]byte(xml.Header), out...)
	data = append(data, '\n')
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return nil, err
	}

	return &WallSegmentsResult{
		SegmentCount:      len(segments),
		WallCount:         len(walls),
		SceneWidthMeters:  doc.Meta.SceneWidthMeters,
		SceneHeightMeters: doc.Meta.SceneHeightMeters,
		Units:             unitName,
		UnitsOverridden:   unitsOverridden,
		UnitWarning:       unitWarning,
		ThicknessClusters: thicknessClusters(walls, 5),
	}, nil
}

// thicknessClusters returns the top-n most common wall thicknesses (rounded to
// the centimeter), most frequent first, for the run summary.
func thicknessClusters(walls []XMLWall, n int) []ThicknessBin {
	counts := make(map[float64]int)
	for _, w := range walls {
		counts[math.Round(w.Thickness*100)/100]++
	}
	bins := make([]ThicknessBin, 0, len(counts))
	for t, c := range counts {
		bins = append(bins, ThicknessBin{Thickness: t, Count: c})
	}
	sort.Slice(bins, func(i, j int) bool {
		if bins[i].Count != bins[j].Count {
			return bins[i].Count > bins[j].Count
		}
		return bins[i].Thickness < bins[j].Thickness
	})
	if len(bins) > n {
		bins = bins[:n]
	}
	return bins
}

// collectSegments walks one entity and appends its line geometry to out,
// recursing into INSERT blocks. It mirrors renderEntity's control flow and
// transform threading (so collected coords share the bbox space exactly) but
// appends instead of drawing.
func collectSegments(out *[]rawSeg, ent dxf.Entity, layers map[string]dxf.Layer,
	blocks map[string]*dxf.Block, layerFilter map[string]bool, opts WallSegmentsOptions,
	depth int, baseX, baseY, insX, insY, insScaleX, insScaleY, insRotDeg float64) {

	if depth > 10 || !ent.IsVisible() {
		return
	}
	if layerFilter != nil {
		if _, ok := layerFilter[ent.Layer()]; !ok {
			return
		}
	}

	_, lwMM := entityStyle(ent, layers)
	layerName := ent.Layer()

	ai := func(x, y float64) (float64, float64) {
		return applyInsert(x, y, baseX, baseY, insX, insY, insScaleX, insScaleY, insRotDeg)
	}
	add := func(x1, y1, x2, y2 float64, source string, isArc bool) {
		*out = append(*out, rawSeg{
			X1: x1, Y1: y1, X2: x2, Y2: y2,
			Layer: layerName, LineWeightMM: lwMM, Source: source, IsArc: isArc,
		})
	}

	switch e := ent.(type) {
	case *dxf.Line:
		x1, y1 := ai(e.P1.X, e.P1.Y)
		x2, y2 := ai(e.P2.X, e.P2.Y)
		add(x1, y1, x2, y2, "LINE", false)

	case *dxf.LWPolyline:
		verts := e.Vertices
		n := len(verts)
		if n < 2 {
			return
		}
		closed := e.IsClosed()
		for i := 0; i < n; i++ {
			j := (i + 1) % n
			if !closed && j == 0 && i != 0 {
				break
			}
			x1, y1 := ai(verts[i].X, verts[i].Y)
			x2, y2 := ai(verts[j].X, verts[j].Y)
			add(x1, y1, x2, y2, "LWPOLYLINE", math.Abs(verts[i].Bulge) > 1e-10)
		}

	case *dxf.Polyline:
		verts := e.Vertices
		if len(verts) < 2 {
			return
		}
		for i := 1; i < len(verts); i++ {
			x1, y1 := ai(verts[i-1].Location.X, verts[i-1].Location.Y)
			x2, y2 := ai(verts[i].Location.X, verts[i].Location.Y)
			add(x1, y1, x2, y2, "POLYLINE", math.Abs(verts[i-1].Bulge) > 1e-10)
		}

	case *dxf.Arc:
		if opts.IncludeCurves {
			addArcChords(add, e.Center.X, e.Center.Y, e.Radius, e.StartAngle, e.EndAngle, ai, "ARC")
		}

	case *dxf.Circle:
		if opts.IncludeCurves {
			addArcChords(add, e.Center.X, e.Center.Y, e.Radius, 0, 360, ai, "CIRCLE")
		}

	case *dxf.Insert:
		// Transform INSERT position to world coords, then recurse with block base point.
		if blk, ok := blocks[e.Name]; ok {
			wx, wy := ai(e.Location.X, e.Location.Y)
			newScaleX := insScaleX * e.XScaleFactor
			newScaleY := insScaleY * e.YScaleFactor
			newRot := insRotDeg + e.Rotation
			for _, be := range blk.Entities {
				collectSegments(out, be, layers, blocks, layerFilter, opts, depth+1,
					blk.BasePoint.X, blk.BasePoint.Y,
					wx, wy, newScaleX, newScaleY, newRot)
			}
		}

	default:
		// Skip DIMENSION anonymous blocks (leader/extension lines → wall false
		// positives) and all other entity types (Text, MText, Solid, Hatch,
		// Spline, Ellipse) — none represent walls.
	}
}

// addArcChords samples a circular arc (degrees, CCW) into ~10° straight chords in
// block-local space, transforms each point through ai, and appends them as raw
// arc segments (IsArc=true) so they appear in <segments> but never get paired.
func addArcChords(add func(x1, y1, x2, y2 float64, source string, isArc bool),
	cx, cy, radius, startDeg, endDeg float64,
	ai func(x, y float64) (float64, float64), source string) {

	if endDeg <= startDeg {
		endDeg += 360
	}
	const step = 10.0
	at := func(deg float64) (float64, float64) {
		rad := deg * math.Pi / 180
		return ai(cx+radius*math.Cos(rad), cy+radius*math.Sin(rad))
	}
	px, py := at(startDeg)
	for a := startDeg + step; a < endDeg; a += step {
		nx, ny := at(a)
		add(px, py, nx, ny, source, true)
		px, py = nx, ny
	}
	ex, ey := at(endDeg)
	add(px, py, ex, ey, source, true)
}

// detectWalls runs the wall-pair heuristic on scene-meter segments and returns
// detected walls (centerline + thickness), with collinear runs merged across
// door/window gaps.
func detectWalls(segs []segGeom, opts WallSegmentsOptions) []XMLWall {
	minWallLen := opts.MinWallLength // drop furniture ticks / short noise

	type cand struct {
		x1, y1, x2, y2 float64
		ux, uy         float64
		length         float64
		angle          float64 // canonical [0,180)
		layer          string
		consumed       bool
	}

	var cands []*cand
	for _, s := range segs {
		if s.isArc || s.length < minWallLen {
			continue
		}
		if !wallLayerEligible(s.layer, opts) {
			continue // furniture/MEP/annotation/opening layers don't form walls
		}
		dx := s.x2 - s.x1
		dy := s.y2 - s.y1
		L := math.Hypot(dx, dy)
		if L < 1e-9 {
			continue
		}
		cands = append(cands, &cand{
			x1: s.x1, y1: s.y1, x2: s.x2, y2: s.y2,
			ux: dx / L, uy: dy / L,
			length: L, angle: canonicalAngle(dx, dy),
			layer: s.layer,
		})
	}
	if len(cands) == 0 {
		return nil
	}

	// Greedy: pair longer (more reliable) faces first.
	order := make([]int, len(cands))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		return cands[order[i]].length > cands[order[j]].length
	})

	// Bucket by 1° angle bin so the pair test only compares near-parallel lines.
	bins := make(map[int][]int)
	for i, c := range cands {
		bins[int(c.angle)] = append(bins[int(c.angle)], i)
	}
	tolBins := int(math.Ceil(opts.AngleTolDeg)) + 1

	var pws []pairWall
	for _, ai := range order {
		A := cands[ai]
		if A.consumed {
			continue
		}
		bestOverlap := 0.0
		bestB := -1
		var bestT0, bestT1, bestD float64
		ab := int(A.angle)
		for db := -tolBins; db <= tolBins; db++ {
			bin := ((ab+db)%180 + 180) % 180
			for _, bi := range bins[bin] {
				if bi == ai {
					continue
				}
				B := cands[bi]
				if B.consumed {
					continue
				}
				if angleDiffDeg(A.angle, B.angle) > opts.AngleTolDeg {
					continue
				}
				bmx := (B.x1 + B.x2) / 2
				bmy := (B.y1 + B.y2) / 2
				d := math.Abs((bmx-A.x1)*A.uy - (bmy-A.y1)*A.ux)
				if d < opts.MinThickness || d > opts.MaxThickness {
					continue
				}
				tb1 := (B.x1-A.x1)*A.ux + (B.y1-A.y1)*A.uy
				tb2 := (B.x2-A.x1)*A.ux + (B.y2-A.y1)*A.uy
				bmin := math.Min(tb1, tb2)
				bmax := math.Max(tb1, tb2)
				t0 := math.Max(0, bmin)
				t1 := math.Min(A.length, bmax)
				overlap := t1 - t0
				if overlap <= 0 || overlap < 0.5*math.Min(A.length, B.length) {
					continue
				}
				if overlap > bestOverlap {
					bestOverlap = overlap
					bestB = bi
					bestT0, bestT1, bestD = t0, t1, d
				}
			}
		}
		if bestB < 0 {
			continue
		}
		B := cands[bestB]
		A.consumed = true
		B.consumed = true

		// Signed perpendicular offset of B from A's line; midline sits at half of it.
		bmx := (B.x1 + B.x2) / 2
		bmy := (B.y1 + B.y2) / 2
		signed := (bmx-A.x1)*A.uy - (bmy-A.y1)*A.ux
		half := 0.5 * signed
		mx1 := A.x1 + bestT0*A.ux + half*A.uy
		my1 := A.y1 + bestT0*A.uy - half*A.ux
		mx2 := A.x1 + bestT1*A.ux + half*A.uy
		my2 := A.y1 + bestT1*A.uy - half*A.ux
		layer := A.layer
		if B.length > A.length {
			layer = B.layer
		}
		pws = append(pws, pairWall{
			x1: mx1, y1: my1, x2: mx2, y2: my2,
			thickness: bestD, ux: A.ux, uy: A.uy,
			length: math.Hypot(mx2-mx1, my2-my1), layer: layer,
		})
	}

	return mergeCollinear(pws, opts)
}

// mergeCollinear groups pair-walls that lie on the same line and merges runs
// whose end-to-start gap is within BridgeGap (bridging doors/windows).
func mergeCollinear(pws []pairWall, opts WallSegmentsOptions) []XMLWall {
	thickTol := opts.MergeGap
	if thickTol < 0.02 {
		thickTol = 0.02
	}

	merged := make([]bool, len(pws))
	var walls []XMLWall
	for i := range pws {
		if merged[i] {
			continue
		}
		base := pws[i]
		group := []int{i}
		merged[i] = true
		baseAngle := canonicalAngle(base.ux, base.uy)
		for j := i + 1; j < len(pws); j++ {
			if merged[j] {
				continue
			}
			c := pws[j]
			if angleDiffDeg(baseAngle, canonicalAngle(c.ux, c.uy)) > opts.AngleTolDeg {
				continue
			}
			cmx := (c.x1 + c.x2) / 2
			cmy := (c.y1 + c.y2) / 2
			if math.Abs((cmx-base.x1)*base.uy-(cmy-base.y1)*base.ux) > opts.MergeGap {
				continue
			}
			if math.Abs(c.thickness-base.thickness) > thickTol {
				continue
			}
			group = append(group, j)
			merged[j] = true
		}
		walls = append(walls, mergeGroup(base, group, pws, opts)...)
	}
	return walls
}

// mergeGroup projects a group of collinear pair-walls onto the base line, sorts
// them, and emits one wall per run separated by more than BridgeGap. Thickness
// and perpendicular offset are length-weighted averages.
func mergeGroup(base pairWall, group []int, pws []pairWall, opts WallSegmentsOptions) []XMLWall {
	ux, uy := base.ux, base.uy
	ox, oy := base.x1, base.y1

	type iv struct {
		t0, t1    float64
		thickness float64
		weight    float64
		perp      float64
		layer     string
	}
	ivs := make([]iv, 0, len(group))
	for _, gi := range group {
		w := pws[gi]
		t1 := (w.x1-ox)*ux + (w.y1-oy)*uy
		t2 := (w.x2-ox)*ux + (w.y2-oy)*uy
		if t2 < t1 {
			t1, t2 = t2, t1
		}
		wmx := (w.x1 + w.x2) / 2
		wmy := (w.y1 + w.y2) / 2
		perp := (wmx-ox)*uy - (wmy-oy)*ux
		ivs = append(ivs, iv{t0: t1, t1: t2, thickness: w.thickness, weight: w.length, perp: perp, layer: w.layer})
	}
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].t0 < ivs[j].t0 })

	var out []XMLWall
	var (
		ct0, ct1  float64
		cWeight   float64
		cThickW   float64
		cPerpW    float64
		cLayer    string
		cLayerLen float64
		active    bool
	)
	addIv := func(v iv) {
		if !active {
			ct0, ct1 = v.t0, v.t1
			cWeight = v.weight
			cThickW = v.thickness * v.weight
			cPerpW = v.perp * v.weight
			cLayer = v.layer
			cLayerLen = v.weight
			active = true
			return
		}
		if v.t1 > ct1 {
			ct1 = v.t1
		}
		cWeight += v.weight
		cThickW += v.thickness * v.weight
		cPerpW += v.perp * v.weight
		if v.weight > cLayerLen {
			cLayer = v.layer
			cLayerLen = v.weight
		}
	}
	flush := func() {
		if !active {
			return
		}
		active = false
		avgPerp, avgThick := 0.0, 0.0
		if cWeight > 0 {
			avgPerp = cPerpW / cWeight
			avgThick = cThickW / cWeight
		}
		// Point on the merged centerline at param t with perpendicular offset:
		// P = O + t*u + perp*(uy, -ux).
		x1 := ox + ct0*ux + avgPerp*uy
		y1 := oy + ct0*uy - avgPerp*ux
		x2 := ox + ct1*ux + avgPerp*uy
		y2 := oy + ct1*uy - avgPerp*ux
		L := math.Hypot(x2-x1, y2-y1)
		if L < 0.05 {
			return
		}
		out = append(out, XMLWall{
			Layer:      cLayer,
			X1:         round6(x1),
			Y1:         round6(y1),
			X2:         round6(x2),
			Y2:         round6(y2),
			Thickness:  round6(avgThick),
			Length:     round6(L),
			Confidence: "paired",
		})
	}

	for _, v := range ivs {
		if active && v.t0 > ct1+opts.BridgeGap {
			flush()
		}
		addIv(v)
	}
	flush()
	return out
}

// canonicalAngle returns the orientation of a direction folded to [0,180) degrees
// (a line and its reverse share an angle).
func canonicalAngle(dx, dy float64) float64 {
	a := math.Atan2(dy, dx) * 180 / math.Pi
	if a < 0 {
		a += 180
	}
	if a >= 180 {
		a -= 180
	}
	return a
}

// angleDiffDeg returns the smallest angle between two [0,180) orientations (<=90).
func angleDiffDeg(a, b float64) float64 {
	d := math.Abs(a - b)
	if d > 90 {
		d = 180 - d
	}
	return d
}

func round6(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}
