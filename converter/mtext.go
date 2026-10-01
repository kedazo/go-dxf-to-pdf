package converter

import (
	"strings"

	dxf "github.com/kedazo/dxf-go"
)

// MTextStyle represents the formatting state at a point in MText content.
type MTextStyle struct {
	FontName       string // \f font family or \F font file ("" = the text style's font)
	Bold           bool
	Italic         bool
	Height         float64 // 0 = use default; absolute height in drawing units
	HeightRelative float64 // 0 = not set; multiplier of the (default or absolute) height (from \H0.66x;)
	WidthFactor    float64 // 0 = use default (1.0)
	ObliqueAngle   float64 // degrees
	Tracking       float64 // \T: character advance factor (0 or 1 = normal)
	ColorR         int
	ColorG         int
	ColorB         int
	HasColor       bool // true once \C or \c set an explicit color (otherwise the entity color applies)
	Underline      bool
	Overstrike     bool
	Strikethrough  bool
	Superscript    bool // \Sx^; stack (e.g. the 2 of m²)
	Subscript      bool // \S^x; stack
	Stacked        bool // \Sa/b; \Sa#b; \Sa^b; with both parts: a stacked fraction
	StackType      dxf.MTextStackType
	Numerator      string
	Denominator    string

	VerticalAlignment dxf.MTextVerticalAlignment // \A
	Paragraph         dxf.MTextParagraph         // \p…; (alignment, indents, tab stops)
}

// MTextSegment is a piece of text with uniform style.
type MTextSegment struct {
	Text    string
	Style   MTextStyle
	NewLine bool // true = start a new line before this segment
}

// ParseMText parses MText content into styled segments, using the dxf
// package's MTEXT run parser (formatting codes, grouping, stacks, special
// characters and \U+/\M+ escapes).
func ParseMText(s string) []MTextSegment {
	var segments []MTextSegment
	for _, run := range dxf.ParseMTextRuns(s) {
		if run.NewParagraph {
			segments = append(segments, MTextSegment{NewLine: true})
		}
		style := MTextStyle{
			FontName:      run.Font,
			Bold:          run.Bold,
			Italic:        run.Italic,
			Height:        run.Height,
			WidthFactor:   run.WidthFactor,
			ObliqueAngle:  run.ObliqueAngle,
			Tracking:      run.Tracking,
			Underline:     run.Underline,
			Overstrike:    run.Overline,
			Strikethrough: run.Strike,
			Superscript:   run.Superscript,
			Subscript:     run.Subscript,
			Stacked:       run.Stacked && run.StackType != dxf.MTextStackNone && run.Numerator != "" && run.Denominator != "",
			StackType:     run.StackType,
			Numerator:     run.Numerator,
			Denominator:   run.Denominator,

			VerticalAlignment: run.VerticalAlignment,
			Paragraph:         run.Paragraph,
		}
		if run.HeightFactor != 1 {
			style.HeightRelative = run.HeightFactor
		}
		switch {
		case run.TrueColor >= 0:
			style.ColorR, style.ColorG, style.ColorB = (run.TrueColor>>16)&0xFF, (run.TrueColor>>8)&0xFF, run.TrueColor&0xFF
			style.HasColor = true
		case run.Color > 0 && run.Color < 256: // \C0; (ByBlock) and \C256; (ByLayer) mean the entity color
			rgb := ACIToRGB(int16(run.Color))
			style.ColorR, style.ColorG, style.ColorB = int(rgb.R), int(rgb.G), int(rgb.B)
			style.HasColor = true
		}
		// ^J/^M arrive as newlines (line breaks here); tabs stay, DrawMText
		// moves to the next tab stop.
		for i, line := range strings.Split(run.Text, "\n") {
			if i > 0 {
				segments = append(segments, MTextSegment{NewLine: true})
			}
			if line != "" {
				segments = append(segments, MTextSegment{Text: line, Style: style})
			}
		}
	}
	return segments
}
