package converter

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	dxf "github.com/kedazo/dxf-go"
)

// monochromePixels reports the non-background and colored pixel counts of a
// PNG. A pixel is colored when its RGB channels differ; anti-aliased black
// ink on the white page is gray in all three.
func monochromePixels(t *testing.T, path string) (nonWhite, colored int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a == 0 || (r == g && g == bl && r == 0xffff) {
				continue // transparent or white
			}
			nonWhite++
			if r != g || g != bl {
				colored++
			}
		}
	}
	return nonWhite, colored
}

// All drawing colors become black with Monochrome on, while the same
// drawing keeps its colors without it.
func TestMonochromeGeometry(t *testing.T) {
	drawing := dxf.NewDrawing()

	red := dxf.NewSolid() // ACI red fill
	red.FirstCorner = dxf.Point{X: 10, Y: 10}
	red.SecondCorner = dxf.Point{X: 70, Y: 10}
	red.ThirdCorner = dxf.Point{X: 10, Y: 40}
	red.FourthCorner = dxf.Point{X: 70, Y: 40}
	red.SetColor(dxf.Color(1))

	green := dxf.NewSolid() // true-color fill
	green.FirstCorner = dxf.Point{X: 80, Y: 10}
	green.SecondCorner = dxf.Point{X: 140, Y: 10}
	green.ThirdCorner = dxf.Point{X: 80, Y: 40}
	green.FourthCorner = dxf.Point{X: 140, Y: 40}
	green.SetColor24Bit(0x00FF00)

	gradient := dxf.NewHatch() // gradient fill: the first stop overrides the entity color
	gradient.Paths = []dxf.HatchBoundaryPath{{
		PathType: 1,
		Vertices: [][2]float64{{10, 60}, {140, 60}, {140, 100}, {10, 100}},
		IsClosed: true,
	}}
	gradient.Gradient = &dxf.HatchGradient{
		IsGradient: true,
		Colors:     []dxf.HatchGradientColor{{HasTrueColor: true, TrueColor: 0x0000FF}},
	}
	gradient.SetColor(dxf.Color(2))

	drawing.Entities = append(drawing.Entities, red, green, gradient)
	dir := t.TempDir()
	in := filepath.Join(dir, "in.dxf")
	if err := drawing.SaveFile(in); err != nil {
		t.Fatal(err)
	}

	convert := func(name string, monochrome bool) (nonWhite, colored int) {
		out := filepath.Join(dir, name)
		if _, err := Convert(in, out, Options{
			Scale: "1:1", Paper: "A4", Margin: 10, Format: "png", DPI: 50, Monochrome: monochrome,
		}); err != nil {
			t.Fatalf("Convert(%s): %v", name, err)
		}
		return monochromePixels(t, out)
	}

	if nonWhite, colored := convert("mono.png", true); nonWhite == 0 || colored != 0 {
		t.Errorf("monochrome output = %d non-white, %d colored pixels; want drawing ink and no color", nonWhite, colored)
	}
	if nonWhite, colored := convert("color.png", false); nonWhite == 0 || colored == 0 {
		t.Errorf("color output = %d non-white, %d colored pixels; want drawing ink and some color", nonWhite, colored)
	}
}

// A gradient hatch's first stop overrides the entity color, so it is a
// separate color source to blacken.
func TestMonochromeHatchGradient(t *testing.T) {
	h := dxf.NewHatch()
	h.Gradient = &dxf.HatchGradient{
		IsGradient: true,
		Colors:     []dxf.HatchGradientColor{{HasTrueColor: true, TrueColor: 0x0000FF}},
	}
	if got := hatchFillColor(h, RGB{255, 255, 0}); got != (RGB{0, 0, 255}) {
		t.Fatalf("gradient color = %v, want its first stop", got)
	}

	r := NewRenderer(PaperSize{Width: 100, Height: 100}, false, 0, "")
	r.SetMonochrome(true)
	if got := r.plotRGB(hatchFillColor(h, RGB{255, 255, 0})); got != (RGB{}) {
		t.Errorf("monochrome gradient = %v, want black", got)
	}
}

// MTEXT runs with an inline color keep it normally and lose it in
// monochrome output.
func TestMonochromeMTextInlineColor(t *testing.T) {
	paper := PaperSize{Width: 200, Height: 200}
	render := func(name string, monochrome bool) string {
		r := NewRenderer(paper, false, 0, "")
		if r.fonts.find("DejaVuSans.ttf") == "" {
			t.Skip("DejaVu fonts not available")
		}
		r.SetMonochrome(monochrome)
		r.SetTransform(NewTransform(BBox{MaxX: 200, MaxY: 200}, 1, paper, 0, AlignTopLeft, false))
		r.SetStyle(RGB{0, 0, 255}, 1) // default blue text
		r.DrawMText(10, 100, ParseMText(`{\C1;RED}`), 40, 0, 1, 1, 0, textStyle{})
		out := filepath.Join(t.TempDir(), name)
		if err := r.Save(out, "png", 100, false); err != nil {
			t.Fatal(err)
		}
		return out
	}

	if nonWhite, colored := monochromePixels(t, render("mono.png", true)); nonWhite == 0 || colored != 0 {
		t.Errorf("monochrome text = %d non-white, %d colored pixels; want text and no color", nonWhite, colored)
	}
	if nonWhite, colored := monochromePixels(t, render("color.png", false)); nonWhite == 0 || colored == 0 {
		t.Errorf("color text = %d non-white, %d colored pixels; want text and some color", nonWhite, colored)
	}
}
