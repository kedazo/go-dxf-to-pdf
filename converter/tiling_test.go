package converter

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	dxf "github.com/kedazo/dxf-go"
	"github.com/tdewolff/canvas"
)

// Transparent tiled PNGs keep the margins clear of spilling text and fills;
// only the crop marks are drawn there.
func TestTransparentTilesClipped(t *testing.T) {
	if _, err := os.Stat(filepath.Join(DefaultFontDir(), "DejaVuSans.ttf")); err != nil {
		t.Skip("DejaVu fonts not available")
	}
	drawing := dxf.NewDrawing()
	line := dxf.NewLine()
	line.P2 = dxf.Point{X: 600, Y: 300}
	text := dxf.NewText()
	text.Value = "A LONG LABEL ACROSS THE TILE EDGE"
	text.Height = 20
	text.Location = dxf.Point{X: 120, Y: 150} // crosses the first tile's right edge
	drawing.Entities = append(drawing.Entities, line, text)
	dir := t.TempDir()
	in := filepath.Join(dir, "in.dxf")
	if err := drawing.SaveFile(in); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.png")
	if _, err := Convert(in, out, Options{Scale: "1:1", Paper: "A4", Margin: 10, Tile: true, Transparent: true, DPI: 50}); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	f, err := os.Open(filepath.Join(dir, "out_1.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	dpmm := 50 / 25.4
	margin := int(10 * dpmm)
	opaque := func(x0, y0, x1, y1 int) int {
		n := 0
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
					n++
				}
			}
		}
		return n
	}
	// The right margin, away from the crop marks at the corners.
	if n := opaque(b.Max.X-margin+1, margin+int(6*dpmm), b.Max.X, b.Max.Y-margin-int(6*dpmm)); n > 0 {
		t.Errorf("%d opaque pixels in the right margin", n)
	}
	if n := opaque(0, 0, margin, margin); n == 0 {
		t.Error("crop mark missing in the top-left corner")
	}
}

// With text cutting (transparent tiled PDF) text across the clip rectangle
// is cut at it; text inside stays text.
func TestTextCutAtClip(t *testing.T) {
	if _, err := os.Stat(filepath.Join(DefaultFontDir(), "DejaVuSans.ttf")); err != nil {
		t.Skip("DejaVu fonts not available")
	}
	paper := PaperSize{Width: 200, Height: 200}
	r := NewRenderer(paper, false, 0, "")
	r.SetTransform(NewTransform(BBox{MaxX: 200, MaxY: 200}, 1, paper, 0, AlignTopLeft, false))
	r.SetTextCut(true)
	r.SetClipRect(0, 0, 50, 200)
	r.DrawText(40, 100, "A LONG LABEL", 5, 0, 0, vAlignBaseline, r.lookWithFont("", "", 1, 0))
	r.ClipEnd()
	if b := r.c.Bounds(); b.W() == 0 || b.X1 > 50.01 {
		t.Errorf("cut text spans x %v..%v, want it to end at the clip edge 50", b.X0, b.X1)
	}
}

// Curves are clipped by their true extent: canvas' FastBounds misses the
// end of a cubic beyond its second control point.
func TestClipPathCurveBounds(t *testing.T) {
	c := curveThrough([][2]float64{{0, 0}, {10, 10}, {20, 0}})
	p := &canvas.Path{}
	p.MoveTo(c[0][0][0], c[0][0][1])
	for _, b := range c {
		p.CubeTo(b[1][0], b[1][1], b[2][0], b[2][1], b[3][0], b[3][1])
	}
	if b := hullBounds(p); b.X1 < 20 {
		t.Errorf("hull bounds end at x=%v, want 20", b.X1)
	}
	if b := clipPath(p, canvas.Rect{X0: -1, Y0: -1, X1: 19, Y1: 11}).Bounds(); b.X1 > 19+1e-6 {
		t.Errorf("clipped curve reaches x=%v, past the clip at 19", b.X1)
	}
	if clipPath(p, canvas.Rect{X0: 19, Y0: -1, X1: 30, Y1: 11}).Empty() {
		t.Error("the curve's piece past x=19 was dropped")
	}
}

// Closed shapes clipped by a viewport keep the join at their start point.
func TestClipStrokesKeepsStartJoin(t *testing.T) {
	r := &Renderer{clipPoly: [][2]float64{{0, 0}, {10, 0}, {10, 10}, {0, 10}}}
	square := func(x0, y0, x1, y1 float64) *canvas.Path {
		p := &canvas.Path{}
		p.MoveTo(x0, y0)
		p.LineTo(x1, y0)
		p.LineTo(x1, y1)
		p.LineTo(x0, y1)
		p.Close()
		return p
	}
	// Inside: still a closed square.
	if got := r.clipStrokes(square(2, 2, 4, 4)).String(); got != "M2 2L4 2L4 4L2 4z" {
		t.Errorf("inside: %s, want the closed square", got)
	}
	// Crossing the right edge, starting inside: one piece through the start.
	if got := r.clipStrokes(square(6, 2, 14, 4)).String(); got != "M10 4L6 4L6 2L10 2" {
		t.Errorf("crossing: %s, want one piece around the start corner", got)
	}
}

