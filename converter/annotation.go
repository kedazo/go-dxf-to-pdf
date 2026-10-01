package converter

import (
	"math"
	"strconv"
	"strings"

	dxf "github.com/kedazo/dxf-go"
)

// leaderArrow is how a LEADER's arrowhead is drawn.
type leaderArrow struct {
	size  float64 // arrow size × dimension scale, drawing units
	block string  // arrow block to insert ("" = closed filled triangle)
	none  bool    // no arrowhead (_NONE)
}

// leaderArrows resolves arrowheads per dimension style; a style's leader
// arrow block (DIMLDRBLK) is a handle to a block record.
type leaderArrows struct {
	styles map[string]leaderArrow // upper-cased dimension style name
	header leaderArrow            // $DIMASZ × $DIMSCALE
	blocks map[dxf.Handle]string  // block record handle → block name (MULTILEADER content)
}

// blockNames returns the block names by block record handle.
func (la *leaderArrows) blockNames() map[dxf.Handle]string {
	if la == nil {
		return nil
	}
	return la.blocks
}

func newLeaderArrows(d *dxf.Drawing) *leaderArrows {
	scaled := func(size, scale float64) float64 {
		if scale <= 0 {
			scale = 1
		}
		return size * scale
	}
	la := &leaderArrows{
		styles: make(map[string]leaderArrow, len(d.DimStyles)),
		header: leaderArrow{size: scaled(d.Header.DimensioningArrowSize, d.Header.DimensioningScaleFactor)},
	}
	recordNames := make(map[dxf.Handle]string, len(d.BlockRecords))
	for i := range d.BlockRecords {
		recordNames[d.BlockRecords[i].Handle()] = d.BlockRecords[i].Name
	}
	la.blocks = recordNames
	for _, s := range d.DimStyles {
		a := leaderArrow{size: scaled(s.DimensioningArrowSize, s.DimensioningScaleFactor)}
		block := s.DimensionLeaderBlockName
		if h, err := strconv.ParseUint(block, 16, 64); err == nil {
			block = recordNames[dxf.Handle(h)]
		}
		if strings.EqualFold(block, "_NONE") {
			a.none = true
		} else {
			a.block = block
		}
		la.styles[strings.ToUpper(s.Name)] = a
	}
	return la
}

// arrow returns the arrowhead of a leader in the given dimension style.
func (la *leaderArrows) arrow(dimStyle string) leaderArrow {
	if la == nil {
		return leaderArrow{}
	}
	if a, ok := la.styles[strings.ToUpper(dimStyle)]; ok {
		return a
	}
	return la.header
}

// arrowAffine places a unit arrowhead at tip, pointing along from → tip
// (arrow blocks are drawn with the tip at the origin and the body along −X,
// as for the right end of a dimension line).
func arrowAffine(tip, from dxf.Point, size float64) affine {
	deg := math.Atan2(tip.Y-from.Y, tip.X-from.X) * 180 / math.Pi
	return translateAffine(tip.X, tip.Y).mul(rotateAffine(deg)).mul(scaleAffine(size, size))
}

// renderLeader draws a LEADER's path and arrowhead.
func renderLeader(r *Renderer, e *dxf.Leader, layers map[string]dxf.Layer, blocks map[string]*dxf.Block,
	ctx drawCtx, rgb RGB) {

	m := ctx.m
	pts := make([][2]float64, len(e.Vertices))
	for i, v := range e.Vertices {
		x, y := m.apply(v.X, v.Y)
		pts[i] = [2]float64{x, y}
	}
	r.DrawPolyline(pts, false)

	if !e.UseArrowheads || len(e.Vertices) < 2 {
		return
	}
	a := r.leaderArrows.arrow(e.DimensionStyleName)
	if a.none || a.size <= 0 {
		return
	}
	local := arrowAffine(e.Vertices[0], e.Vertices[1], a.size)
	if blk, ok := blocks[a.block]; ok && a.block != "" {
		actx := ctx.child(e, local.mul(translateAffine(-blk.BasePoint.X, -blk.BasePoint.Y)), layers)
		for _, be := range blk.Entities {
			renderEntity(r, be, layers, blocks, actx)
		}
		return
	}
	am := m.mul(local)
	tri := make([][2]float64, 0, 3)
	for _, p := range [3][2]float64{{0, 0}, {-1, 1.0 / 6}, {-1, -1.0 / 6}} {
		x, y := am.apply(p[0], p[1])
		tri = append(tri, [2]float64{x, y})
	}
	r.FillPolygons([][][2]float64{tri}, rgb)
}

// tableInsert returns the INSERT a table stands for: its "*T" block, which
// holds the table's lines and cell texts, at the insertion point.
func tableInsert(e *dxf.Table) *dxf.Insert {
	ins := dxf.NewInsert()
	ins.Name = e.BlockName
	ins.Location = e.InsertionPoint
	copyEntityStyle(e, ins)
	return ins
}

// copyEntityStyle gives to the layer, color, line type and line weight of
// from, so that an entity standing in for from (and its block content)
// looks like it.
func copyEntityStyle(from, to dxf.Entity) {
	to.SetLayer(from.Layer())
	to.SetColor(from.Color())
	if from.HasColor24Bit() {
		to.SetColor24Bit(from.Color24Bit())
	}
	to.SetLineTypeName(from.LineTypeName())
	to.SetLineWeight(from.LineWeight())
}

