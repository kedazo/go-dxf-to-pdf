package converter

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

func header(ver, cp string) string {
	s := "0\nSECTION\n2\nHEADER\n9\n$ACADVER\n1\n" + ver + "\n"
	if cp != "" {
		s += "9\n$DWGCODEPAGE\n3\n" + cp + "\n"
	}
	return s + "0\nENDSEC\n"
}

func TestSniffDxfEncodingFallbacks(t *testing.T) {
	legacy := "\xe1rv\xedzt\xfbr\xf5" // "árvíztűrő" in Windows-1250, not valid UTF-8
	tests := []struct {
		name string
		data string
		want *charmap.Charmap
	}{
		{"utf8 2013", header("AC1027", "ANSI_1250") + "1\nárvíztűrő\n", nil},
		{"ascii", header("AC1015", "ANSI_1250") + "1\nplain\n", nil},
		{"legacy pre2007 with codepage: parser decodes", header("AC1015", "ANSI_1250") + "1\n" + legacy + "\n", nil},
		{"legacy bytes in 2013 file", header("AC1027", "ANSI_1250") + "1\n" + legacy + "\n", charmap.Windows1250},
		{"no codepage, c238 font", header("AC1015", "") + "1\n{\\fArial|b0|i0|c238|p0;" + legacy + "}\n", charmap.Windows1250},
		{"no codepage, no hint", header("AC1015", "") + "1\n" + legacy + "\n", nil},
	}
	for _, tt := range tests {
		h, err := sniffDxfEncoding(strings.NewReader(tt.data))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got := h.fallbackEncoding(); got != tt.want {
			t.Errorf("%s: fallback = %v, want %v (hints %+v)", tt.name, got, tt.want, h)
		}
	}
}

// Valid multi-byte runes anywhere around the scan's chunk boundary (split by
// it or just before it) must not be reported as invalid UTF-8, and a
// "c238" marker split by the boundary must still be found.
func TestSniffDxfEncodingChunkBoundary(t *testing.T) {
	for offset := 1; offset <= 6; offset++ {
		data := bytes.Repeat([]byte("a"), sniffChunk-offset)
		data = append(data, "őű€"...) // 2-, 2- and 3-byte runes
		data = append(data, "\nend"...)
		h, err := sniffDxfEncoding(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if h.invalidUTF8 {
			t.Errorf("offset %d: valid UTF-8 reported invalid", offset)
		}
	}
	for split := 1; split < 4; split++ {
		data := bytes.Repeat([]byte("a"), sniffChunk-split)
		data = append(data, "c238;x"...)
		h, err := sniffDxfEncoding(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if !h.c238 {
			t.Errorf("split %d: c238 across the chunk boundary not found", split)
		}
	}
	// Invalid bytes in the second chunk are still detected.
	data := append(bytes.Repeat([]byte("a"), sniffChunk+10), 0xF5, 'x')
	if h, _ := sniffDxfEncoding(bytes.NewReader(data)); !h.invalidUTF8 {
		t.Error("invalid byte after the first chunk not detected")
	}
}
