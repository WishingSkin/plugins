// Package skinpng implements deterministic Minecraft skin image transforms and 2D paper-doll rendering.
package skinpng

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
)

// DecodePNG decodes a PNG byte slice into a mutable NRGBA image.
func DecodePNG(raw []byte) (*image.NRGBA, error) {
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return ToNRGBA(img), nil
}

// EncodePNG encodes an image into PNG bytes.
func EncodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ValidateSkinDimensions validates standard or HD Minecraft skin geometry (64..1024 power-of-two width, 1:1 or 2:1).
func ValidateSkinDimensions(w, h int) error {
	if w < 64 || w > 1024 || (w&(w-1)) != 0 || (h != w && h != w/2) {
		return errors.New("皮肤尺寸须为 64 到 1024 之间 2 的幂宽度且比例为 1:1 或 2:1")
	}
	return nil
}

// ToNRGBA clones img into an image.NRGBA with origin (0, 0).
func ToNRGBA(img image.Image) *image.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			dst.SetNRGBA(x, y, color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA))
		}
	}
	return dst
}

// Upgrade64x32To64x64 upgrades a 2:1 legacy skin (e.g. 64x32, 128x64) to square 1:1 format,
// mirroring Right Leg -> Left Leg and Right Arm -> Left Arm across all 6 cuboid faces.
func Upgrade64x32To64x64(src *image.NRGBA) *image.NRGBA {
	w := src.Bounds().Dx()
	h := src.Bounds().Dy()
	if h == w {
		return ToNRGBA(src)
	}
	s := max(1, w/64)
	dst := image.NewNRGBA(image.Rect(0, 0, w, w))
	copyRect(dst, src, 0, 0, 0, 0, w, h)

	// Right leg (0,16) -> Left leg (16,48)
	copyRectFlipH(dst, src, 20, 48, 4, 16, 4, 4, s)   // Top
	copyRectFlipH(dst, src, 24, 48, 8, 16, 4, 4, s)   // Bottom
	copyRectFlipH(dst, src, 24, 52, 0, 20, 4, 12, s)  // Outer
	copyRectFlipH(dst, src, 20, 52, 4, 20, 4, 12, s)  // Front
	copyRectFlipH(dst, src, 16, 52, 8, 20, 4, 12, s)  // Inner
	copyRectFlipH(dst, src, 28, 52, 12, 20, 4, 12, s) // Back

	// Right arm (40,16) -> Left arm (32,48)
	copyRectFlipH(dst, src, 36, 48, 44, 16, 4, 4, s)  // Top
	copyRectFlipH(dst, src, 40, 48, 48, 16, 4, 4, s)  // Bottom
	copyRectFlipH(dst, src, 40, 52, 40, 20, 4, 12, s) // Outer
	copyRectFlipH(dst, src, 36, 52, 44, 20, 4, 12, s) // Front
	copyRectFlipH(dst, src, 32, 52, 48, 20, 4, 12, s) // Inner
	copyRectFlipH(dst, src, 44, 52, 52, 20, 4, 12, s) // Back
	return dst
}

// ConvertArmModel converts arm UVs between Classic (4px) and Slim/Alex (3px) across all 4 arm regions.
func ConvertArmModel(src *image.NRGBA, toSlim bool) *image.NRGBA {
	sq := Upgrade64x32To64x64(src)
	w := sq.Bounds().Dx()
	s := max(1, w/64)
	dst := ToNRGBA(sq)
	for _, box := range [][2]int{{40, 16}, {40, 32}, {32, 48}, {48, 48}} {
		convertArmBox(dst, sq, box[0], box[1], s, toSlim)
	}
	return dst
}

