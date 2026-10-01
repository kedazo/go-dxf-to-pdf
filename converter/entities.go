package converter

import (
	"math"
	"strings"

	dxf "github.com/kedazo/dxf-go"
)



// drawCtx is the state inherited from enclosing INSERTs while walking
// entities: the block-local → world transform plus the INSERT's resolved
// style, which entities with ByBlock color/lineweight and entities on layer
// "0" take over.
type drawCtx struct {
	m     affine
	depth int
	layer string  // effective layer for block entities on layer "0" ("" = top level)
	color RGB     // ByBlock color
	lw    float64 // ByBlock line weight (mm)
	ltBlock string // ByBlock line type name

	filter   *layerFilter // --layers selection (nil = all)
	selected bool         // an enclosing INSERT is on a selected layer

	hiddenLayers map[string]layerHide // layers that don't plot
	vpFrozen     map[string]bool      // layers frozen in the current viewport
	vp           *viewportSource      // what VIEWPORTs show (nil = don't draw viewports)
	ltScale      float64              // extra line type scale (0 = 1), for $PSLTSCALE
}

// layerHide is why a layer doesn't plot.
type layerHide uint8

const (
	layerFrozen layerHide = 1 << iota // hides everything on it, block content included
	layerNoPlot                       // hides what is drawn on it
)

// hiddenLayersOf returns the frozen and non-plotting layers (and Defpoints,
// which never plots).
func hiddenLayersOf(layers map[string]dxf.Layer) map[string]layerHide {
	hidden := map[string]layerHide{}
	for name, l := range layers {
		var h layerHide
		if l.Flags&1 != 0 {
			h |= layerFrozen
		}
		if !l.IsLayerPlotted || strings.EqualFold(name, "Defpoints") {
			h |= layerNoPlot
		}
		if h != 0 {
			hidden[name] = h
		}
	}
	return hidden
}

// hidden reports whether ent doesn't plot: it is on a frozen layer (also in
// the current viewport) or, unless it's an INSERT whose content may be on
// other layers, on a non-plotting one. Layers selected by name with
// --layers are shown anyway (except where a viewport freezes them).
func (ctx drawCtx) hidden(ent dxf.Entity) bool {
	if len(ctx.hiddenLayers) == 0 && len(ctx.vpFrozen) == 0 {
		return false
	}
	layer := ctx.effectiveLayer(ent)
	if ctx.vpFrozen[layer] {
		return true
	}
	h := ctx.hiddenLayers[layer]
	if h != 0 && ctx.filter != nil && ctx.filter.match(layer) {
		return false // asked for by name with --layers
	}
	if _, isInsert := ent.(*dxf.Insert); isInsert {
		return h&layerFrozen != 0
	}
	return h != 0
}

// maxBlockDepth guards against runaway (or cyclic) block nesting.
const maxBlockDepth = 10

var topCtx = drawCtx{m: identityAffine, lw: defaultLineWidthMM}

// filtered returns the top-level context for a layer selection.
func filtered(f *layerFilter) drawCtx {
	ctx := topCtx
	ctx.filter = f
	return ctx
}

// plotted returns the top-level context for a layer selection that also
// leaves out the layers that don't plot.
func plotted(f *layerFilter, layers map[string]dxf.Layer) drawCtx {
	ctx := filtered(f)
	ctx.hiddenLayers = hiddenLayersOf(layers)
	return ctx
}

// keeps reports whether ent passes the layer selection: it is on a selected
// layer itself, or it is part of a block inserted on one. INSERTs are always
// walked, so block content on selected (pen sub)layers shows up even when the
// INSERT sits on another layer.
func (ctx drawCtx) keeps(ent dxf.Entity) bool {
	return ctx.selected || ctx.filter.match(ctx.effectiveLayer(ent))
}

// effectiveLayer returns the layer an entity behaves as being on: entities
// on layer "0" inside a block inherit the INSERT's layer.
func (ctx drawCtx) effectiveLayer(ent dxf.Entity) string {
	if l := ent.Layer(); l != "0" || ctx.layer == "" {
		return l
	}
	return ctx.layer
}

// child returns the context for the entities of a block inserted by ins
// with instance transform local.
func (ctx drawCtx) child(ins dxf.Entity, local affine, layers map[string]dxf.Layer) drawCtx {
	c := ctx.inner(ins, local)
	c.color, c.lw = resolveStyle(ins, layers, ctx)
	c.ltBlock = ctx.lineTypeName(ins, layers)
	return c
}

