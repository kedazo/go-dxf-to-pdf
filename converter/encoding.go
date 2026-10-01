package converter

import (
	"bytes"
	"io"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// dxfEncodingHints is what a quick scan of the raw DXF bytes tells about its
// text encoding.
type dxfEncodingHints struct {
	version     string // $ACADVER, e.g. "AC1027"
	codepage    string // $DWGCODEPAGE, e.g. "ANSI_1250"
	invalidUTF8 bool   // the file contains bytes that are not valid UTF-8
	c238        bool   // MText font specs with charset 238 (Central European)
}

// sniffChunk is the read size of the encoding scan; the HEADER section (with
// $ACADVER and $DWGCODEPAGE) is expected within the first chunk.
const sniffChunk = 256 << 10

// sniffDxfEncoding scans a DXF stream for encoding hints without holding the
// whole file in memory.
func sniffDxfEncoding(r io.Reader) (dxfEncodingHints, error) {
	const marker = "c238"
	var h dxfEncodingHints
	chunk := make([]byte, sniffChunk)
	utf := make([]byte, 0, sniffChunk+utf8.UTFMax) // incomplete rune carried over + chunk
	seam := make([]byte, 0, 2*len(marker))         // previous chunk's tail + this chunk's head
	first := true
	for {
		n, err := io.ReadFull(r, chunk)
		data := chunk[:n]
		eof := err == io.EOF || err == io.ErrUnexpectedEOF
		if err != nil && !eof {
			return h, err
		}

		if first {
			h.version = headerValue(data, "$ACADVER")
			h.codepage = headerValue(data, "$DWGCODEPAGE")
			first = false
		}
		if !h.c238 {
			seam = append(seam, data[:min(len(marker)-1, len(data))]...)
			h.c238 = bytes.Contains(data, []byte(marker)) || bytes.Contains(seam, []byte(marker))
			seam = append(seam[:0], data[max(len(data)-(len(marker)-1), 0):]...)
		}

		// Validate up to the last complete rune; only an incomplete rune at
		// the very end is carried into the next round.
		utf = append(utf, data...)
		cut := len(utf)
		if !eof {
			for i := len(utf) - 1; i >= 0 && i >= len(utf)-utf8.UTFMax; i-- {
				if utf8.RuneStart(utf[i]) {
					if !utf8.FullRune(utf[i:]) {
						cut = i
					}
					break
				}
			}
		}
		if !h.invalidUTF8 && !utf8.Valid(utf[:cut]) {
			h.invalidUTF8 = true
		}
		if eof {
			return h, nil
		}
		utf = append(utf[:0], utf[cut:]...)
	}
}

// headerValue returns the value of a HEADER variable ($NAME, group code line,
// value line) found in data, or "".
func headerValue(data []byte, name string) string {
	idx := bytes.Index(data, []byte(name))
	if idx < 0 {
		return ""
	}
	lines := bytes.SplitN(data[idx:min(idx+256, len(data))], []byte{'\n'}, 4)
	if len(lines) < 3 || string(bytes.TrimSpace(lines[0])) != name {
		return ""
	}
	return string(bytes.TrimSpace(lines[2]))
}

// fallbackEncoding returns the encoding to force on the DXF parser, or nil to
// let it decide. The parser already decodes pre-2007 text using
// $DWGCODEPAGE (keeping values that are valid UTF-8) and treats 2007+ as
// UTF-8. Two cases are left over, both only when the file is not valid UTF-8:
//   - 2007+ files that still carry legacy-code-page bytes: use $DWGCODEPAGE;
//   - files without a code page (e.g. dwg2dxf -m minimal headers) whose MText
//     font specs say charset 238: assume Windows-1250.
func (h dxfEncodingHints) fallbackEncoding() *charmap.Charmap {
	if !h.invalidUTF8 {
		return nil
	}
	pre2007 := h.version != "" && h.version < "AC1021"
	if pre2007 && h.codepage != "" {
		return nil
	}
	if !pre2007 {
		if cm := windowsCodePage(h.codepage); cm != nil {
			return cm
		}
	}
	if h.c238 {
		return charmap.Windows1250
	}
	return nil
}

// windowsCodePage maps the common ANSI_125x code pages.
func windowsCodePage(cp string) *charmap.Charmap {
	switch cp {
	case "ANSI_1250":
		return charmap.Windows1250
	case "ANSI_1251":
		return charmap.Windows1251
	case "ANSI_1252":
		return charmap.Windows1252
	case "ANSI_1253":
		return charmap.Windows1253
	case "ANSI_1254":
		return charmap.Windows1254
	case "ANSI_1255":
		return charmap.Windows1255
	case "ANSI_1256":
		return charmap.Windows1256
	case "ANSI_1257":
		return charmap.Windows1257
	}
	return nil
}