func convertArmBox(dst, src *image.NRGBA, x0, y0, s int, toSlim bool) {
	clearRect(dst, x0*s, y0*s, 16*s, 16*s)
	// Outer face is 4x12 in both classic and slim models.
	copyRect(dst, src, x0*s, (y0+4)*s, x0*s, (y0+4)*s, 4*s, 12*s)

	if toSlim {
		cols := []int{0, 1, 3}
		mapCols(dst, src, x0+4, y0+4, x0+4, y0+4, cols, 12, s)       // Front 4->3
		mapCols(dst, src, x0+4, y0, x0+4, y0, cols, 4, s)            // Top 4->3
		mapCols(dst, src, x0+7, y0, x0+8, y0, cols, 4, s)            // Bottom 4->3
		copyRect(dst, src, (x0+7)*s, (y0+4)*s, (x0+8)*s, (y0+4)*s, 4*s, 12*s) // Inner shifted left
		mapCols(dst, src, x0+11, y0+4, x0+12, y0+4, cols, 12, s)     // Back 4->3
	} else {
		cols := []int{0, 1, 1, 2}
		mapCols(dst, src, x0+4, y0+4, x0+4, y0+4, cols, 12, s)       // Front 3->4
		mapCols(dst, src, x0+4, y0, x0+4, y0, cols, 4, s)            // Top 3->4
		mapCols(dst, src, x0+8, y0, x0+7, y0, cols, 4, s)            // Bottom 3->4
		copyRect(dst, src, (x0+8)*s, (y0+4)*s, (x0+7)*s, (y0+4)*s, 4*s, 12*s) // Inner shifted right
		mapCols(dst, src, x0+12, y0+4, x0+11, y0+4, cols, 12, s)     // Back 3->4
	}
}

func mapCols(dst, src *image.NRGBA, dx, dy, sx, sy int, srcCols []int, vh, s int) {
	for cIdx, srcCol := range srcCols {
		copyRect(dst, src, (dx+cIdx)*s, dy*s, (sx+srcCol)*s, sy*s, s, vh*s)
	}
}

// ScaleNearest resizes src using nearest-neighbor sampling to preserve crisp pixel edges.
func ScaleNearest(src *image.NRGBA, dstW, dstH int) *image.NRGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	if sw == 0 || sh == 0 || dstW == 0 || dstH == 0 {
		return dst
	}
	for y := range dstH {
		sy := (y * sh) / dstH
		for x := range dstW {
			sx := (x * sw) / dstW
			dst.SetNRGBA(x, y, src.NRGBAAt(sb.Min.X+sx, sb.Min.Y+sy))
		}
	}
	return dst
}

// CompositeOverlay blends overlay on top of base using Porter-Duff Over with scaled opacity (0..1).
func CompositeOverlay(base, overlay *image.NRGBA, opacity float64) *image.NRGBA {
	w, h := base.Bounds().Dx(), base.Bounds().Dy()
	dst := ToNRGBA(base)
	ov := overlay
	if ov.Bounds().Dx() != w || ov.Bounds().Dy() != h {
		ov = ScaleNearest(ov, w, h)
	}
	opacity = min(1, max(0, opacity))
	if opacity == 0 {
		return dst
	}
	for y := range h {
		for x := range w {
			sc := ov.NRGBAAt(x, y)
			if sc.A == 0 {
				continue
			}
			sc.A = uint8(math.Round(float64(sc.A) * opacity))
			if sc.A == 0 {
				continue
			}
			blendOver(dst, x, y, sc)
		}
	}
	return dst
}

// ClearHatLayer zeroes the secondary Hat UV region [32..63, 0..15] scaled to skin resolution.
func ClearHatLayer(src *image.NRGBA) *image.NRGBA {
	dst := ToNRGBA(src)
	s := max(1, dst.Bounds().Dx()/64)
	clearRect(dst, 32*s, 0, 32*s, 16*s)
	return dst
}

// StripOuterLayers zeroes the Hat region plus all secondary body/limb overlay boxes if square.
func StripOuterLayers(src *image.NRGBA) *image.NRGBA {
	dst := ClearHatLayer(src)
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	if h < w {
		return dst
	}
	s := max(1, w/64)
	clearRect(dst, 0, 32*s, 56*s, 16*s)
	clearRect(dst, 0, 48*s, 16*s, 16*s)
	clearRect(dst, 48*s, 48*s, 16*s, 16*s)
	return dst
}