// inner is child without the style: transform, nesting and layer selection
// only (enough for extents).
func (ctx drawCtx) inner(ins dxf.Entity, local affine) drawCtx {
	c := ctx
	c.m = ctx.m.mul(local)
	c.depth = ctx.depth + 1
	c.layer = ctx.effectiveLayer(ins)
	c.selected = ctx.keeps(ins)
	return c
}

// resolveStyle resolves an entity's color and line weight: true color wins,
// then ACI; ByLayer uses the (effective) layer, ByBlock the enclosing INSERT.
func resolveStyle(entity dxf.Entity, layers map[string]dxf.Layer, ctx drawCtx) (RGB, float64) {
	layer, hasLayer := layers[ctx.effectiveLayer(entity)]

	var rgb RGB
	entColor := entity.Color()
	switch {
	case entity.HasColor24Bit():
		rgb = trueColorRGB(entity.Color24Bit())
	case entColor == dxf.ByLayer():
		if hasLayer && layer.HasColor24Bit {
			rgb = trueColorRGB(layer.Color24Bit)
		} else if hasLayer {
			// A negative layer color only means "layer off"; keep the hue.
			idx := int16(layer.Color)
			if idx < 0 {
				idx = -idx
			}
			rgb = ACIToRGB(idx)
		}
	case entColor == dxf.ByBlock():
		rgb = ctx.color
	default:
		rgb = ACIToRGB(int16(entColor))
	}

	return rgb, ResolveLineWeight(entity.LineWeight(), layer.LineWeight, hasLayer, ctx.lw)
}

// trueColorRGB splits a 0xRRGGBB true color.
func trueColorRGB(c int) RGB {
	return RGB{uint8(c >> 16), uint8(c >> 8), uint8(c)}
}

// ComputeBoundingBox computes the bounding box of all entities in world coordinates.
// Entities inside INSERT blocks are mapped through the accumulated insert
// transform so the bbox captures their actual placement.
func ComputeBoundingBox(entities []dxf.Entity, blocks map[string]*dxf.Block) BBox {
	return selectionBBox(entities, blocks, nil)
}

// selectionBBox is the bounding box of the entities passing the layer
// selection f (nil = all).
func selectionBBox(entities []dxf.Entity, blocks map[string]*dxf.Block, f *layerFilter) BBox {
	return plottedBBox(entities, blocks, f, nil)
}

// plottedBBox is selectionBBox leaving out the layers that don't plot.
func plottedBBox(entities []dxf.Entity, blocks map[string]*dxf.Block, f *layerFilter, layers map[string]dxf.Layer) BBox {
	bb := NewBBox()
	ctx := plotted(f, layers)
	for _, ent := range entities {
		expandBBoxForEntity(&bb, ent, blocks, ctx)
	}
	return bb
}

// entityBoxes returns the world bbox of every top-level entity (of its parts
// passing the layer selection f). Entities without a known extent get an
// empty bbox (see cullEntities).
func entityBoxes(entities []dxf.Entity, blocks map[string]*dxf.Block, f *layerFilter, layers map[string]dxf.Layer) []BBox {
	boxes := make([]BBox, len(entities))
	ctx := plotted(f, layers)
	for i, ent := range entities {
		boxes[i] = NewBBox()
		expandBBoxForEntity(&boxes[i], ent, blocks, ctx)
	}
	return boxes
}

// cullEntities returns the entities whose bbox intersects box. Entities
// without a known extent are kept, so culling never drops geometry.
func cullEntities(entities []dxf.Entity, boxes []BBox, box BBox) []dxf.Entity {
	out := make([]dxf.Entity, 0, len(entities))
	for i, ent := range entities {
		b := boxes[i]
		if b.MinX > b.MaxX || (b.MaxX >= box.MinX && b.MinX <= box.MaxX && b.MaxY >= box.MinY && b.MinY <= box.MaxY) {
			out = append(out, ent)
		}
	}
	return out
}

// curve geometry shared by rendering and bbox computation

// circleArcAxes returns the world center and conjugate semi-axes of a circle
// (radius r around local cx, cy) under m.
func circleArcAxes(m affine, cx, cy, r float64) (wcx, wcy, ux, uy, vx, vy float64) {
	wcx, wcy = m.apply(cx, cy)
	ux, uy = m.applyVec(r, 0)
	vx, vy = m.applyVec(0, r)
	return
}

