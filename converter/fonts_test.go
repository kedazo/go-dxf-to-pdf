package converter

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	dxf "github.com/kedazo/dxf-go"
	"github.com/tdewolff/canvas"
)

func TestFontCategory(t *testing.T) {
	for name, want := range map[string]string{
		"ARIALN.TTF":      "narrow",
		"Arial Narrow":    "narrow",
		"arial.ttf":       "sans",
		"txt":             "sans",
		"romans.shx":      "sans",
		"romanc.shx":      "serif",
		"Times New Roman": "serif",
		"cour.ttf":        "mono",
		"Consolas":        "mono",
		"":                "sans",
	} {
		if got := fontCategory(name); got != want {
			t.Errorf("fontCategory(%q) = %s, want %s", name, got, want)
		}
	}
}

// testFontLib searches only the given directories (no system fonts).
func testFontLib(t *testing.T, dirs ...string) *fontLib {
	t.Helper()
	for _, d := range dirs {
		if _, err := os.Stat(d); err != nil {
			t.Skipf("font directory %s not available", d)
		}
	}
	return &fontLib{dirs: dirs, sets: map[string]*fontSet{}}
}

func TestFontSubstitution(t *testing.T) {
	const dejavu, liberation = "/usr/share/fonts/truetype/dejavu", "/usr/share/fonts/truetype/liberation"

	// Only DejaVu: the condensed face, scaled to Arial Narrow widths.
	fs := testFontLib(t, dejavu).font("ARIALN.TTF")
	if filepath.Base(fs.files[0]) != "DejaVuSansCondensed.ttf" || fs.widthComp >= 1 {
		t.Errorf("DejaVu-only narrow = %s ×%v", fs.files[0], fs.widthComp)
	}
	if filepath.Base(fs.files[1]) != "DejaVuSansCondensed-Bold.ttf" {
		t.Errorf("bold slot = %s", fs.files[1])
	}

	// Liberation is metric-compatible: no compensation.
	lib := testFontLib(t, liberation, dejavu)
	if fs := lib.font("ARIALN.TTF"); filepath.Base(fs.files[0]) != "LiberationSansNarrow-Regular.ttf" || fs.widthComp != 1 {
		t.Errorf("narrow = %s ×%v", fs.files[0], fs.widthComp)
	}
	if fs := lib.font("txt"); filepath.Base(fs.files[0]) != "LiberationSans-Regular.ttf" {
		t.Errorf("SHX txt = %s", fs.files[0])
	}
	if lib.font("Arial Narrow") != lib.font("arial narrow") {
		t.Error("font sets must be shared per (case-insensitive) name")
	}

	// A font file present in a font directory is used as is.
	if fs := lib.font("dejavusansmono.ttf"); filepath.Base(fs.files[0]) != "DejaVuSansMono.ttf" || fs.widthComp != 1 {
		t.Errorf("exact file = %s ×%v", fs.files[0], fs.widthComp)
	}

	// Siblings of a font file supply bold and italic.
	if fs := lib.font("DejaVuSans.ttf"); filepath.Base(fs.files[1]) != "DejaVuSans-Bold.ttf" || filepath.Base(fs.files[2]) != "DejaVuSans-Oblique.ttf" {
		t.Errorf("font file siblings = %v", fs.files)
	}

	// Nothing available: the regular DejaVu path, so the load error shows it.
	if fs := testFontLib(t, t.TempDir()).font("ARIALN.TTF"); filepath.Base(fs.files[0]) != "DejaVuSans.ttf" {
		t.Errorf("no fonts = %s", fs.files[0])
	}
}

// A style's TrueType data picks the face: bold and italic styles may share
// one font file.
func TestStyleTrueTypeFace(t *testing.T) {
	style := func(name, file string, flags int) dxf.Style {
		s := dxf.NewStyle()
		s.Name, s.PrimaryFontFileName = name, file
		s.XData = dxf.XData{{Name: "ACAD", Items: []dxf.XDataItem{{Code: 1000, String: "Arial Narrow"}, {Code: 1071, Int: flags}}}}
		return *s
	}
	r := NewRenderer(PaperSize{Width: 10, Height: 10}, false, 0, "")
	r.SetTextStyles([]dxf.Style{style("PLAIN", "ARIALN.TTF", 0), style("BOLD", "ARIALN.TTF", 0x2000000), style("BI", "ARIALN.TTF", 0x3000000)})
	for name, want := range map[string]canvas.FontStyle{
		"PLAIN": canvas.FontRegular,
		"BOLD":  canvas.FontBold,
		"BI":    canvas.FontBold | canvas.FontItalic,
	} {
		if got := r.lookFor(name, 0, 0).style; got != want {
			t.Errorf("style %s: face %v, want %v", name, got, want)
		}
	}
	if st := r.textStyleNamed("BOLD"); st.family != "Arial Narrow" || !st.bold || st.italic {
		t.Errorf("BOLD style = %+v", st)
	}
}

// The width factor stretches the drawn text horizontally.
func TestTextWidthFactor(t *testing.T) {
	if _, err := os.Stat(filepath.Join(DefaultFontDir(), "DejaVuSans.ttf")); err != nil {
		t.Skip("DejaVu fonts not available")
	}
	paper := PaperSize{Width: 200, Height: 100}
	drawnWidth := func(width float64) float64 {
		r := NewRenderer(paper, false, 0, "")
		r.SetTransform(NewTransform(BBox{MaxX: 200, MaxY: 100}, 1, paper, 0, AlignTopLeft, false))
		r.DrawText(10, 50, "WIDTH TEST", 5, 0, 0, vAlignBaseline, r.lookWithFont("", "", width, 0))
		return r.c.Bounds().W()
	}
	if full, half := drawnWidth(1), drawnWidth(0.5); math.Abs(half/full-0.5) > 0.01 {
		t.Errorf("width 0.5 draws %v mm, width 1 draws %v mm", half, full)
	}
}
