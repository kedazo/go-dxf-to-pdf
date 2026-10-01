package converter

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	dxf "github.com/kedazo/dxf-go"
)

// Paper space layouts. A layout is a sheet (paper size from its plot
// settings, in paper units 1:1) with paper space entities (title block,
// labels) and VIEWPORTs that show model space at some scale, clipped to the
// viewport's outline.

// LayoutInfo describes a paper space layout.
type LayoutInfo struct {
	Name          string
	Active        bool    // the layout the drawing was saved on ($TILEMODE 0)
	Width, Height float64 // paper size in mm
	Viewports     []ViewportInfo
}

// ViewportInfo describes a viewport showing model space on a layout.
type ViewportInfo struct {
	Scale        float64 // paper units per model unit
	TwistDeg     float64
	Clipped      bool // non-rectangular clip outline
	FrozenLayers int
}

// modelSpace returns the model space entities.
func modelSpace(entities []dxf.Entity) []dxf.Entity {
	out := make([]dxf.Entity, 0, len(entities))
	for _, e := range entities {
		if !e.IsInPaperSpace() {
			out = append(out, e)
		}
	}
	return out
}

// paperLayouts returns the paper space layouts in tab order.
func paperLayouts(d *dxf.Drawing) []*dxf.Layout {
	var out []*dxf.Layout
	for i := range d.Layouts {
		if !d.Layouts[i].IsModel() {
			out = append(out, &d.Layouts[i])
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TabOrder < out[j].TabOrder })
	return out
}

// activeLayout returns the layout whose entities are the paper space
// entities of the ENTITIES section (the one last shown), or nil.
func activeLayout(d *dxf.Drawing) *dxf.Layout {
	var paperSpace dxf.Handle
	for i := range d.BlockRecords {
		if strings.EqualFold(d.BlockRecords[i].Name, "*Paper_Space") {
			paperSpace = d.BlockRecords[i].Handle()
		}
	}
	layouts := paperLayouts(d)
	for _, l := range layouts {
		if paperSpace != 0 && l.BlockRecordHandle == paperSpace {
			return l
		}
	}
	if len(layouts) > 0 {
		return layouts[0]
	}
	return nil
}

// chooseLayout returns the layout to plot: the named one, "model" (nil),
// or by default the active layout if the drawing was saved showing a layout
// ($TILEMODE 0) that has viewports, unless model space options (scale,
// paper fitting, tiling, crop) ask for model space.
func chooseLayout(d *dxf.Drawing, name string, modelOptions bool) (*dxf.Layout, error) {
	l, err := savedLayout(d, name)
	if err != nil || name != "" || l == nil {
		return l, err
	}
	if modelOptions {
		fmt.Fprintf(os.Stderr, "Note: the drawing was saved on layout %q; plotting model space as a scale was given (--layout %q plots the sheet)\n", l.Name, l.Name)
		return nil, nil
	}
	fmt.Fprintf(os.Stderr, "Note: plotting layout %q, the sheet the drawing was saved on (--layout model and --scale for model space)\n", l.Name)
	return l, nil
}

func savedLayout(d *dxf.Drawing, name string) (*dxf.Layout, error) {
	switch {
	case strings.EqualFold(name, "model"):
		return nil, nil
	case name != "":
		if l := d.LayoutByName(name); l != nil && !l.IsModel() {
			return l, nil
		}
		var names []string
		for _, l := range paperLayouts(d) {
			names = append(names, fmt.Sprintf("%q", l.Name))
		}
		return nil, fmt.Errorf("no layout %q (layouts: model %s)", name, strings.Join(names, " "))
	}
	if d.Header.PreviousReleaseTileCompatability { // $TILEMODE 1: model space shown
		return nil, nil
	}
	l := activeLayout(d)
	if l == nil {
		return nil, nil
	}
	for _, e := range d.LayoutEntities(l) {
		if v, ok := e.(*dxf.Viewport); ok && !v.IsPaperSpaceView() && v.IsOn() {
			return l, nil
		}
	}
	return nil, nil
}