// arcParams returns the CCW parameter range of an ARC (angles in degrees).
func arcParams(startDeg, endDeg float64) (t0, t1 float64) {
	return ccwRange(startDeg*math.Pi/180, endDeg*math.Pi/180)
}

// ccwRange returns the CCW parameter range from t0 to t1 (radians), with
// t1 in (t0, t0+2π]: equal angles mean a full turn. Non-finite angles give
// an empty range.
func ccwRange(t0, t1 float64) (float64, float64) {
	if math.IsNaN(t0) || math.IsInf(t0, 0) || math.IsNaN(t1) || math.IsInf(t1, 0) {
		return 0, 0
	}
	sweep := math.Mod(t1-t0, 2*math.Pi)
	if sweep <= 0 {
		sweep += 2 * math.Pi
	}
	t0 = math.Mod(t0, 2*math.Pi)
	return t0, t0 + sweep
}

// ellipseAxes returns the local conjugate semi-axes and parameter range of an
// ELLIPSE. The minor axis is normal × major (scaled by the ratio), so ellipses
// with a negative normal run the other way round.
func ellipseAxes(e *dxf.Ellipse) (ux, uy, vx, vy, t0, t1 float64) {
	n := e.Normal
	if n.X == 0 && n.Y == 0 && n.Z == 0 {
		n.Z = 1
	}
	mj := e.MajorAxis
	ux, uy = mj.X, mj.Y
	vx = e.MinorAxisRatio * (n.Y*mj.Z - n.Z*mj.Y)
	vy = e.MinorAxisRatio * (n.Z*mj.X - n.X*mj.Z)

	t0, t1 = e.StartAngle, e.EndAngle
	if (t0 == 0 && t1 == 0) || math.Abs(t1-t0-2*math.Pi) < 1e-9 {
		return ux, uy, vx, vy, 0, 2 * math.Pi
	}
	t0, t1 = ccwRange(t0, t1)
	return ux, uy, vx, vy, t0, t1
}

// polyline2DAffine returns the OCS transform of a POLYLINE: 2D polylines
// live in their OCS, 3D polylines and meshes in world coordinates.
func polyline2DAffine(p *dxf.Polyline) affine {
	if p.Is3DPolyline() || p.Is3DPolygonMesh() || p.IsPolyfaceMesh() {
		return identityAffine
	}
	return ocsAffine(p.Normal, p.Location.Z)
}

func expandEllipticArc(bb *BBox, cx, cy, ux, uy, vx, vy, t0, t1 float64) {
	if t1-t0 >= 2*math.Pi-1e-9 {
		hw, hh := math.Hypot(ux, vx), math.Hypot(uy, vy)
		bb.Expand(cx-hw, cy-hh)
		bb.Expand(cx+hw, cy+hh)
		return
	}
	for _, p := range ellipticArcPoints(cx, cy, ux, uy, vx, vy, t0, t1, 32) {
		bb.Expand(p[0], p[1])
	}
}

