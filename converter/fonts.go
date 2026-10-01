package converter

import (
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/tdewolff/canvas"
)

// Font substitution. CAD text names a font file (STYLE, e.g. "ARIALN.TTF",
// or an SHX font like "txt") or a family (MTEXT \f, e.g. "Arial Narrow").
// We use, in order:
//  1. that font file itself, if it is in a font directory;
//  2. the metric-compatible Liberation family (Sans, Sans Narrow, Serif,
//     Mono match Arial, Arial Narrow, Times New Roman, Courier New);
//  3. the DejaVu family, horizontally scaled to roughly Arial-like widths.
// Faces are loaded on first use only.

// fontStyleIndex maps a canvas style to the regular/bold/italic/bold-italic slot.
func fontStyleIndex(style canvas.FontStyle) int {
	i := 0
	if style&canvas.FontBold != 0 {
		i |= 1
	}
	if style&canvas.FontItalic != 0 {
		i |= 2
	}
	return i
}

var slotStyles = [4]canvas.FontStyle{
	canvas.FontRegular, canvas.FontBold, canvas.FontItalic, canvas.FontBold | canvas.FontItalic,
}

// fontCandidate is a family's file names for the four slots.
type fontCandidate struct {
	files     [4]string
	widthComp float64 // horizontal scale making it as wide as the font it replaces
}

func liberation(stem string) fontCandidate {
	return fontCandidate{files: [4]string{stem + "-Regular.ttf", stem + "-Bold.ttf", stem + "-Italic.ttf", stem + "-BoldItalic.ttf"}, widthComp: 1}
}

func dejaVu(stem, italic string, widthComp float64) fontCandidate {
	return fontCandidate{files: [4]string{stem + ".ttf", stem + "-Bold.ttf", stem + "-" + italic + ".ttf", stem + "-Bold" + italic + ".ttf"}, widthComp: widthComp}
}

// Width compensation for the DejaVu fallbacks: their width per cap height
// relative to the Liberation (Arial-metric) faces, measured on typical
// drawing text.
var fontFallbacks = map[string][]fontCandidate{
	"sans":   {liberation("LiberationSans"), dejaVu("DejaVuSans", "Oblique", 0.96)},
	"narrow": {liberation("LiberationSansNarrow"), dejaVu("DejaVuSansCondensed", "Oblique", 0.88)},
	"serif":  {liberation("LiberationSerif"), dejaVu("DejaVuSerif", "Italic", 0.91)},
	"mono":   {liberation("LiberationMono"), dejaVu("DejaVuSansMono", "Oblique", 1.10)},
}

// extraNarrow lists fonts narrower than the substitute of their category,
// with their width relative to it (Arial Narrow, Arial).
var extraNarrow = map[string]float64{
	"agency":    0.85, // Agency FB (AGENCYR.TTF / AGENCYB.TTF)
	"yugoth":    0.92, // Yu Gothic (YuGothL.ttc, …): narrower Latin than Arial
	"yu gothic": 0.92,
}

// fontNarrowing returns the extra horizontal scale for a CAD font name.
func fontNarrowing(name string) float64 {
	n := strings.ToLower(name)
	for key, f := range extraNarrow {
		if strings.Contains(n, key) {
			return f
		}
	}
	return 1
}

// fontCategory classifies a CAD font name for substitution.
func fontCategory(name string) string {
	n := strings.ToLower(name)
	n = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(n, ".ttf"), ".otf"), ".shx")
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(n, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("narrow", "cond", "agency") || n == "arialn" || strings.HasPrefix(n, "arialn"):
		return "narrow"
	case has("mono", "cour", "consol", "console", "typewriter"):
		return "mono"
	case has("times", "serif", "georgia", "garamond", "cambria", "book", "roman") && !has("romans", "sans"):
		return "serif"
	}
	return "sans"
}

// fontSet is one substituted font: its files per slot, loaded lazily.
type fontSet struct {
	mu        sync.Mutex
	files     [4]string // "" = use the regular file
	family    *canvas.FontFamily
	loaded    [4]bool
	capRatio  float64 // cap height / em size, measured on first use
	widthComp float64
	faces     map[faceKey]*canvas.FontFace // reused, so laid-out text can be cached per face
}

type faceKey struct {
	capHeightMM float64
	col         color.RGBA
	style       canvas.FontStyle
}

// face returns a face of the given cap height (CAD text height is the height
// of capitals, not the em size).
func (fs *fontSet) face(capHeightMM float64, col color.RGBA, style canvas.FontStyle) *canvas.FontFace {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	slot := fontStyleIndex(style)
	if !fs.loaded[slot] {
		fs.loaded[slot] = true
		file := fs.files[slot]
		if file == "" {
			file = fs.files[0]
		}
		fs.family.LoadFontFile(file, slotStyles[slot])
	}
	if fs.capRatio == 0 {
		fs.capRatio = 0.729 // DejaVu Sans, if the font has no cap height
		ref := fs.family.Face(100, canvas.Black, style, canvas.FontNormal)
		if m := ref.Metrics(); m.CapHeight > 0 && m.LineHeight > 0 {
			fs.capRatio = m.CapHeight / (100 / 2.83465)
		}
	}
	key := faceKey{capHeightMM, col, style}
	if f, ok := fs.faces[key]; ok {
		return f
	}
	if fs.faces == nil || len(fs.faces) >= maxCachedFaces {
		fs.faces = map[faceKey]*canvas.FontFace{}
	}
	f := fs.family.Face(capHeightMM/fs.capRatio*2.83465, col, style, canvas.FontNormal) // mm → pt
	fs.faces[key] = f
	return f
}

// maxCachedFaces bounds the faces a font keeps (text heights × colours).
const maxCachedFaces = 4096

