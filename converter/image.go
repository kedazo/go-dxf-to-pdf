package converter

import (
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"

	dxf "github.com/kedazo/dxf-go"
	"github.com/tdewolff/canvas"
)

// SetImageFiles sets the image file of each IMAGE entity (see findImages).
func (r *Renderer) SetImageFiles(files map[*dxf.Image]string) {
	r.imageFiles = files
}

// loadImage decodes an image file once per renderer; failures are reported
// once and give nil.
func (r *Renderer) loadImage(path string) image.Image {
	if img, ok := r.images[path]; ok {
		return img
	}
	if r.images == nil {
		r.images = map[string]image.Image{}
	}
	var img image.Image
	if f, err := os.Open(path); err != nil {
		fmt.Fprintf(os.Stderr, "warning: image %s: %v\n", path, err)
	} else {
		if img, _, err = image.Decode(f); err != nil {
			fmt.Fprintf(os.Stderr, "warning: image %s: %v\n", path, err)
			img = nil
		}
		f.Close()
	}
	r.images[path] = img
	return img
}

// renderImage draws an IMAGE: its pixels span the U and V vectors from the
// insertion point (the lower-left corner), mapped through m. With IMAGEFRAME
// 1 its outline is drawn too, also when the file is missing.
func renderImage(r *Renderer, e *dxf.Image, m affine) {
	drawImagePixels(r, e, m)
	if r.imageFrame {
		loc, u, v, s := e.Location(), e.UVector(), e.VVector(), e.ImageSize()
		frame := make([][2]float64, 0, 4)
		for _, f := range [4][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
			x, y := m.apply(loc.X+u.X*s.X*f[0]+v.X*s.Y*f[1], loc.Y+u.Y*s.X*f[0]+v.Y*s.Y*f[1])
			frame = append(frame, [2]float64{x, y})
		}
		r.DrawPolyline(frame, true)
	}
}

func drawImagePixels(r *Renderer, e *dxf.Image, m affine) {
	path := r.imageFiles[e]
	if path == "" {
		return
	}
	img := r.loadImage(path)
	if img == nil {
		return
	}
	size := e.ImageSize()
	if size.X <= 0 || size.Y <= 0 {
		b := img.Bounds()
		size.X, size.Y = float64(b.Dx()), float64(b.Dy())
	}
	loc, u, v := e.Location(), e.UVector(), e.VVector()
	// The file's pixels may differ from the size the image was placed with.
	b := img.Bounds()
	sx, sy := size.X/float64(b.Dx()), size.Y/float64(b.Dy())

	// Page position of a file pixel (column i, row k from the top).
	page := func(i, k float64) (float64, float64) {
		x := loc.X + u.X*i*sx + v.X*(size.Y-k*sy)
		y := loc.Y + u.Y*i*sx + v.Y*(size.Y-k*sy)
		wx, wy := m.apply(x, y)
		return r.transform.X(wx), r.transform.Y(wy)
	}
	x0, y0 := page(0, 0)
	xi, yi := page(1, 0)
	xk, yk := page(0, 1)
	cx, cy := page(float64(b.Dx())/2, float64(b.Dy())/2)
	if r.outsideClip(cx, cy, max(abs(xi-x0)*float64(b.Dx()), abs(yk-y0)*float64(b.Dy()))) {
		return
	}
	// In a viewport: drawn whole if any of it shows (images aren't cut).
	if r.clipPoly != nil {
		w, h := float64(b.Dx()), float64(b.Dy())
		quad := make([][2]float64, 0, 4)
		for _, c := range [4][2]float64{{0, 0}, {w, 0}, {w, h}, {0, h}} {
			x, y := page(c[0], c[1])
			quad = append(quad, [2]float64{x, y})
		}
		if !polygonsIntersect(quad, r.clipPoly) {
			return
		}
	}

	r.flush()
	r.ctx.Push()
	r.ctx.ComposeView(canvas.Matrix{{xi - x0, xk - x0, x0}, {yi - y0, yk - y0, y0}})
	r.ctx.DrawImage(0, 0, img, canvas.DPMM(1))
	r.ctx.Pop()
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