// AdjustColors applies HSL hue/saturation/brightness adjustments and optional grayscale conversion while preserving alpha.
func AdjustColors(src *image.NRGBA, hueDeg, satPct, brightPct float64, grayscale bool) *image.NRGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dst := ToNRGBA(src)
	satFactor := max(0, 1.0+satPct/100.0)
	brightShift := brightPct / 100.0

	for y := range h {
		for x := range w {
			c := dst.NRGBAAt(x, y)
			if c.A == 0 {
				continue
			}
			if grayscale {
				luma := uint8(math.Round(0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)))
				c.R, c.G, c.B = luma, luma, luma
			}
			hue, s, l := rgbToHSL(c.R, c.G, c.B)
			if hueDeg != 0 && !grayscale {
				hue = math.Mod(hue+hueDeg, 360)
				if hue < 0 {
					hue += 360
				}
			}
			if !grayscale {
				s = min(1, max(0, s*satFactor))
			}
			l = min(1, max(0, l+brightShift))
			r, g, b := hslToRGB(hue, s, l)
			dst.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: c.A})
		}
	}
	return dst
}

// CleanAlphaNoise resets faint semi-transparent fringe pixels (0 < A < threshold) to fully transparent.
func CleanAlphaNoise(src *image.NRGBA, threshold uint8) *image.NRGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dst := ToNRGBA(src)
	for y := range h {
		for x := range w {
			c := dst.NRGBAAt(x, y)
			if c.A > 0 && c.A < threshold {
				dst.SetNRGBA(x, y, color.NRGBA{})
			}
		}
	}
	return dst
}

// RenderHead renders a crisp square avatar from the skin's front head face + optional hat overlay.
func RenderHead(skin *image.NRGBA, size int, withHat bool) *image.NRGBA {
	if size < 8 {
		size = 8
	}
	small := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	sw := skin.Bounds().Dx()
	scale := max(1, sw/64)
	blitScaledOver(small, skin, 0, 0, 8, 8, 8*scale, 8*scale, 8*scale, 8*scale, false)
	if withHat {
		blitScaledOver(small, skin, 0, 0, 8, 8, 40*scale, 8*scale, 8*scale, 8*scale, false)
	}
	if size == 8 {
		return small
	}
	return ScaleNearest(small, size, size)
}

// RenderBodyFront renders a crisp 16x32 paper-doll front view scaled by pixelScale.
func RenderBodyFront(skin *image.NRGBA, pixelScale int, slim bool, withOuter bool) *image.NRGBA {
	return renderBody(skin, pixelScale, slim, withOuter, false)
}

// RenderBodyBack renders a crisp 16x32 paper-doll back view scaled by pixelScale.
func RenderBodyBack(skin *image.NRGBA, pixelScale int, slim bool, withOuter bool) *image.NRGBA {
	return renderBody(skin, pixelScale, slim, withOuter, true)
}