// expandBBoxForEntity expands the bounding box with the given entity, mapped
// through ctx.m (block-local → world), if it passes the layer selection.
func expandBBoxForEntity(bb *BBox, ent dxf.Entity, blocks map[string]*dxf.Block, ctx drawCtx) {
	if ctx.depth > maxBlockDepth || !ent.IsVisible() || ctx.hidden(ent) {
		return
	}
	if _, isInsert := ent.(*dxf.Insert); !isInsert && !ctx.keeps(ent) {
		return
	}
	m := ctx.m

	expand := func(t affine, x, y float64) {
		bb.Expand(t.apply(x, y))
	}

	switch e := ent.(type) {
	case *dxf.Line:
		expand(m, e.P1.X, e.P1.Y)
		expand(m, e.P2.X, e.P2.Y)
	case *dxf.Circle:
		cx, cy, ux, uy, vx, vy := circleArcAxes(m.mul(ocsAffine(e.Normal, e.Center.Z)), e.Center.X, e.Center.Y, e.Radius)
		expandEllipticArc(bb, cx, cy, ux, uy, vx, vy, 0, 2*math.Pi)
	case *dxf.Arc:
		cx, cy, ux, uy, vx, vy := circleArcAxes(m.mul(ocsAffine(e.Normal, e.Center.Z)), e.Center.X, e.Center.Y, e.Radius)
		t0, t1 := arcParams(e.StartAngle, e.EndAngle)
		expandEllipticArc(bb, cx, cy, ux, uy, vx, vy, t0, t1)
	case *dxf.Ellipse:
		lux, luy, lvx, lvy, t0, t1 := ellipseAxes(e)
		cx, cy := m.apply(e.Center.X, e.Center.Y)
		ux, uy := m.applyVec(lux, luy)
		vx, vy := m.applyVec(lvx, lvy)
		expandEllipticArc(bb, cx, cy, ux, uy, vx, vy, t0, t1)
	case *dxf.LWPolyline:
		om := m.mul(ocsAffine(e.ExtrusionDirection, e.Elevation()))
		verts := e.Vertices
		expandWideOutline(expand, om, lwPolylineEdges(e), e.IsClosed())
		for i, v := range verts {
			expand(om, v.X, v.Y)
			if j := i + 1; j < len(verts) || (e.IsClosed() && len(verts) > 1) {
				w := verts[j%len(verts)]
				expandBulge(bb, om, v.X, v.Y, w.X, w.Y, v.Bulge)
			}
		}
	case *dxf.Polyline:
		// Use the edges, not the raw vertices: polyface face records sit at
		// (0,0,0) and would drag the bbox to the origin.
		om := m.mul(polyline2DAffine(e))
		edges := polylineEdges(e)
		expandWideOutline(expand, om, edges, !e.IsPolyfaceMesh() && !e.Is3DPolygonMesh() && e.IsClosed())
		for _, edge := range edges {
			expand(om, edge.X1, edge.Y1)
			expand(om, edge.X2, edge.Y2)
			expandBulge(bb, om, edge.X1, edge.Y1, edge.X2, edge.Y2, edge.Bulge)
		}
	case *dxf.Spline:
		for _, cp := range e.ControlPoints {
			expand(m, cp.Point.X, cp.Point.Y)
		}
	case *dxf.Text:
		expandTextLine(bb, m.mul(ocsAffine(e.Normal, e.Location.Z)), e.PlainText(), e.Height, e.RelativeXScaleFactor,
			resolveTextAnchor(e.Location, e.SecondAlignmentPoint, e.HorizontalTextJustification, e.VerticalTextJustification, e.Rotation))
	case *dxf.MText:
		expandMText(bb, m, e)
	case *dxf.ModelPoint:
		expand(m, e.Location.X, e.Location.Y)
	case *dxf.Leader:
		for _, v := range e.Vertices {
			expand(m, v.X, v.Y)
		}
	case *dxf.Table:
		expandBBoxForEntity(bb, tableInsert(e), blocks, ctx)
	case *dxf.MLeader:
		// The content block is left out: resolving it takes the drawing.
		paths, text, _ := mleaderParts(e, nil, nil)
		for _, path := range paths {
			for _, p := range path {
				expand(m, p.X, p.Y)
			}
		}
		if text != nil {
			expandMText(bb, m, text)
		}
	case *dxf.Wipeout:
		for _, p := range wipeoutPolygon(e) {
			expand(m, p[0], p[1])
		}
	case *dxf.Viewport:
		expand(m, e.Center.X-e.Width/2, e.Center.Y-e.Height/2)
		expand(m, e.Center.X+e.Width/2, e.Center.Y+e.Height/2)
	case *dxf.Image:
		loc, u, v, s := e.Location(), e.UVector(), e.VVector(), e.ImageSize()
		for _, f := range [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
			expand(m, loc.X+u.X*s.X*f[0]+v.X*s.Y*f[1], loc.Y+u.Y*s.X*f[0]+v.Y*s.Y*f[1])
		}
	case *dxf.Solid:
		om := m.mul(ocsAffine(e.ExtrusionDirection, e.FirstCorner.Z))
		for _, p := range []dxf.Point{e.FirstCorner, e.SecondCorner, e.ThirdCorner, e.FourthCorner} {
			expand(om, p.X, p.Y)
		}
	case *dxf.Trace:
		om := m.mul(ocsAffine(e.ExtrusionDirection, e.FirstCorner.Z))
		for _, p := range []dxf.Point{e.FirstCorner, e.SecondCorner, e.ThirdCorner, e.FourthCorner} {
			expand(om, p.X, p.Y)
		}
	case *dxf.Mesh:
		for _, v := range e.Vertices {
			expand(m, v.X, v.Y)
		}
	case *dxf.MLine:
		lines, _ := mlineElements(e)
		for _, line := range lines {
			for _, p := range line {
				expand(m, p.X, p.Y)
			}
		}
	case *dxf.AttributeDefinition:
		if text, ok := attdefText(e, ctx.depth > 0); ok {
			if e.IsMultiline() && ctx.depth > 0 && e.MText.Text != "" {
				expandMText(bb, m, &e.MText)
			} else {
				expandTextLine(bb, m.mul(ocsAffine(e.Normal, e.Location.Z)), text, e.TextHeight, e.RelativeXScaleFactor,
					resolveTextAnchor(e.Location, e.SecondAlignmentPoint, e.HorizontalTextJustification, e.VerticalTextJustification, e.Rotation))
			}
		}
	case *dxf.Insert:
		if blk, ok := blocks[e.Name]; ok {
			for _, local := range insertInstances(e, blk.BasePoint) {
				cctx := ctx.inner(e, local)
				for _, be := range blk.Entities {
					expandBBoxForEntity(bb, be, blocks, cctx)
				}
			}
		}
		actx := ctx.inner(e, identityAffine)
		for i := range e.Attributes {
			expandBBoxForEntity(bb, &e.Attributes[i], blocks, actx)
		}
	case *dxf.Attribute:
		switch {
		case e.IsInvisible():
		case e.MTextFlag&dxf.MTextFlagMultilineAttribute != 0 && e.MText.Text != "":
			expandMText(bb, m, &e.MText)
		default:
			expandTextLine(bb, m.mul(ocsAffine(e.Normal, e.Location.Z)), e.PlainText(), e.TextHeight, e.RelativeXScaleFactor,
				resolveTextAnchor(e.Location, e.SecondAlignmentPoint, e.HorizontalTextJustification, e.VerticalTextJustification, e.Rotation))
		}
	case *dxf.Hatch:
		om := m.mul(ocsAffine(e.ExtrusionDirection, e.Elevation()))
		for _, poly := range hatchPolygons(e, 0) {
			for _, v := range poly {
				expand(om, v[0], v[1])
			}
		}

	default:
		// DIMENSION entities reference an anonymous block containing their
		// geometry in world coordinates (relative to the dimension's space).
		if dim, ok := ent.(dxf.Dimension); ok {
			if blk, ok := blocks[dim.BlockName()]; ok {
				dctx := ctx.inner(ent, identityAffine)
				dctx.selected = true // the dimension itself passed the selection
				for _, be := range blk.Entities {
					expandBBoxForEntity(bb, be, blocks, dctx)
				}
			}
		}
	}
}

