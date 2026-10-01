package converter

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	dxf "github.com/kedazo/dxf-go"
)

// External references (XREF blocks) are bound into the host drawing the way
// CAD programs show them: the referenced drawing's model space becomes the
// block's content, and its layers, line types, text and dimension styles and
// blocks are added under "<xref block name>|<name>" (layer "0" and the
// ByLayer/ByBlock/Continuous line types keep their names).

// maxXrefDepth limits nested external references.
const maxXrefDepth = 4

// XrefInfo describes an external reference of a drawing.
type XrefInfo struct {
	Name  string // block name
	Ref   string // path as stored in the drawing
	Path  string // file found ("" = not found)
	Error string // why it isn't shown ("" = bound)
}

// bindXrefs loads the external drawings referenced by the XREF blocks of d
// (relative to hostPath) and binds them into d. Nested references are bound
// too, up to maxXrefDepth; seen holds the files on the current path, to
// break cycles.
func bindXrefs(d *dxf.Drawing, hostPath, dwg2dxf string, depth int, seen map[string]bool, files *drawingFiles) []XrefInfo {
	var infos []XrefInfo
	type bound struct {
		name     string
		entities []dxf.Entity
		base     dxf.Point
	}
	var results []bound
	n := len(d.Blocks) // bound blocks are appended below
	for i := 0; i < n; i++ {
		b := &d.Blocks[i]
		if !b.IsXref() && b.XrefName == "" {
			continue
		}
		info := XrefInfo{Name: b.Name, Ref: b.XrefName}
		info.Path = findXrefFile(hostPath, b.XrefName)
		abs, _ := filepath.Abs(info.Path)
		switch {
		case info.Path == "":
			info.Error = "file not found"
		case depth >= maxXrefDepth:
			info.Error = "nested too deep"
		case seen[abs]:
			info.Error = "circular reference"
		default:
			seen[abs] = true
			x, nested, err := loadDrawingDepth(info.Path, dwg2dxf, depth+1, seen, files)
			delete(seen, abs)
			if err != nil {
				info.Error = err.Error()
				break
			}
			for _, ni := range nested {
				ni.Name = b.Name + "|" + ni.Name
				infos = append(infos, ni)
			}
			entities, base := mergeXref(d, x, b.Name+"|")
			results = append(results, bound{b.Name, entities, base})
		}
		infos = append(infos, info)
	}
	// d.Blocks may have grown (and moved): set the content by name.
	for _, r := range results {
		if b := d.BlockByName(r.name); b != nil {
			b.Entities, b.BasePoint = r.entities, r.base
		}
	}
	for _, info := range infos {
		if info.Error != "" {
			fmt.Fprintf(os.Stderr, "warning: external reference %q (%s): %s\n", info.Name, info.Ref, info.Error)
		}
	}
	return infos
}

// mergeXref adds x's tables and blocks to d under prefix and returns x's
// model space entities (renamed) and insertion base point.
func mergeXref(d, x *dxf.Drawing, prefix string) ([]dxf.Entity, dxf.Point) {
	ren := xrefRenamer(prefix)

	// The host's own records of xref-dependent names ("X|layer", with the
	// host's overrides such as frozen or colour) win over the xref's.
	hostLayers := make(map[string]bool, len(d.Layers))
	for _, l := range d.Layers {
		hostLayers[strings.ToUpper(l.Name)] = true
	}
	hostLineTypes := make(map[string]bool, len(d.LineTypes))
	for _, lt := range d.LineTypes {
		hostLineTypes[strings.ToUpper(lt.Name)] = true
	}
	hostStyles := make(map[string]bool, len(d.Styles))
	for _, s := range d.Styles {
		hostStyles[strings.ToUpper(s.Name)] = true
	}
	hostDimStyles := make(map[string]bool, len(d.DimStyles))
	for _, s := range d.DimStyles {
		hostDimStyles[strings.ToUpper(s.Name)] = true
	}

	for _, l := range x.Layers {
		name := ren.layer(l.Name)
		if name == l.Name || hostLayers[strings.ToUpper(name)] {
			continue // "0" and Defpoints are shared; host records win
		}
		l.Name = name
		l.LineTypeName = ren.lineType(l.LineTypeName)
		// Handles belong to the xref file and would clash with the host's
		// (viewports freeze layers by handle).
		l.SetHandle(0)
		d.Layers = append(d.Layers, l)
	}
	for _, lt := range x.LineTypes {
		if name := ren.lineType(lt.Name); name != lt.Name && !hostLineTypes[strings.ToUpper(name)] {
			lt.Name = name
			d.LineTypes = append(d.LineTypes, lt)
		}
	}
	for _, s := range x.Styles {
		if s.Name = ren.name(s.Name); !hostStyles[strings.ToUpper(s.Name)] {
			d.Styles = append(d.Styles, s)
		}
	}
	recordNames := make(map[dxf.Handle]string, len(x.BlockRecords))
	for i := range x.BlockRecords {
		recordNames[x.BlockRecords[i].Handle()] = x.BlockRecords[i].Name
	}
	for _, s := range x.DimStyles {
		if s.Name = ren.name(s.Name); hostDimStyles[strings.ToUpper(s.Name)] {
			continue
		}
		// The leader arrow is a block record handle of x: make it a name
		// (unresolved: the default arrow, not a host block by accident).
		if h, err := strconv.ParseUint(s.DimensionLeaderBlockName, 16, 64); err == nil {
			s.DimensionLeaderBlockName = ""
			if name := recordNames[dxf.Handle(h)]; name != "" {
				s.DimensionLeaderBlockName = ren.name(name)
			}
		}
		d.DimStyles = append(d.DimStyles, s)
	}
	for _, b := range x.Blocks {
		if isLayoutBlock(b.Name) {
			continue
		}
		b.Name = ren.name(b.Name)
		for _, e := range b.Entities {
			ren.entity(e)
		}
		d.Blocks = append(d.Blocks, b)
	}

	var model []dxf.Entity
	for _, e := range x.Entities {
		if !e.IsInPaperSpace() {
			ren.entity(e)
			model = append(model, e)
		}
	}
	// Units are not converted here: CAD programs put the unit factor into
	// the scale of the xref's INSERT when attaching it.
	return model, x.Header.InsertionBase
}

