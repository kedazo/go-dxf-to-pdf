package converter

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// dwg2dxf only gets ASCII names: a DWG with a non-ASCII path is copied into
// the work directory first.
func TestConvertDWGtoDXFASCIINames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as dwg2dxf")
	}
	dir := t.TempDir()
	// A fake dwg2dxf: writes its arguments and the input's content as the
	// output file named after -o (relative to its working directory).
	fake := filepath.Join(dir, "dwg2dxf")
	script := "#!/bin/sh\nout=\"\"; while [ $# -gt 1 ]; do [ \"$1\" = -o ] && out=$2; echo \"$1\" >> args; shift; done\n" +
		"echo \"$1\" >> args; cat args \"$1\" > \"$out\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	dwg := filepath.Join(dir, "FЩLDSZINT ÉT-01.dwg")
	if err := os.WriteFile(dwg, []byte("DWG DATA\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, minimal := range []bool{false, true} {
		dxfPath, err := ConvertDWGtoDXF(dwg, fake, minimal)
		if err != nil {
			t.Fatalf("minimal=%v: %v", minimal, err)
		}
		out, err := os.ReadFile(dxfPath)
		os.RemoveAll(filepath.Dir(dxfPath))
		if err != nil {
			t.Fatal(err)
		}
		if !isASCII(strings.SplitN(string(out), "DWG DATA", 2)[0]) {
			t.Errorf("minimal=%v: dwg2dxf got non-ASCII arguments:\n%s", minimal, out)
		}
		if !strings.Contains(string(out), "DWG DATA") {
			t.Errorf("minimal=%v: the DWG's content didn't reach dwg2dxf:\n%s", minimal, out)
		}
		if minimal != strings.Contains(string(out), "-m\n") {
			t.Errorf("minimal=%v: arguments:\n%s", minimal, out)
		}
	}
}
