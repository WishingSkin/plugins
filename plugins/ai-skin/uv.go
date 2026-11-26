package main

import (
	"errors"
	"image"
	"image/color"

	"github.com/WishingSkin/plugins/shared/skinpng"
)

type uvRect struct {
	X, Y, W, H int
}

// SanitizeAndValidateSkin normalizes an arbitrary generated image into a strict 64x64 Minecraft skin,
// strips unused dead-zone alpha, cleans outer-layer translucency, fills sparse base holes, and verifies
// minimum geometry coverage so invalid outputs (e.g. blank images or off-grid illustrations) do not bill points.
func SanitizeAndValidateSkin(src *image.NRGBA, slim bool) (*image.NRGBA, error) {
	if src == nil {
		return nil, errors.New("生成图像为空（未扣费）")
	}
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw < 32 || sh < 16 {
		return nil, errors.New("生成图像尺寸过小，无法作为 Minecraft 皮肤（未扣费）")
	}

	var work *image.NRGBA
	switch {
	case sw == 64 && sh == 64:
		work = skinpng.ToNRGBA(src)
	case sw == sh:
		work = downsampleCenter(src, 64, 64)
	case sh*2 == sw:
		if sw > 64 {
			src = downsampleCenter(src, 64, 32)
		}
		work = skinpng.Upgrade64x32To64x64(src)
	default:
		return nil, errors.New("生成图像比例不符合 1:1 或 2:1 Minecraft UV 规范（未扣费）")
	}

	baseRects, outerRects := minecraftUVRects(slim)
	allowed := make([][]bool, 64)
	baseMask := make([][]bool, 64)
	for y := range 64 {
		allowed[y] = make([]bool, 64)
		baseMask[y] = make([]bool, 64)
	}
	for _, r := range baseRects {
		markRect(allowed, r)
		markRect(baseMask, r)
	}
	for _, r := range outerRects {
		markRect(allowed, r)
	}

	// Clear dead-zone pixels outside valid UV regions and binarize outer layer alpha.
	cleaned := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			if !allowed[y][x] {
				continue
			}
			c := work.NRGBAAt(x, y)
			if !baseMask[y][x] {
				if c.A < 128 {
					continue
				}
				c.A = 255
			}
			cleaned.SetNRGBA(x, y, c)
		}
	}

	// Validate base layer opaque coverage, Head Front presence, and color diversity BEFORE hole filling.
	var baseTotal, baseOpaque int
	var headFrontTotal, headFrontOpaque int
	colors := map[uint32]struct{}{}
	for _, r := range baseRects {
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				baseTotal++
				c := cleaned.NRGBAAt(x, y)
				isHeadFront := x >= 8 && x < 16 && y >= 8 && y < 16
				if isHeadFront {
					headFrontTotal++
				}
				if c.A >= 32 {
					baseOpaque++
					if isHeadFront {
						headFrontOpaque++
					}
					if len(colors) <= 4 {
						q := (uint32(c.R>>4) << 8) | (uint32(c.G>>4) << 4) | uint32(c.B>>4)
						colors[q] = struct{}{}
					}
				}
			}
		}
	}

	if baseTotal == 0 || float64(baseOpaque)/float64(baseTotal) < 0.35 {
		return nil, errors.New("生成结果缺少有效的皮肤基础图层（未扣费）")
	}
	if headFrontTotal == 0 || float64(headFrontOpaque)/float64(headFrontTotal) < 0.50 {
		return nil, errors.New("生成结果未能识别出完整头部正面区域（未扣费）")
	}
	if len(colors) <= 2 {
		return nil, errors.New("生成结果色彩过于单一，未能构成有效皮肤细节（未扣费）")
	}

	// Fill sparse holes inside base-layer cuboids and force base opacity to 255.
	for _, r := range baseRects {
		var sumR, sumG, sumB, count int64
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				c := cleaned.NRGBAAt(x, y)
				if c.A >= 32 {
					sumR += int64(c.R)
					sumG += int64(c.G)
					sumB += int64(c.B)
					count++
				}
			}
		}
		fallback := color.NRGBA{R: 224, G: 184, B: 152, A: 255}
		if count > 0 {
			fallback = color.NRGBA{
				R: uint8(sumR / count),
				G: uint8(sumG / count),
				B: uint8(sumB / count),
				A: 255,
			}
		}
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				c := cleaned.NRGBAAt(x, y)
				if c.A < 32 {
					cleaned.SetNRGBA(x, y, fallback)
				} else if c.A < 255 {
					c.A = 255
					cleaned.SetNRGBA(x, y, c)
				}
			}
		}
	}

	return cleaned, nil
}