// RenderEntities renders all DXF entities to the PDF renderer.
func RenderEntities(r *Renderer, entities []dxf.Entity, layers map[string]dxf.Layer,
	blocks map[string]*dxf.Block, sel *layerFilter) {

	renderAll(r, entities, layers, blocks, plotted(sel, layers))
}

func renderAll(r *Renderer, entities []dxf.Entity, layers map[string]dxf.Layer, blocks map[string]*dxf.Block, ctx drawCtx) {
	for _, ent := range entities {
		renderEntity(r, ent, layers, blocks, ctx)
	}
}

func renderEntity(r *Renderer, ent dxf.Entity, layers map[string]dxf.Layer,
	blocks map[string]*dxf.Block, ctx drawCtx) {

	// A viewport's layer only governs its border: the view itself plots
	// even when that layer doesn't.
	if v, ok := ent.(*dxf.Viewport); ok {
		if v.IsVisible() && ctx.depth <= maxBlockDepth {
			renderViewport(r, v, layers, blocks, ctx)
		}
		return
	}
	if ctx.depth > maxBlockDepth || !ent.IsVisible() || ctx.hidden(ent) {
		return
	}

	// Layer selection; INSERTs are always walked (see keeps).
	if _, isInsert := ent.(*dxf.Insert); !isInsert && !ctx.keeps(ent) {
		return
	}

	rgb, lw := resolveStyle(ent, layers, ctx)
	m := ctx.m
	if pattern := r.lineTypes.pattern(ctx.lineTypeName(ent, layers)); pattern != nil {
		// Linetypes scale with the drawing, the entity and the block it is in.
		ltScale := ent.LineTypeScale()
		if ltScale <= 0 {
			ltScale = 1
		}
		if ctx.ltScale > 0 { // $PSLTSCALE: patterns sized in paper units in viewports
			ltScale *= ctx.ltScale
		}
		offset, dashes := dashPattern(pattern, r.lineTypes.scale*ltScale*m.linearScale()*r.transform.Scale)
		r.SetDashedStyle(rgb, lw, offset, dashes)
		r.BreakPath() // each entity starts its own pattern
	} else {
		r.SetStyle(rgb, lw)
	}

	switch e := ent.(type) {
	case *dxf.Line:
		x1, y1 := m.apply(e.P1.X, e.P1.Y)
		x2, y2 := m.apply(e.P2.X, e.P2.Y)
		r.DrawLine(x1, y1, x2, y2)

	case *dxf.Circle:
		cx, cy, ux, uy, vx, vy := circleArcAxes(m.mul(ocsAffine(e.Normal, e.Center.Z)), e.Center.X, e.Center.Y, e.Radius)
		r.DrawEllipticArc(cx, cy, ux, uy, vx, vy, 0, 2*math.Pi)

	case *dxf.Arc:
		cx, cy, ux, uy, vx, vy := circleArcAxes(m.mul(ocsAffine(e.Normal, e.Center.Z)), e.Center.X, e.Center.Y, e.Radius)
		t0, t1 := arcParams(e.StartAngle, e.EndAngle)
		r.DrawEllipticArc(cx, cy, ux, uy, vx, vy, t0, t1)

	case *dxf.Ellipse:
		lux, luy, lvx, lvy, t0, t1 := ellipseAxes(e)
		cx, cy := m.apply(e.Center.X, e.Center.Y)
		ux, uy := m.applyVec(lux, luy)
		vx, vy := m.applyVec(lvx, lvy)
		r.DrawEllipticArc(cx, cy, ux, uy, vx, vy, t0, t1)

	case *dxf.LWPolyline:
		renderLWPolyline(r, e, m.mul(ocsAffine(e.ExtrusionDirection, e.Elevation())), rgb)

	case *dxf.Polyline:
		simple := !e.IsPolyfaceMesh() && !e.Is3DPolygonMesh()
		renderPolylineEdges(r, polylineEdges(e), simple && e.IsClosed(), m.mul(polyline2DAffine(e)), rgb)

	case *dxf.Spline:
		cps := make([][2]float64, len(e.ControlPoints))
		for i, cp := range e.ControlPoints {
			x, y := m.apply(cp.Point.X, cp.Point.Y)
			cps[i] = [2]float64{x, y}
		}
		r.DrawSpline(cps, e.DegreeOfCurve, e.KnotValues)

	case *dxf.Text:
		renderTextLine(r, m.mul(ocsAffine(e.Normal, e.Location.Z)), e.PlainText(), e.Height,
			resolveTextAnchor(e.Location, e.SecondAlignmentPoint,
				e.HorizontalTextJustification, e.VerticalTextJustification, e.Rotation),
			r.lookFor(e.TextStyleName, e.RelativeXScaleFactor, e.ObliqueAngle))

	case *dxf.Attribute:
		if e.IsInvisible() {
			return
		}
		if e.MTextFlag&dxf.MTextFlagMultilineAttribute != 0 && e.MText.Text != "" {
			renderMText(r, &e.MText, m)
			return
		}
		renderTextLine(r, m.mul(ocsAffine(e.Normal, e.Location.Z)), e.PlainText(), e.TextHeight,
			resolveTextAnchor(e.Location, e.SecondAlignmentPoint,
				e.HorizontalTextJustification, e.VerticalTextJustification, e.Rotation),
			r.lookFor(e.TextStyleName, e.RelativeXScaleFactor, e.ObliqueAngle))

	case *dxf.MText:
		renderMText(r, e, m)

	case *dxf.ModelPoint:
		r.DrawPoint(m.apply(e.Location.X, e.Location.Y))

	case *dxf.Leader:
		renderLeader(r, e, layers, blocks, ctx, rgb)

	case *dxf.MLeader:
		renderMLeader(r, e, layers, blocks, ctx, rgb, lw)

	case *dxf.Table:
		renderEntity(r, tableInsert(e), layers, blocks, ctx)

	case *dxf.Image:
		renderImage(r, e, m)

	case *dxf.Wipeout:
		// Masks what was drawn before it with the paper color.
		poly := wipeoutPolygon(e)
		for i, p := range poly {
			poly[i][0], poly[i][1] = m.apply(p[0], p[1])
		}
		r.FillPolygons([][][2]float64{poly}, RGB{255, 255, 255})
		if r.wipeoutFrame { // the outline, in the wipeout's own style
			r.DrawPolyline(poly, true)
		}

	case *dxf.Solid:
		om := m.mul(ocsAffine(e.ExtrusionDirection, e.FirstCorner.Z))
		x1, y1 := om.apply(e.FirstCorner.X, e.FirstCorner.Y)
		x2, y2 := om.apply(e.SecondCorner.X, e.SecondCorner.Y)
		x3, y3 := om.apply(e.ThirdCorner.X, e.ThirdCorner.Y)
		x4, y4 := om.apply(e.FourthCorner.X, e.FourthCorner.Y)
		r.SetStyle(rgb, 0)
		r.SetFillColor(rgb)
		r.DrawSolid(x1, y1, x2, y2, x3, y3, x4, y4)

	case *dxf.Trace: // a filled quadrilateral, like SOLID
		om := m.mul(ocsAffine(e.ExtrusionDirection, e.FirstCorner.Z))
		x1, y1 := om.apply(e.FirstCorner.X, e.FirstCorner.Y)
		x2, y2 := om.apply(e.SecondCorner.X, e.SecondCorner.Y)
		x3, y3 := om.apply(e.ThirdCorner.X, e.ThirdCorner.Y)
		x4, y4 := om.apply(e.FourthCorner.X, e.FourthCorner.Y)
		r.SetStyle(rgb, 0)
		r.SetFillColor(rgb)
		r.DrawSolid(x1, y1, x2, y2, x3, y3, x4, y4)

	case *dxf.MLine:
		renderMLine(r, e, m)

	case *dxf.Mesh: // its edges, seen from above
		for _, edge := range meshEdges(e) {
			drawEdge(r, m, edge.X1, edge.Y1, edge.X2, edge.Y2, 0)
		}

	case *dxf.AttributeDefinition:
		text, ok := attdefText(e, ctx.depth > 0)
		if !ok {
			return
		}
		if e.IsMultiline() && ctx.depth > 0 && e.MText.Text != "" {
			renderMText(r, &e.MText, m)
			return
		}
		renderTextLine(r, m.mul(ocsAffine(e.Normal, e.Location.Z)), text, e.TextHeight,
			resolveTextAnchor(e.Location, e.SecondAlignmentPoint,
				e.HorizontalTextJustification, e.VerticalTextJustification, e.Rotation),
			r.lookFor(e.TextStyleName, e.RelativeXScaleFactor, e.ObliqueAngle))

	case *dxf.Hatch:
		if !enableHatch {
			break
		}
		// Boundaries and pattern lines are in the hatch's OCS.
		om := m.mul(ocsAffine(e.ExtrusionDirection, e.Elevation()))
		// Paper-size based tolerances: curves flattened to within 0.05 mm,
		// dots drawn ~0.15 mm long, whatever the scale.
		pageScale := r.transform.Scale * om.linearScale()
		tolerance, dotLen := 0.0, 0.15
		if pageScale > 0 {
			tolerance, dotLen = 0.05/pageScale, 0.15/pageScale
		}
		if e.SolidFill {
			polys := hatchPolygons(e, tolerance)
			world := make([][][2]float64, len(polys))
			for i, poly := range polys {
				world[i] = make([][2]float64, len(poly))
				for j, v := range poly {
					x, y := om.apply(v[0], v[1])
					world[i][j] = [2]float64{x, y}
				}
			}
			r.FillPolygons(world, hatchFillColor(e, rgb))
		} else if len(e.PatternLines) > 0 {
			r.SetStyle(rgb, 0.05) // thin lines for hatch fill
			lines := r.hatchLines(e, dotLen, tolerance)
			for _, seg := range lines {
				x1, y1 := om.apply(seg[0], seg[1])
				x2, y2 := om.apply(seg[2], seg[3])
				r.DrawLine(x1, y1, x2, y2)
			}
		}

	case *dxf.Insert:
		if blk, ok := blocks[e.Name]; ok {
			for _, local := range insertInstances(e, blk.BasePoint) {
				cctx := ctx.child(e, local, layers)
				for _, be := range blk.Entities {
					renderEntity(r, be, layers, blocks, cctx)
				}
			}
		}
		// ATTRIBs are stored in the INSERT's own coordinate space (already
		// placed), so they take the parent transform, not the block's.
		actx := ctx.child(e, identityAffine, layers)
		for i := range e.Attributes {
			renderEntity(r, &e.Attributes[i], layers, blocks, actx)
		}

	default:
		// DIMENSION entities reference an anonymous block containing their geometry.
		// The block entities are in world coordinates (basePoint=0,0) and may be on
		// sublayers (e.g. "Layer_Pen_No__1"), so the whole block is selected —
		// the dimension entity itself already passed the selection above.
		if dim, ok := ent.(dxf.Dimension); ok {
			if blk, ok := blocks[dim.BlockName()]; ok {
				dctx := ctx.child(ent, identityAffine, layers)
				dctx.selected = true
				for _, be := range blk.Entities {
					renderEntity(r, be, layers, blocks, dctx)
				}
			}
		}
	}
}