func renderBody(skin *image.NRGBA, pixelScale int, slim, withOuter, back bool) *image.NRGBA {
	if pixelScale < 1 {
		pixelScale = 1
	}
	sq := Upgrade64x32To64x64(skin)
	s := max(1, sq.Bounds().Dx()/64)
	armW := 4
	if slim {
		armW = 3
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, 16*s, 32*s))

	if !back {
		// Head + Hat
		blitScaledOver(canvas, sq, 4*s, 0, 8*s, 8*s, 8*s, 8*s, 8*s, 8*s, false)
		if withOuter {
			blitScaledOver(canvas, sq, 4*s, 0, 8*s, 8*s, 40*s, 8*s, 8*s, 8*s, false)
		}
		// Torso + Jacket
		blitScaledOver(canvas, sq, 4*s, 8*s, 8*s, 12*s, 20*s, 20*s, 8*s, 12*s, false)
		if withOuter {
			blitScaledOver(canvas, sq, 4*s, 8*s, 8*s, 12*s, 20*s, 36*s, 8*s, 12*s, false)
		}
		// Right arm (viewer's left)
		blitScaledOver(canvas, sq, (4-armW)*s, 8*s, armW*s, 12*s, 44*s, 20*s, armW*s, 12*s, false)
		if withOuter {
			blitScaledOver(canvas, sq, (4-armW)*s, 8*s, armW*s, 12*s, 44*s, 36*s, armW*s, 12*s, false)
		}
		// Left arm (viewer's right)
		blitScaledOver(canvas, sq, 12*s, 8*s, armW*s, 12*s, 36*s, 52*s, armW*s, 12*s, false)
		if withOuter {
			blitScaledOver(canvas, sq, 12*s, 8*s, armW*s, 12*s, 52*s, 52*s, armW*s, 12*s, false)
		}
		// Right leg + Pants (viewer's left)
		blitScaledOver(canvas, sq, 4*s, 20*s, 4*s, 12*s, 4*s, 20*s, 4*s, 12*s, false)
		if withOuter {
			blitScaledOver(canvas, sq, 4*s, 20*s, 4*s, 12*s, 4*s, 36*s, 4*s, 12*s, false)
		}
		// Left leg + Pants (viewer's right)
		blitScaledOver(canvas, sq, 8*s, 20*s, 4*s, 12*s, 20*s, 52*s, 4*s, 12*s, false)
		if withOuter {
			blitScaledOver(canvas, sq, 8*s, 20*s, 4*s, 12*s, 4*s, 52*s, 4*s, 12*s, false)
		}
	} else {
		rArmBackX := 52
		lArmBackX := 44
		lSleeveBackX := 60
		if slim {
			rArmBackX = 51
			lArmBackX = 43
			lSleeveBackX = 59
		}
		// Head + Hat back
		blitScaledOver(canvas, sq, 4*s, 0, 8*s, 8*s, 24*s, 8*s, 8*s, 8*s, false)
		if withOuter {
			blitScaledOver(canvas, sq, 4*s, 0, 8*s, 8*s, 56*s, 8*s, 8*s, 8*s, false)
		}
		// Torso + Jacket back
		blitScaledOver(canvas, sq, 4*s, 8*s, 8*s, 12*s, 32*s, 20*s, 8*s, 12*s, false)
		if withOuter {
			blitScaledOver(canvas, sq, 4*s, 8*s, 8*s, 12*s, 32*s, 36*s, 8*s, 12*s, false)
		}
		// Left arm back (viewer's left)
		blitScaledOver(canvas, sq, (4-armW)*s, 8*s, armW*s, 12*s, lArmBackX*s, 52*s, armW*s, 12*s, false)
		if withOuter {
			blitScaledOver(canvas, sq, (4-armW)*s, 8*s, armW*s, 12*s, lSleeveBackX*s, 52*s, armW*s, 12*s, false)
		}
		// Right arm back (viewer's right)
		blitScaledOver(canvas, sq, 12*s, 8*s, armW*s, 12*s, rArmBackX*s, 20*s, armW*s, 12*s, false)
		if withOuter {
			blitScaledOver(canvas, sq, 12*s, 8*s, armW*s, 12*s, rArmBackX*s, 36*s, armW*s, 12*s, false)
		}
		// Left leg back (viewer's left)
		blitScaledOver(canvas, sq, 4*s, 20*s, 4*s, 12*s, 28*s, 52*s, 4*s, 12*s, false)
		if withOuter {
			blitScaledOver(canvas, sq, 4*s, 20*s, 4*s, 12*s, 12*s, 52*s, 4*s, 12*s, false)
		}
		// Right leg back (viewer's right)
		blitScaledOver(canvas, sq, 8*s, 20*s, 4*s, 12*s, 12*s, 20*s, 4*s, 12*s, false)
		if withOuter {
			blitScaledOver(canvas, sq, 8*s, 20*s, 4*s, 12*s, 12*s, 36*s, 4*s, 12*s, false)
		}
	}

	if pixelScale == s {
		return canvas
	}
	return ScaleNearest(canvas, 16*pixelScale, 32*pixelScale)
}

func clearRect(img *image.NRGBA, x, y, w, h int) {
	b := img.Bounds()
	for py := range h {
		yy := y + py
		if yy < b.Min.Y || yy >= b.Max.Y {
			continue
		}
		for px := range w {
			xx := x + px
			if xx < b.Min.X || xx >= b.Max.X {
				continue
			}
			img.SetNRGBA(xx, yy, color.NRGBA{})
		}
	}
}