// layoutInfos describes the paper space layouts of d.
func layoutInfos(d *dxf.Drawing) []LayoutInfo {
	active := activeLayout(d)
	var out []LayoutInfo
	for _, l := range paperLayouts(d) {
		entities := d.LayoutEntities(l)
		if len(entities) == 0 {
			continue // an empty default layout
		}
		w, h := layoutPaper(l)
		info := LayoutInfo{Name: l.Name, Active: l == active && !d.Header.PreviousReleaseTileCompatability, Width: w, Height: h}
		for _, e := range entities {
			if v, ok := e.(*dxf.Viewport); ok && !v.IsPaperSpaceView() {
				info.Viewports = append(info.Viewports, ViewportInfo{
					Scale: v.Scale(), TwistDeg: v.TwistAngle, Clipped: v.IsNonRectangularClipping(),
					FrozenLayers: len(v.FrozenLayerHandles),
				})
			}
		}
		out = append(out, info)
	}
	return out
}

// layoutPaper returns a layout's sheet size in mm as plotted (turned by the
// plot rotation).
func layoutPaper(l *dxf.Layout) (w, h float64) {
	w, h = l.PaperWidth, l.PaperHeight
	if l.PlotRotation == 1 || l.PlotRotation == 3 {
		w, h = h, w
	}
	return w, h
}

// convertLayout plots a paper space layout on its sheet: paper units 1:1,
// with the viewports showing the model space.
func convertLayout(d *dxf.Drawing, l *dxf.Layout, outPath string, opts Options,
	newRenderer func(PaperSize, bool) *Renderer, layers map[string]dxf.Layer, blocks map[string]*dxf.Block,
	sel *layerFilter) (*Result, error) {

	unit := 1.0 // paper units → mm
	if l.PaperUnits == 0 {
		unit = 25.4
	}
	paperEntities := d.LayoutEntities(l)
	var sheet BBox
	w, h := layoutPaper(l)
	if mn, mx, ok := l.SheetRectangle(); ok {
		sheet = BBox{MinX: mn.X, MinY: mn.Y, MaxX: mx.X, MaxY: mx.Y}
	} else {
		// No plot settings: the extents of what's on the layout.
		sheet = plottedBBox(paperEntities, blocks, sel, layers)
		sheet.padFlat()
		if sheet.Width() <= 0 || sheet.Height() <= 0 {
			return nil, fmt.Errorf("layout %q is empty", l.Name)
		}
		w, h = sheet.Width()*unit, sheet.Height()*unit
	}
	paper := PaperSize{Name: l.Name, Width: w, Height: h}

	r := newRenderer(paper, false)
	r.SetTransform(NewTransform(sheet, unit, paper, 0, AlignTopLeft, false))

	model := modelSpace(d.Entities)
	ctx := plotted(sel, layers)
	ctx.vp = &viewportSource{
		drawing:   d,
		entities:  model,
		boxes:     entityBoxes(model, blocks, sel, layers),
		psLTScale: d.Header.ScaleLineTypesInPaperspace,
	}
	// Only the sheet plots (ArchiCAD leaves title blocks far off it).
	r.SetClipRect(0, 0, w, h)
	renderAll(r, paperEntities, layers, blocks, ctx)
	r.ClipEnd()

	if err := r.Save(outPath, opts.Format, opts.DPI, opts.Transparent); err != nil {
		return nil, fmt.Errorf("saving: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Layout %q: %.0f x %.0f mm\n", l.Name, w, h)
	return &Result{Pages: 1, BoundingBox: sheet, Units: "paper " + map[bool]string{true: "millimeters", false: "inches"}[unit == 1], UnitFactor: unit}, nil
}

// viewportSource is what viewports show: the model space.
type viewportSource struct {
	drawing   *dxf.Drawing
	entities  []dxf.Entity
	boxes     []BBox // model extents of entities, for culling
	psLTScale bool   // $PSLTSCALE: line type patterns in paper units
}

// affineOf takes the plan (x, y) part of a 3D matrix.
func affineOf(m dxf.Matrix) affine {
	p0 := m.TransformPoint(dxf.Point{})
	px := m.TransformPoint(dxf.Point{X: 1})
	py := m.TransformPoint(dxf.Point{Y: 1})
	return affine{a: px.X - p0.X, b: px.Y - p0.Y, c: py.X - p0.X, d: py.Y - p0.Y, e: p0.X, f: p0.Y}
}

// inverse returns the inverse of m (identity if m is singular).
func (m affine) inverse() affine {
	det := m.a*m.d - m.b*m.c
	if det == 0 {
		return identityAffine
	}
	a, b, c, d := m.d/det, -m.b/det, -m.c/det, m.a/det
	return affine{a: a, b: b, c: c, d: d, e: -(a*m.e + c*m.f), f: -(b*m.e + d*m.f)}
}

// renderViewport draws the model space through a viewport on the sheet.
func renderViewport(r *Renderer, v *dxf.Viewport, layers map[string]dxf.Layer, blocks map[string]*dxf.Block, ctx drawCtx) {
	src := ctx.vp
	if src == nil || v.IsPaperSpaceView() || !v.IsOn() || v.Scale() == 0 {
		return
	}
	// Outline on the sheet; curves flattened to ~0.05 mm on paper.
	tol := 0.05
	if s := r.transform.Scale * ctx.m.linearScale(); s > 0 {
		tol = 0.05 / s
	}
	outline, _ := v.ClipPolygon(tol)
	clip := make([][2]float64, len(outline))
	for i, p := range outline {
		x, y := ctx.m.apply(p.X, p.Y)
		clip[i] = [2]float64{r.transform.X(x), r.transform.Y(y)}
	}

	view := ctx.m.mul(affineOf(v.ModelToPaperMatrix()))
	vctx := filtered(ctx.filter)
	vctx.m = view
	vctx.hiddenLayers = ctx.hiddenLayers
	if src.drawing != nil {
		if frozen := v.FrozenLayers(src.drawing); len(frozen) > 0 {
			vctx.vpFrozen = make(map[string]bool, len(frozen))
			for _, name := range frozen {
				vctx.vpFrozen[name] = true
			}
		}
	}
	if src.psLTScale {
		vctx.ltScale = 1 / view.linearScale()
	}

	// Cull the model to what the outline can show.
	inv := view.inverse()
	shown := NewBBox()
	for _, p := range outline {
		x, y := ctx.m.apply(p.X, p.Y)
		shown.Expand(inv.apply(x, y))
	}

	r.SetClipPolygon(clip)
	for i, e := range src.entities {
		b := src.boxes[i]
		if b.MinX <= b.MaxX && (b.MaxX < shown.MinX || b.MinX > shown.MaxX || b.MaxY < shown.MinY || b.MinY > shown.MaxY) {
			continue
		}
		renderEntity(r, e, layers, blocks, vctx)
	}
	r.SetClipPolygon(nil)

	// Rectangular viewports plot their border (non-rectangular ones are
	// bordered by their clip boundary entity, drawn on its own).
	if !v.IsNonRectangularClipping() && ctx.keeps(v) && !ctx.hidden(v) {
		rgb, lw := resolveStyle(v, layers, ctx)
		r.SetStyle(rgb, lw)
		pts := make([][2]float64, len(outline))
		for i, p := range outline {
			pts[i][0], pts[i][1] = ctx.m.apply(p.X, p.Y)
		}
		r.DrawPolyline(pts, true)
	}
}

// pointInPolygon reports whether (x, y) lies inside poly (even-odd).
func pointInPolygon(x, y float64, poly [][2]float64) bool {
	in := false
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		a, b := poly[i], poly[j]
		if (a[1] > y) != (b[1] > y) && x < (b[0]-a[0])*(y-a[1])/(b[1]-a[1])+a[0] {
			in = !in
		}
	}
	return in
}

