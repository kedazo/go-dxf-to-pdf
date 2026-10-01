package converter

import (
	"testing"

	dxf "github.com/ixmilia/dxf-go"
)

func TestCullEntities(t *testing.T) {
	in := dxf.NewLine()
	in.P1, in.P2 = dxf.Point{X: 1, Y: 1}, dxf.Point{X: 2, Y: 2}
	out := dxf.NewLine()
	out.P1, out.P2 = dxf.Point{X: 50, Y: 50}, dxf.Point{X: 60, Y: 60}
	unknown := dxf.NewLeader() // no extent known → always kept

	ents := []dxf.Entity{in, out, unknown}
	got := cullEntities(ents, entityBoxes(ents, nil), BBox{MinX: 0, MinY: 0, MaxX: 10, MaxY: 10})
	if len(got) != 2 || got[0] != dxf.Entity(in) || got[1] != dxf.Entity(unknown) {
		t.Errorf("culled = %v, want [in, unknown]", got)
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
