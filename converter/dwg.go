package converter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// FindDwg2Dxf locates the dwg2dxf binary, checking the explicit path first,
// then falling back to PATH lookup.
func FindDwg2Dxf(explicitPath string) (string, error) {
	if explicitPath != "" {
		if _, err := os.Stat(explicitPath); err != nil {
			return "", fmt.Errorf("dwg2dxf not found at %q: %w", explicitPath, err)
		}
		return explicitPath, nil
	}

	path, err := exec.LookPath(DefaultDwg2DxfName())
	if err != nil {
		return "", fmt.Errorf("dwg2dxf not found on PATH.\n\n%s", dwgConvertHelp)
	}
	return path, nil
}

// IsDWG returns true if the file path has a .dwg extension.
func IsDWG(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".dwg")
}

// ConvertDWGtoDXF converts a DWG file to a temporary DXF file using dwg2dxf.
// Returns the path to the temporary DXF file. Caller is responsible for cleanup.
func ConvertDWGtoDXF(dwgPath, dwg2dxfPath string) (string, error) {
	tmpFile, err := os.CreateTemp("", "dxf-to-pdf-*.dxf")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	dxfPath := tmpFile.Name()
	tmpFile.Close()
	os.Remove(dxfPath) // remove so dwg2dxf creates it fresh

	cmd := exec.Command(dwg2dxfPath, "-y", "-o", dxfPath, dwgPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(dxfPath)
		return "", fmt.Errorf("dwg2dxf failed: %w", err)
	}

	// Verify output exists and is non-empty
	info, err := os.Stat(dxfPath)
	if err != nil || info.Size() == 0 {
		os.Remove(dxfPath)
		return "", fmt.Errorf("dwg2dxf produced no output")
	}

	return dxfPath, nil
}