func TestCullEntities(t *testing.T) {
	in := dxf.NewLine()
	in.P1, in.P2 = dxf.Point{X: 1, Y: 1}, dxf.Point{X: 2, Y: 2}
	out := dxf.NewLine()
	out.P1, out.P2 = dxf.Point{X: 50, Y: 50}, dxf.Point{X: 60, Y: 60}
	unknown := dxf.NewLeader() // no extent known → always kept

	ents := []dxf.Entity{in, out, unknown}
	got := cullEntities(ents, entityBoxes(ents, nil, nil, nil), BBox{MinX: 0, MinY: 0, MaxX: 10, MaxY: 10})
	if len(got) != 2 || got[0] != dxf.Entity(in) || got[1] != dxf.Entity(unknown) {
		t.Errorf("culled = %v, want [in, unknown]", got)
	}
}

// Stroke batching flushes every maxPendingSegs segments; arcs and lines that
// straddle a flush (clipped or not) must keep drawing correctly.
func TestStrokeBatchingAcrossFlushes(t *testing.T) {
	paper := PaperSize{Width: 100, Height: 100}
	r := NewRenderer(paper, false, 0, "")
	r.SetTransform(NewTransform(BBox{MaxX: 100, MaxY: 100}, 1, paper, 0, AlignTopLeft, false))
	r.SetBatching(true)
	r.SetStyle(RGB{}, 0.1)
	for pass := 0; pass < 2; pass++ {
		if pass == 1 {
			r.SetClipRect(10, 10, 80, 80)
		}
		for i := 0; i < maxPendingSegs+10; i++ {
			x := float64(i%100) + 0.5
			r.DrawLine(x, 0, x, 100)
			if i%1000 == 0 {
				r.DrawEllipticArc(50, 50, 30, 0, 0, 30, 0, 6.283185307)
			}
		}
		r.ClipEnd()
	}
	if err := r.Save(t.TempDir()+"/out.pdf", "pdf", 0, false); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// Raster output strokes batches itself: crossing chains and dashes all
// render, each at the stroke width.
func TestRasterStrokeBatches(t *testing.T) {
	paper := PaperSize{Width: 100, Height: 100}
	r := NewRenderer(paper, false, 0, "")
	r.SetTransform(NewTransform(BBox{MaxX: 100, MaxY: 100}, 1, paper, 0, AlignTopLeft, false))
	r.SetBatching(true)
	r.SetRasterStrokes(50)
	r.SetStyle(RGB{}, 1)
	for i := 0; i < 50; i++ { // a self-crossing zigzag chain
		r.DrawLine(float64(i*2), float64(10+(i%2)*80), float64(i*2+2), float64(10+((i+1)%2)*80))
	}
	r.SetDashedStyle(RGB{}, 1, 0, []float64{5, 5})
	r.DrawLine(0, 95, 100, 95) // page y = 5
	path := t.TempDir() + "/out.png"
	if err := r.Save(path, "png", 50, false); err != nil {
		t.Fatalf("Save: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	dark := func(xmm, ymm float64) bool {
		c, _, _, _ := img.At(int(xmm*50/25.4), int(ymm*50/25.4)).RGBA()
		return c < 0x8000
	}
	if !dark(51, 50) || !dark(1, 50) || dark(50, 50) { // the segments cross mid-height at odd x
		t.Error("zigzag chain not drawn")
	}
	if !dark(2.5, 5) || dark(7.5, 5) || !dark(12.5, 5) {
		t.Error("dashes not drawn as dash, gap, dash")
	}
}

// Separate strokes in one batch must stay separate after clipping (canvas'
// Path.Clip would join them with a stray line).
func TestClipPathKeepsSubpathsSeparate(t *testing.T) {
	p := &canvas.Path{}
	p.MoveTo(1, 1)
	p.LineTo(2, 1)
	p.MoveTo(5, 5)
	p.LineTo(6, 5)
	p.MoveTo(-5, 3) // crosses the left edge
	p.LineTo(3, 3)
	got := clipPath(p, canvas.Rect{X0: 0, Y0: 0, X1: 10, Y1: 10}).String()
	if want := "M1 1L2 1M5 5L6 5M0 3L3 3"; got != want {
		t.Errorf("clipped = %s, want %s", got, want)
	}
}

func TestStrokesClippedToTile(t *testing.T) {
	paper := PaperSize{Width: 200, Height: 200}
	r := NewRenderer(paper, false, 0, "")
	bbox := BBox{MinX: 0, MinY: 0, MaxX: 200, MaxY: 200}
	r.SetTransform(NewTransform(bbox, 1, paper, 0, AlignTopLeft, false))
	r.SetStyle(RGB{}, 0.2)

	r.SetClipRect(50, 50, 40, 40)
	r.DrawLine(0, 200, 200, 0)                               // page (0,0)-(200,200): crosses the tile
	r.DrawEllipticArc(150, 30, 10, 0, 0, 10, 0, 6.283185307) // fully outside
	r.ClipEnd()

	b := r.c.Bounds() // canvas-native, Y-up: flip back to page Y-down
	if b.W() == 0 {
		t.Fatal("nothing drawn; the line should cross the tile")
	}
	y0, y1 := paper.Height-b.Y1, paper.Height-b.Y0
	const hw = 0.2 // stroke width slack
	if b.X0 < 50-hw || y0 < 50-hw || b.X1 > 90+hw || y1 > 90+hw {
		t.Errorf("drawn page bounds (%v,%v)-(%v,%v) leave the clip rect (50,50)-(90,90)", b.X0, y0, b.X1, y1)
	}
}