// mleaderParts returns what a MULTILEADER shows besides its arrows: the
// leader paths (each line through the landing and along the dogleg), the
// text as an MTEXT (nil = none) and the content block's INSERT (nil = none,
// or the block is unknown).
func mleaderParts(e *dxf.MLeader, blockNames map[dxf.Handle]string) (paths [][]dxf.Point, text *dxf.MText, block *dxf.Insert) {
	for _, l := range e.Leaders {
		dogleg := dxf.Point{
			X: l.LastLeaderPoint.X + l.DoglegVector.X*l.DoglegLength,
			Y: l.LastLeaderPoint.Y + l.DoglegVector.Y*l.DoglegLength,
		}
		for _, line := range l.Lines {
			path := append([]dxf.Point(nil), line...)
			if n := len(path); n == 0 || path[n-1] != l.LastLeaderPoint {
				path = append(path, l.LastLeaderPoint)
			}
			if l.DoglegLength != 0 {
				path = append(path, dogleg)
			}
			paths = append(paths, path)
		}
	}
	if e.HasText && e.Text != "" {
		text = dxf.NewMText()
		text.Text = e.Text
		text.InsertionPoint = e.TextLocation
		text.InitialTextHeight = e.TextHeight
		text.ReferenceRectangleWidth = e.TextWidth
		if d := e.TextDirection; d.X != 0 || d.Y != 0 {
			text.XAxisDirection = d
			text.HasXAxisDirection = true
		}
		copyEntityStyle(e, text)
	}
	if name := blockNames[e.BlockRecordHandle]; e.HasBlock && name != "" {
		block = dxf.NewInsert()
		block.Name = name
		block.ExtrusionDirection = e.BlockNormal
		block.Location = dxf.WCSToOCSMatrix(e.BlockNormal).TransformPoint(e.BlockLocation)
		block.XScaleFactor, block.YScaleFactor, block.ZScaleFactor = e.BlockScale.X, e.BlockScale.Y, e.BlockScale.Z
		block.Rotation = e.BlockRotation * 180 / math.Pi
		copyEntityStyle(e, block)
	}
	return paths, text, block
}

// renderMLeader draws a MULTILEADER: leader lines with filled arrowheads,
// its text and its content block.
func renderMLeader(r *Renderer, e *dxf.MLeader, layers map[string]dxf.Layer, blocks map[string]*dxf.Block,
	ctx drawCtx, rgb RGB) {

	m := ctx.m
	paths, text, block := mleaderParts(e, r.leaderArrows.blockNames())
	for _, path := range paths {
		pts := make([][2]float64, len(path))
		for i, p := range path {
			pts[i][0], pts[i][1] = m.apply(p.X, p.Y)
		}
		r.BreakPath()
		r.DrawPolyline(pts, false)
		if len(path) >= 2 && e.ArrowSize > 0 {
			am := m.mul(arrowAffine(path[0], path[1], e.ArrowSize))
			tri := make([][2]float64, 0, 3)
			for _, p := range [3][2]float64{{0, 0}, {-1, 1.0 / 6}, {-1, -1.0 / 6}} {
				x, y := am.apply(p[0], p[1])
				tri = append(tri, [2]float64{x, y})
			}
			r.FillPolygons([][][2]float64{tri}, rgb)
		}
	}
	if text != nil {
		renderEntity(r, text, layers, blocks, ctx)
	}
	if block != nil {
		renderEntity(r, block, layers, blocks, ctx)
	}
}

// wipeoutPolygon returns a WIPEOUT's masking area in its own coordinates.
// Clip vertices are in image pixels with the origin at the top-left pixel's
// center, y down; the U/V vectors span one pixel (a wipeout is 1 × 1).
func wipeoutPolygon(w *dxf.Wipeout) [][2]float64 {
	size := w.ImageSize()
	if size.X <= 0 {
		size.X = 1
	}
	if size.Y <= 0 {
		size.Y = 1
	}
	clip := w.ClippingVertices()
	var px [][2]float64
	switch {
	case len(clip) == 2: // rectangle by two corners
		a, b := clip[0], clip[1]
		px = [][2]float64{{a.X, a.Y}, {b.X, a.Y}, {b.X, b.Y}, {a.X, b.Y}}
	case len(clip) > 2:
		for _, p := range clip {
			px = append(px, [2]float64{p.X, p.Y})
		}
		if n := len(px); px[0] == px[n-1] {
			px = px[:n-1]
		}
	default: // the whole image
		x0, y0, x1, y1 := -0.5, -0.5, size.X-0.5, size.Y-0.5
		px = [][2]float64{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}
	}
	loc, u, v := w.Location(), w.UVector(), w.VVector()
	poly := make([][2]float64, len(px))
	for i, p := range px {
		s, t := p[0]+0.5, size.Y-p[1]-0.5
		poly[i] = [2]float64{loc.X + u.X*s + v.X*t, loc.Y + u.Y*s + v.Y*t}
	}
	return poly
}
