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
		{"santa_hat", "圣诞帽", "节日", makeSantaHat},
		{"pumpkin_band", "万圣节南瓜发箍", "节日", makePumpkinBand},
		{"golden_crown", "黄金王冠", "头饰", makeGoldenCrown},
		{"cat_ears", "猫耳发箍", "头饰", makeCatEars},
		{"cyber_visor", "赛博护目镜", "面部", makeCyberVisor},
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

func makeSantaHat() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	red := color.NRGBA{R: 205, G: 38, B: 46, A: 255}
	darkRed := color.NRGBA{R: 168, G: 26, B: 34, A: 255}
	trim := color.NRGBA{R: 245, G: 245, B: 248, A: 255}
	pom := color.NRGBA{R: 255, G: 255, B: 255, A: 255}

	// Hat top + upper sides
	fillOverlayRect(img, 40, 0, 8, 8, red)
	fillOverlayRect(img, 42, 2, 4, 4, darkRed)
	for _, x0 := range []int{32, 40, 48, 56} {
		fillOverlayRect(img, x0, 8, 8, 2, red)
		fillOverlayRect(img, x0, 10, 8, 1, trim)
	}
	// Side hanging tail & white pom
	fillOverlayRect(img, 48, 10, 3, 2, red)
	fillOverlayRect(img, 49, 12, 2, 2, pom)
	return img
}

func makePumpkinBand() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	band := color.NRGBA{R: 38, G: 34, B: 48, A: 255}
	pumpkin := color.NRGBA{R: 238, G: 118, B: 28, A: 255}
	ridge := color.NRGBA{R: 204, G: 92, B: 18, A: 255}
	stem := color.NRGBA{R: 72, G: 152, B: 56, A: 255}

	for _, x0 := range []int{32, 40, 48, 56} {
		fillOverlayRect(img, x0, 9, 8, 1, band)
	}
	// Mini pumpkin centerpiece on front Hat
	fillOverlayRect(img, 42, 8, 4, 2, pumpkin)
	img.SetNRGBA(43, 8, ridge)
	img.SetNRGBA(44, 8, ridge)
	fillOverlayRect(img, 43, 6, 2, 2, stem)
	return img
}

func makeGoldenCrown() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	gold := color.NRGBA{R: 245, G: 196, B: 42, A: 255}
	brightGold := color.NRGBA{R: 255, G: 224, B: 92, A: 255}
	ruby := color.NRGBA{R: 220, G: 38, B: 68, A: 255}
	emerald := color.NRGBA{R: 34, G: 197, B: 94, A: 255}

	for _, x0 := range []int{32, 40, 48, 56} {
		fillOverlayRect(img, x0, 9, 8, 2, gold)
		for dx := 0; dx < 8; dx += 2 {
			img.SetNRGBA(x0+dx, 8, brightGold)
		}
	}
	img.SetNRGBA(43, 10, ruby)
	img.SetNRGBA(44, 10, ruby)
	img.SetNRGBA(35, 10, emerald)
	img.SetNRGBA(51, 10, emerald)
	return img
}

func makeCatEars() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	fur := color.NRGBA{R: 42, G: 44, B: 54, A: 255}
	earPink := color.NRGBA{R: 244, G: 143, B: 177, A: 255}

	// Headband across Hat top/front
	fillOverlayRect(img, 40, 9, 8, 1, fur)
	fillOverlayRect(img, 32, 9, 8, 1, fur)
	fillOverlayRect(img, 48, 9, 8, 1, fur)
	// Left & Right ears on Hat front/top corners
	fillOverlayRect(img, 40, 7, 2, 2, fur)
	img.SetNRGBA(41, 8, earPink)
	fillOverlayRect(img, 46, 7, 2, 2, fur)
	img.SetNRGBA(46, 8, earPink)
	return img
}

func makeCyberVisor() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	frame := color.NRGBA{R: 28, G: 36, B: 48, A: 255}
	glass := color.NRGBA{R: 34, G: 211, B: 238, A: 185}
	glow := color.NRGBA{R: 165, G: 243, B: 252, A: 235}

	// Front glass across eyes + thin side straps
	fillOverlayRect(img, 40, 11, 8, 2, glass)
	img.SetNRGBA(41, 11, glow)
	img.SetNRGBA(42, 11, glow)
	fillOverlayRect(img, 36, 11, 4, 1, frame)
	fillOverlayRect(img, 48, 11, 4, 1, frame)
	return img
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
