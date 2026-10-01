package converter

import (
	"math"
	"slices"
	"strings"
	"testing"

	dxf "github.com/kedazo/dxf-go"
	"github.com/tdewolff/canvas"
)

func TestDashPattern(t *testing.T) {
	for _, tc := range []struct {
		name    string
		lengths []float64
		scale   float64
		offset  float64
		dashes  []float64
	}{
		{"dash gap", []float64{0.1, -0.05}, 10, 0, []float64{1, 0.5}},
		{"dot becomes a short dash", []float64{1, -0.5, 0, -0.5}, 1, 0, []float64{1, 0.5, dotMM, 0.5}},
		{"leading gap rotates to the end", []float64{-0.5, 1}, 1, 1, []float64{1, 0.5}},
		{"same kinds merge", []float64{0.5, 0.5, -1}, 1, 0, []float64{1, 1}},
		{"wrapping dash joins the first", []float64{1, -1, 2}, 1, 2, []float64{3, 1}},
		{"continuous", []float64{1}, 1, 0, nil},
		{"too fine to see", []float64{0.1, -0.1}, 1, 0, nil},
		{"no pattern", nil, 1, 0, nil},
	} {
		offset, dashes := dashPattern(tc.lengths, tc.scale)
		if math.Abs(offset-tc.offset) > 1e-12 || !slicesNear(dashes, tc.dashes) {
			t.Errorf("%s: dashPattern(%v, %v) = %v, %v, want %v, %v",
				tc.name, tc.lengths, tc.scale, offset, dashes, tc.offset, tc.dashes)
		}
	}
}

func slicesNear(a, b []float64) bool {
	return slices.EqualFunc(a, b, func(x, y float64) bool { return math.Abs(x-y) < 1e-12 })
}

func TestLineTypeNameResolution(t *testing.T) {
	layers := map[string]dxf.Layer{
		"HIDDEN": {Name: "HIDDEN", LineTypeName: "Takart vonal"},
		"PLAIN":  {Name: "PLAIN", LineTypeName: "CONTINUOUS"},
	}
	ins := dxf.NewInsert()
	ins.SetLayer("PLAIN")
	ins.SetLineTypeName("Axis")
	ctx := topCtx.child(ins, identityAffine, layers)

	for _, tc := range []struct{ layer, lt, want string }{
		{"HIDDEN", "BYLAYER", "Takart vonal"},
		{"HIDDEN", "ByLayer", "Takart vonal"},
		{"PLAIN", "Dashed", "Dashed"},
		{"PLAIN", "BYBLOCK", "Axis"},
		{"MISSING", "BYLAYER", ""},
	} {
		l := dxf.NewLine()
		l.SetLayer(tc.layer)
		l.SetLineTypeName(tc.lt)
		if got := ctx.lineTypeName(l, layers); got != tc.want {
			t.Errorf("layer %s, line type %s: got %q, want %q", tc.layer, tc.lt, got, tc.want)
		}
	}
}

// A dashed line becomes several pieces; the next entity restarts the pattern
// even where it continues the previous one, while polyline edges continue it.
func TestDashedStrokes(t *testing.T) {
	paper := PaperSize{Width: 100, Height: 100}
	newRenderer := func(batch bool) *Renderer {
		r := NewRenderer(paper, false, 0, "")
		r.SetTransform(NewTransform(BBox{MaxX: 100, MaxY: 100}, 1, paper, 0, AlignTopLeft, false))
		r.SetBatching(batch)
		r.SetDashedStyle(RGB{}, 0.1, 0, []float64{4, 1})
		return r
	}

	r := newRenderer(true)
	r.DrawLine(0, 50, 10, 50) // 10 mm: dashes 0-4, 5-9
	r.BreakPath()
	r.DrawLine(10, 50, 12, 50) // restarts: one 2 mm dash
	if got := r.pending.Dash(r.dashOffset, r.dashes...).String(); got != "M0 50L4 50M5 50L9 50M10 50L12 50" {
		t.Errorf("dashed = %s", got)
	}

	// Without batching (raster), connected edges still form one path, so the
	// pattern runs on across the vertex.
	for _, batch := range []bool{true, false} {
		r := newRenderer(batch)
		for i := 0; i < 600; i++ { // long polylines too
			x := float64(i) / 10
			r.DrawLine(x, 50, x+0.1, 50)
		}
		var p *canvas.Path = r.pending
		if n := strings.Count(p.String(), "M"); n != 1 {
			t.Errorf("batch=%v: polyline path %s has %d subpaths, want 1", batch, p, n)
		}
	}
}
