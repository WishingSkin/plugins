package main

import (
	"encoding/base64"
	"image"
	"image/color"

	"github.com/WishingSkin/plugins/shared/skinpng"
)

type presetOverlay struct {
	ID             string
	Name           string
	Category       string
	Image          *image.NRGBA
	PreviewDataURL string
}

var builtinPresets = buildBuiltinPresets()

func buildBuiltinPresets() []presetOverlay {
	defs := []struct {
		id       string
		name     string
		category string
		build    func() *image.NRGBA
	}{
		{"winter_scarf", "冬日红围巾", "服饰", makeWinterScarf},
	}

	out := make([]presetOverlay, 0, len(defs))
	for _, d := range defs {
		img := d.build()
		out = append(out, presetOverlay{
			ID:             d.id,
			Name:           d.name,
			Category:       d.category,
			Image:          img,
			PreviewDataURL: renderOverlayPreviewDataURL(img),
		})
	}
	return out
}

func renderOverlayPreviewDataURL(overlay *image.NRGBA) string {
	mannequin := newMannequinBase()
	comp := skinpng.CompositeOverlay(mannequin, overlay, 1.0)
	preview := skinpng.RenderBodyFront(comp, 3, false, true)
	raw, err := skinpng.EncodePNG(preview)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
}

func newMannequinBase() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	skinTone := color.NRGBA{R: 214, G: 218, B: 226, A: 255}
	shadeTone := color.NRGBA{R: 184, G: 190, B: 202, A: 255}
	eyeTone := color.NRGBA{R: 92, G: 102, B: 120, A: 255}

	fillBox := func(x, y, w, h int, c color.NRGBA) {
		for py := range h {
			for px := range w {
				img.SetNRGBA(x+px, y+py, c)
			}
		}
	}
	// Head & eyes
	fillBox(8, 8, 8, 8, skinTone)
	img.SetNRGBA(9, 12, eyeTone)
	img.SetNRGBA(14, 12, eyeTone)
	// Torso
	fillBox(20, 20, 8, 12, shadeTone)
	// Right & Left arms
	fillBox(44, 20, 4, 12, skinTone)
	fillBox(36, 52, 4, 12, skinTone)
	// Right & Left legs
	fillBox(4, 20, 4, 12, shadeTone)
	fillBox(20, 52, 4, 12, shadeTone)
	return img
}

func fillOverlayRect(img *image.NRGBA, x, y, w, h int, c color.NRGBA) {
	for py := range h {
		for px := range w {
			img.SetNRGBA(x+px, y+py, c)
		}
	}
}

func makeWinterScarf() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	scarfRed := color.NRGBA{R: 196, G: 44, B: 52, A: 255}
	scarfCream := color.NRGBA{R: 245, G: 234, B: 214, A: 255}

	// Neck ring around Jacket outer layer [16..39, 32..47]
	for _, seg := range [][2]int{{16, 4}, {20, 8}, {28, 4}, {32, 8}} {
		fillOverlayRect(img, seg[0], 36, seg[1], 2, scarfRed)
		for dx := 0; dx < seg[1]; dx += 2 {
			img.SetNRGBA(seg[0]+dx, 37, scarfCream)
		}
	}
	// Hanging front tail
	fillOverlayRect(img, 22, 38, 2, 5, scarfRed)
	img.SetNRGBA(22, 42, scarfCream)
	img.SetNRGBA(23, 42, scarfCream)
	return img
}
