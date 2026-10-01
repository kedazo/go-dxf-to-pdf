package converter

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	dxf "github.com/kedazo/dxf-go"
)

const dwgConvertHelp = `DWG support requires LibreDWG's dwg2dxf tool.

Install it:
  Ubuntu/Debian: build from https://github.com/LibreDWG/libredwg
  Fedora:        dnf install libredwg
  macOS:         brew install libredwg
  Windows:       download from https://github.com/LibreDWG/libredwg/releases

Or specify the path: --dwg2dxf /path/to/dwg2dxf`

// DefaultDwg2DxfName returns the default binary name for the current OS.
func DefaultDwg2DxfName() string {
	if runtime.GOOS == "windows" {
		return "dwg2dxf.exe"
	}
	return "dwg2dxf"
}

// FindDwg2Dxf locates the dwg2dxf binary: the explicit path if given, else
// the one on PATH or next to this program (where installers bundle it).
func FindDwg2Dxf(explicitPath string) (string, error) {
	if explicitPath != "" {
		if _, err := os.Stat(explicitPath); err != nil {
			return "", fmt.Errorf("dwg2dxf not found at %q: %w", explicitPath, err)
		}
		return explicitPath, nil
	}

	path, err := dxf.FindLibreDWGTool("dwg2dxf")
	if err != nil {
		return "", fmt.Errorf("dwg2dxf not found on PATH or next to the program.\n\n%s", dwgConvertHelp)
	}
	return path, nil
}

// IsDWG returns true if the file path has a .dwg extension.
func IsDWG(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".dwg")
}

// ConvertDWGtoDXF converts a DWG file to a DXF file in a new temporary
// directory using dwg2dxf and returns its path. The caller removes the
// directory (filepath.Dir of the result).
//
// dwg2dxf only gets ASCII file names, relative to that directory: Windows
// builds read their arguments in the system code page, which can't hold
// every name. A DWG with a non-ASCII path is copied there first.
//
// When minimal is true, dwg2dxf is invoked with -m (minimal header: only
// $ACADVER, HANDSEED and ENTITIES). This is used as a fallback for DWGs whose
// full HEADER section is degraded (e.g. AutoCAD 2018/AC1032 files where
// LibreDWG reports "Template section not found") and would otherwise be
// rejected by the strict DXF header parser. Note that -m drops $DWGCODEPAGE
// and $INSUNITS, so text-encoding and unit auto-detection are less reliable on
// the fallback output.
func ConvertDWGtoDXF(dwgPath, dwg2dxfPath string, minimal bool) (string, error) {
	workDir, err := os.MkdirTemp("", "dxf-to-pdf-*")
	if err != nil {
		return "", fmt.Errorf("creating temp directory: %w", err)
	}
	fail := func(err error) (string, error) {
		os.RemoveAll(workDir)
		return "", err
	}

	input := dwgPath
	if !isASCII(dwgPath) {
		input = "input.dwg"
		if err := copyFile(dwgPath, filepath.Join(workDir, input)); err != nil {
			return fail(fmt.Errorf("copying %s: %w", dwgPath, err))
		}
	} else if abs, err := filepath.Abs(dwgPath); err == nil {
		input = abs // the program runs in workDir
	}

	args := make([]string, 0, 5)
	if minimal {
		args = append(args, "-m")
	}
	args = append(args, "-y", "-o", "output.dxf", input)
	// dwg2dxf reports every object it only partly understands; show that
	// only when the conversion fails.
	var stderr bytes.Buffer
	cmd := exec.Command(dwg2dxfPath, args...)
	cmd.Dir = workDir
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Stderr.Write(stderr.Bytes())
		return fail(fmt.Errorf("dwg2dxf failed: %w", err))
	}

	// Verify output exists and is non-empty
	dxfPath := filepath.Join(workDir, "output.dxf")
	if info, err := os.Stat(dxfPath); err != nil || info.Size() == 0 {
		return fail(fmt.Errorf("dwg2dxf produced no output"))
	}
	return dxfPath, nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