// BuildUVGuidePNG creates a crisp 1024x1024 color-coded Minecraft UV layout guide for image-edit endpoints.
func BuildUVGuidePNG(slim bool) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	baseRects, outerRects := minecraftUVRects(slim)
	fill := func(r uvRect, c color.NRGBA) {
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				img.SetNRGBA(x, y, c)
			}
		}
	}
	palette := []color.NRGBA{
		{242, 200, 168, 255}, // Head (0..5)
		{186, 148, 120, 255},
		{102, 168, 214, 255}, // Torso
		{76, 134, 178, 255},
		{110, 186, 138, 255}, // R-Arm
		{84, 152, 110, 255},
		{110, 186, 138, 255}, // L-Arm
		{84, 152, 110, 255},
		{112, 118, 176, 255}, // R-Leg
		{88, 94, 146, 255},
		{112, 118, 176, 255}, // L-Leg
		{88, 94, 146, 255},
	}
	for idx, r := range baseRects {
		partIdx := idx / 6
		cIdx := partIdx*2 + (idx % 2)
		if cIdx >= len(palette) {
			cIdx = 0
		}
		fill(r, palette[cIdx])
	}
	// Mark eyes on Head Front & neckline so edit models align character features.
	img.SetNRGBA(9, 12, color.NRGBA{255, 255, 255, 255})
	img.SetNRGBA(10, 12, color.NRGBA{48, 86, 168, 255})
	img.SetNRGBA(13, 12, color.NRGBA{48, 86, 168, 255})
	img.SetNRGBA(14, 12, color.NRGBA{255, 255, 255, 255})
	img.SetNRGBA(23, 20, color.NRGBA{242, 200, 168, 255})
	img.SetNRGBA(24, 20, color.NRGBA{242, 200, 168, 255})
	for _, r := range outerRects {
		fill(r, color.NRGBA{210, 214, 224, 96})
	}
	scaled := skinpng.ScaleNearest(img, 1024, 1024)
	b, _ := skinpng.EncodePNG(scaled)
	return b
}

// UpscaleSkinForEditPNG upscales an existing 64x64 skin nearest-neighbor to 1024x1024 for /images/edits refinement.
func UpscaleSkinForEditPNG(skin *image.NRGBA) []byte {
	if skin == nil {
		return nil
	}
	sq := skinpng.Upgrade64x32To64x64(skin)
	scaled := skinpng.ScaleNearest(sq, 1024, 1024)
	b, _ := skinpng.EncodePNG(scaled)
	return b
}

func downsampleCenter(src *image.NRGBA, dstW, dstH int) *image.NRGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	for y := range dstH {
		sy := min(sh-1, (y*sh+sh/2)/dstH)
		for x := range dstW {
			sx := min(sw-1, (x*sw+sw/2)/dstW)
			dst.SetNRGBA(x, y, src.NRGBAAt(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}

func markRect(grid [][]bool, r uvRect) {
	for y := r.Y; y < r.Y+r.H && y < len(grid); y++ {
		if y < 0 {
			continue
		}
		for x := r.X; x < r.X+r.W && x < len(grid[y]); x++ {
			if x >= 0 {
				grid[y][x] = true
			}
		}
	}
}

func minecraftUVRects(slim bool) (base []uvRect, outer []uvRect) {
	armW := 4
	if slim {
		armW = 3
	}
	base = []uvRect{
		// Head (0,0)
		{8, 0, 8, 8}, {16, 0, 8, 8}, {0, 8, 8, 8}, {8, 8, 8, 8}, {16, 8, 8, 8}, {24, 8, 8, 8},
		// Right Leg (0,16)
		{4, 16, 4, 4}, {8, 16, 4, 4}, {0, 20, 4, 12}, {4, 20, 4, 12}, {8, 20, 4, 12}, {12, 20, 4, 12},
		// Torso (16,16)
		{20, 16, 8, 4}, {28, 16, 8, 4}, {16, 20, 4, 12}, {20, 20, 8, 12}, {28, 20, 4, 12}, {32, 20, 8, 12},
		// Right Arm (40,16)
		{44, 16, armW, 4}, {44 + armW, 16, armW, 4}, {40, 20, 4, 12}, {44, 20, armW, 12}, {44 + armW, 20, 4, 12}, {48 + armW, 20, armW, 12},
		// Left Leg (16,48)
		{20, 48, 4, 4}, {24, 48, 4, 4}, {16, 52, 4, 12}, {20, 52, 4, 12}, {24, 52, 4, 12}, {28, 52, 4, 12},
		// Left Arm (32,48)
		{36, 48, armW, 4}, {36 + armW, 48, armW, 4}, {32, 52, 4, 12}, {36, 52, armW, 12}, {36 + armW, 52, 4, 12}, {40 + armW, 52, armW, 12},
	}
	outer = []uvRect{
		// Hat (32,0)
		{40, 0, 8, 8}, {48, 0, 8, 8}, {32, 8, 8, 8}, {40, 8, 8, 8}, {48, 8, 8, 8}, {56, 8, 8, 8},
		// Right Pants (0,32)
		{4, 32, 4, 4}, {8, 32, 4, 4}, {0, 36, 4, 12}, {4, 36, 4, 12}, {8, 36, 4, 12}, {12, 36, 4, 12},
		// Jacket (16,32)
		{20, 32, 8, 4}, {28, 32, 8, 4}, {16, 36, 4, 12}, {20, 36, 8, 12}, {28, 36, 4, 12}, {32, 36, 8, 12},
		// Right Sleeve (40,32)
		{44, 32, armW, 4}, {44 + armW, 32, armW, 4}, {40, 36, 4, 12}, {44, 36, armW, 12}, {44 + armW, 36, 4, 12}, {48 + armW, 36, armW, 12},
		// Left Pants (0,48)
		{4, 48, 4, 4}, {8, 48, 4, 4}, {0, 52, 4, 12}, {4, 52, 4, 12}, {8, 52, 4, 12}, {12, 52, 4, 12},
		// Left Sleeve (48,48)
		{52, 48, armW, 4}, {52 + armW, 48, armW, 4}, {48, 52, 4, 12}, {52, 52, armW, 12}, {52 + armW, 52, 4, 12}, {56 + armW, 52, armW, 12},
	}
	return base, outer
}