// FontSubstitute returns the font file text in the given CAD font (file
// and/or TrueType family, bold/italic face) is drawn with (fontDir as for
// Options.FontDir) and the horizontal scale applied to match the
// original's widths.
func FontSubstitute(font, family string, bold, italic bool, fontDir string) (path string, widthComp float64) {
	fs := fontLibrary(fontDir).fontFor(font, family)
	path = fs.files[fontStyleIndex(faceStyle(bold, italic))]
	if path == "" {
		path = fs.files[0] // drawn with the regular face
	}
	return path, fs.widthComp
}

// fontLib resolves CAD font names to font files from a set of directories.
type fontLib struct {
	mu    sync.Mutex
	dirs  []string
	index map[string]string // lower-cased file name → path (built on first use)
	sets  map[string]*fontSet
}

var (
	fontLibsMu sync.Mutex
	fontLibs   = map[string]*fontLib{}
)

// fontLibrary returns the (shared) font library searching fontDir first,
// then the platform's font directories.
func fontLibrary(fontDir string) *fontLib {
	fontLibsMu.Lock()
	defer fontLibsMu.Unlock()
	if l, ok := fontLibs[fontDir]; ok {
		return l
	}
	l := &fontLib{sets: map[string]*fontSet{}}
	if fontDir != "" {
		l.dirs = append(l.dirs, fontDir)
	}
	l.dirs = append(l.dirs, systemFontDirs()...)
	fontLibs[fontDir] = l
	return l
}

// systemFontDirs lists where font files are looked up (not recursively).
func systemFontDirs() []string {
	switch runtime.GOOS {
	case "windows":
		dirs := []string{DefaultFontDir()}
		if w := os.Getenv("WINDIR"); w != "" {
			dirs = append(dirs, filepath.Join(w, "Fonts"))
		}
		return dirs
	case "darwin":
		return []string{"/Library/Fonts", "/System/Library/Fonts/Supplemental", "/System/Library/Fonts"}
	}
	return []string{
		DefaultFontDir(), // DejaVu
		"/usr/share/fonts/truetype/liberation",
		"/usr/share/fonts/truetype/liberation2",
		"/usr/share/fonts/liberation-sans", // Fedora layout
		"/usr/share/fonts/liberation-sans-narrow",
		"/usr/share/fonts/liberation-serif",
		"/usr/share/fonts/liberation-mono",
		"/usr/share/fonts/dejavu-sans-fonts",
		"/usr/share/fonts/dejavu-serif-fonts",
		"/usr/share/fonts/dejavu-sans-mono-fonts",
		"/usr/share/fonts/TTF", // Arch layout
		"/usr/share/fonts/truetype/msttcorefonts",
	}
}

// find returns the path of a font file by (case-insensitive) name.
func (l *fontLib) find(name string) string {
	if l.index == nil {
		l.index = map[string]string{}
		for _, dir := range l.dirs {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				key := strings.ToLower(e.Name())
				if _, dup := l.index[key]; !dup && !e.IsDir() {
					l.index[key] = filepath.Join(dir, e.Name())
				}
			}
		}
	}
	return l.index[strings.ToLower(name)]
}

// font returns the font set substituting the CAD font name.
func (l *fontLib) font(name string) *fontSet {
	return l.fontFor(name, "")
}

// fontFor returns the font set for a text style's font file and TrueType
// family name (either may be empty): the file itself if it is installed,
// else a substitute chosen by both names.
func (l *fontLib) fontFor(file, family string) *fontSet {
	l.mu.Lock()
	defer l.mu.Unlock()
	file, family = strings.ToLower(strings.TrimSpace(file)), strings.ToLower(strings.TrimSpace(family))
	key := file + "|" + family
	if fs, ok := l.sets[key]; ok {
		return fs
	}
	fs := l.resolve(file, family)
	l.sets[key] = fs
	return fs
}

func (l *fontLib) resolve(name, family string) *fontSet {
	newSet := func(files [4]string, widthComp float64) *fontSet {
		return &fontSet{files: files, family: canvas.NewFontFamily(filepath.Base(files[0])), widthComp: widthComp}
	}
	// The font file itself (TrueType fonts named by file), with its bold and
	// italic siblings by the usual file names (arialbd/ariali/arialbi.ttf,
	// georgiab/georgiai/georgiaz.ttf, Name-Bold.ttf, …).
	if ext := filepath.Ext(name); ext == ".ttf" || ext == ".otf" {
		if path := l.find(name); path != "" {
			stem := strings.TrimSuffix(name, ext)
			files := [4]string{path}
			for slot, suffixes := range [4][]string{1: {"bd", "b", "-bold"}, 2: {"i", "-italic", "-oblique"}, 3: {"bi", "z", "-bolditalic", "-boldoblique"}} {
				for _, s := range suffixes {
					if files[slot] = l.find(stem + s + ext); files[slot] != "" {
						break
					}
				}
			}
			return newSet(files, 1)
		}
	}
	hint := strings.TrimSpace(family + " " + name) // e.g. "arial narrow arialn.ttf"
	for _, c := range fontFallbacks[fontCategory(hint)] {
		var files [4]string
		if files[0] = l.find(c.files[0]); files[0] == "" {
			continue
		}
		for i := 1; i < 4; i++ {
			files[i] = l.find(c.files[i])
		}
		return newSet(files, c.widthComp*fontNarrowing(hint))
	}
	if hint != "" {
		return l.resolve("", "") // plain sans
	}
	// Nothing found: keep the historical location, so the error shows there.
	return newSet([4]string{filepath.Join(DefaultFontDir(), "DejaVuSans.ttf")}, 1)
}