// renderTextLine draws a single-line TEXT/ATTRIB whose anchor is given in the
// entity's own coordinates, mapped through m.
func renderTextLine(r *Renderer, m affine, value string, height float64, a textAnchor, look textLook) {
	x, y, rot, hScale, mirrored := textFrame(m, a.X, a.Y, a.RotationDeg)
	hAlign := a.HAlign
	if mirrored {
		hAlign = 1 - hAlign
	}
	r.DrawText(x, y, value, height*hScale, rot, hAlign, a.VAlign, look)
}

func renderMText(r *Renderer, e *dxf.MText, m affine) {
	// Long MTEXT is split into 250-char group 3 chunks followed by the final
	// group 1 chunk; FormattedText joins them in that order.
	segments := ParseMText(e.FormattedText())

	x, y, rot, hScale, mirrored := textFrame(m, e.InsertionPoint.X, e.InsertionPoint.Y, mtextRotationDeg(e))
	// Absolute \H heights scale with the block like the initial height.
	for i := range segments {
		segments[i].Style.Height *= hScale
	}
	attach := int(e.AttachmentPoint)
	if mirrored && attach >= 1 && attach <= 9 {
		row, col := (attach-1)/3, (attach-1)%3
		attach = row*3 + (2 - col) + 1
	}
	// Lines wrap at the MTEXT's box width (0 = no box).
	box := 0.0
	if w := mtextBoxWidth(e); w > 0 {
		box = r.transform.Dist(w * hScale)
	}
	r.DrawMText(x, y, segments, e.InitialTextHeight*hScale, rot, attach, e.LineSpacingFactor, box, r.textStyleNamed(e.TextStyleName))
}