// isLayoutBlock reports whether a block holds a space (model or a layout).
func isLayoutBlock(name string) bool {
	n := strings.ToLower(name)
	return strings.HasPrefix(n, "*model_space") || strings.HasPrefix(n, "*paper_space")
}

// xrefRenamer prefixes the names an external drawing's entities refer to.
type xrefRenamer string

func (p xrefRenamer) name(n string) string {
	if n == "" {
		return n
	}
	return string(p) + n
}

func (p xrefRenamer) layer(n string) string {
	if n == "0" || n == "" || strings.EqualFold(n, "Defpoints") {
		return n
	}
	return string(p) + n
}

func (p xrefRenamer) lineType(n string) string {
	switch {
	case n == "", strings.EqualFold(n, "BYLAYER"), strings.EqualFold(n, "BYBLOCK"), strings.EqualFold(n, "CONTINUOUS"):
		return n
	}
	return string(p) + n
}

// entity renames the layer, line type, style and block references of e.
func (p xrefRenamer) entity(e dxf.Entity) {
	e.SetLayer(p.layer(e.Layer()))
	e.SetLineTypeName(p.lineType(e.LineTypeName()))
	switch v := e.(type) {
	case *dxf.Insert:
		v.Name = p.name(v.Name)
		for i := range v.Attributes {
			p.entity(&v.Attributes[i])
		}
	case *dxf.Text:
		v.TextStyleName = p.name(v.TextStyleName)
	case *dxf.MText:
		v.TextStyleName = p.name(v.TextStyleName)
	case *dxf.Attribute:
		v.TextStyleName = p.name(v.TextStyleName)
		v.MText.TextStyleName = p.name(v.MText.TextStyleName)
	case *dxf.AttributeDefinition:
		v.TextStyleName = p.name(v.TextStyleName)
	case *dxf.Leader:
		v.DimensionStyleName = p.name(v.DimensionStyleName)
	case dxf.Dimension:
		v.SetBlockName(p.name(v.BlockName()))
		v.SetDimensionStyleName(p.name(v.DimensionStyleName()))
	}
}

// findImages records the image file of every IMAGE in d (located like
// external references, relative to the drawing at path).
func findImages(d *dxf.Drawing, path string, images map[*dxf.Image]string) {
	missing := map[string]bool{}
	visit := func(e dxf.Entity) {
		img, ok := e.(*dxf.Image)
		if !ok {
			return
		}
		def := d.ImageDefinition(img)
		if def == nil {
			images[img] = ""
			return
		}
		file := findXrefFile(path, def.FileName)
		images[img] = file
		if file == "" && !missing[def.FileName] {
			missing[def.FileName] = true
			fmt.Fprintf(os.Stderr, "warning: image %q not found\n", def.FileName)
		}
	}
	for _, e := range d.Entities {
		visit(e)
	}
	for i := range d.Blocks {
		for _, e := range d.Blocks[i].Entities {
			visit(e)
		}
	}
}

// findXrefFile locates a file referenced by the drawing at hostPath ("" =
// not found) with dxf.FindXrefFile: relative to the drawing, with names
// garbled by a wrong-code-page archive matched too, and a DXF next to a
// referenced DWG preferred. A path from another machine is also looked for
// by its name next to the drawing.
func findXrefFile(hostPath, ref string) string {
	dir := filepath.Dir(hostPath)
	if path, err := dxf.FindXrefFile(dir, ref); err == nil {
		return path
	}
	if base := filepath.Base(strings.ReplaceAll(ref, `\`, "/")); base != ref && base != "." {
		if path, err := dxf.FindXrefFile(dir, base); err == nil {
			return path
		}
	}
	return ""
}