func copyRect(dst, src *image.NRGBA, dx, dy, sx, sy, w, h int) {
	for py := range h {
		for px := range w {
			dst.SetNRGBA(dx+px, dy+py, src.NRGBAAt(sx+px, sy+py))
		}
	}
}

func copyRectFlipH(dst, src *image.NRGBA, dx, dy, sx, sy, cw, ch, s int) {
	w, h := cw*s, ch*s
	dxs, dys := dx*s, dy*s
	sxs, sys := sx*s, sy*s
	for py := range h {
		for px := range w {
			dst.SetNRGBA(dxs+w-1-px, dys+py, src.NRGBAAt(sxs+px, sys+py))
		}
	}
}

func blitScaledOver(dst, src *image.NRGBA, dx, dy, dw, dh, sx, sy, sw, sh int, flipH bool) {
	if dw <= 0 || dh <= 0 || sw <= 0 || sh <= 0 {
		return
	}
	for py := range dh {
		uy := sy + (py*sh)/dh
		for px := range dw {
			lx := (px * sw) / dw
			if flipH {
				lx = sw - 1 - lx
			}
			c := src.NRGBAAt(sx+lx, uy)
			if c.A == 0 {
				continue
			}
			blendOver(dst, dx+px, dy+py, c)
		}
	}
}

func blendOver(dst *image.NRGBA, x, y int, sc color.NRGBA) {
	if sc.A == 255 {
		dst.SetNRGBA(x, y, sc)
		return
	}
	dc := dst.NRGBAAt(x, y)
	if dc.A == 0 {
		dst.SetNRGBA(x, y, sc)
		return
	}
	sa := float64(sc.A) / 255.0
	da := float64(dc.A) / 255.0
	outA := sa + da*(1-sa)
	if outA <= 0 {
		dst.SetNRGBA(x, y, color.NRGBA{})
		return
	}
	r := (float64(sc.R)*sa + float64(dc.R)*da*(1-sa)) / outA
	g := (float64(sc.G)*sa + float64(dc.G)*da*(1-sa)) / outA
	b := (float64(sc.B)*sa + float64(dc.B)*da*(1-sa)) / outA
	dst.SetNRGBA(x, y, color.NRGBA{
		R: uint8(min(255, max(0, math.Round(r)))),
		G: uint8(min(255, max(0, math.Round(g)))),
		B: uint8(min(255, max(0, math.Round(b)))),
		A: uint8(min(255, max(0, math.Round(outA*255)))),
	})
}

func rgbToHSL(r8, g8, b8 uint8) (float64, float64, float64) {
	r := float64(r8) / 255.0
	g := float64(g8) / 255.0
	b := float64(b8) / 255.0
	mx := max(r, max(g, b))
	mn := min(r, min(g, b))
	l := (mx + mn) / 2.0
	if mx == mn {
		return 0, 0, l
	}
	d := mx - mn
	s := d / (1 - math.Abs(2*l-1))
	var h float64
	switch mx {
	case r:
		h = 60 * math.Mod((g-b)/d, 6)
	case g:
		h = 60 * ((b-r)/d + 2)
	default:
		h = 60 * ((r-g)/d + 4)
	}
	if h < 0 {
		h += 360
	}
	return h, min(1, max(0, s)), l
}

func hslToRGB(h, s, l float64) (uint8, uint8, uint8) {
	if s == 0 {
		v := uint8(math.Round(l * 255))
		return v, v, v
	}
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r1, g1, b1 float64
	switch {
	case h < 60:
		r1, g1, b1 = c, x, 0
	case h < 120:
		r1, g1, b1 = x, c, 0
	case h < 180:
		r1, g1, b1 = 0, c, x
	case h < 240:
		r1, g1, b1 = 0, x, c
	case h < 300:
		r1, g1, b1 = x, 0, c
	default:
		r1, g1, b1 = c, 0, x
	}
	return uint8(min(255, max(0, math.Round((r1+m)*255)))),
		uint8(min(255, max(0, math.Round((g1+m)*255)))),
		uint8(min(255, max(0, math.Round((b1+m)*255))))
}