// expandBulge expands bb with the arc of a bulged polyline edge (local
// coordinates, mapped through m); straight edges add nothing.
func expandBulge(bb *BBox, m affine, x1, y1, x2, y2, bulge float64) {
	if math.Abs(bulge) <= 1e-10 || (x1 == x2 && y1 == y2) {
		return
	}
	lcx, lcy, radius, start, sweep := bulgeArc(x1, y1, x2, y2, bulge)
	cx, cy, ux, uy, vx, vy := circleArcAxes(m, lcx, lcy, radius)
	t0, t1 := start, start+sweep
	if t1 < t0 {
		t0, t1 = t1, t0
	}
	expandEllipticArc(bb, cx, cy, ux, uy, vx, vy, t0, t1)
}

// drawEdge draws a straight or bulged (arc) polyline edge given in local
// coordinates, mapped through m.
func drawEdge(r *Renderer, m affine, x1, y1, x2, y2, bulge float64) {
	if math.Abs(bulge) > 1e-10 && (x1 != x2 || y1 != y2) {
		lcx, lcy, radius, start, sweep := bulgeArc(x1, y1, x2, y2, bulge)
		cx, cy, ux, uy, vx, vy := circleArcAxes(m, lcx, lcy, radius)
		r.DrawEllipticArc(cx, cy, ux, uy, vx, vy, start, start+sweep)
		return
	}
	wx1, wy1 := m.apply(x1, y1)
	wx2, wy2 := m.apply(x2, y2)
	r.DrawLine(wx1, wy1, wx2, wy2)
}

func renderLWPolyline(r *Renderer, e *dxf.LWPolyline, m affine, rgb RGB) {
	renderPolylineEdges(r, lwPolylineEdges(e), e.IsClosed(), m, rgb)
}


// stripMTextFormatting removes MText formatting codes and returns plain text.
// For styled rendering, use ParseMText instead.
func stripMTextFormatting(s string) string {
	segments := ParseMText(s)
	var result strings.Builder
	for _, seg := range segments {
		if seg.NewLine && result.Len() > 0 {
			result.WriteByte(' ')
		}
		result.WriteString(seg.Text)
	}
	return result.String()
}