// polygonsIntersect reports whether two polygons overlap: a vertex of one
// lies in the other, or two edges cross.
func polygonsIntersect(a, b [][2]float64) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	if pointInPolygon(a[0][0], a[0][1], b) || pointInPolygon(b[0][0], b[0][1], a) {
		return true
	}
	cross := func(o, p, q [2]float64) float64 { return (p[0]-o[0])*(q[1]-o[1]) - (p[1]-o[1])*(q[0]-o[0]) }
	for i := range a {
		a0, a1 := a[i], a[(i+1)%len(a)]
		for j := range b {
			b0, b1 := b[j], b[(j+1)%len(b)]
			if (cross(a0, a1, b0) > 0) != (cross(a0, a1, b1) > 0) && (cross(b0, b1, a0) > 0) != (cross(b0, b1, a1) > 0) {
				return true
			}
		}
	}
	return false
}

// clipToPolygon keeps the parts of the (flattened) path inside poly.
func clipToPolygon(segments [][4]float64, poly [][2]float64, emit func(x1, y1, x2, y2 float64)) {
	polys := [][][2]float64{poly}
	for _, s := range segments {
		dx, dy := s[2]-s[0], s[3]-s[1]
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		ux, uy := dx/l, dy/l
		for _, iv := range lineIntervals(s[0], s[1], ux, uy, polys) {
			t0, t1 := max(iv[0], 0), min(iv[1], l)
			if t1 > t0 {
				emit(s[0]+ux*t0, s[1]+uy*t0, s[0]+ux*t1, s[1]+uy*t1)
			}
		}
	}
}
