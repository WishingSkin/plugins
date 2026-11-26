package main

import (
	"errors"
	"image"
	"image/color"
	"math"
	"sort"

	"github.com/WishingSkin/plugins/shared/skinpng"
)

func isChromaGreenPixel(c color.NRGBA) bool {
	return c.G > 90 && int(c.G)-max(int(c.R), int(c.B)) > 35
}

func isOrthoBgBase(c color.NRGBA) bool {
	return c.A < 128 || isChromaGreenPixel(c)
}

func rgbDistance(a, b color.NRGBA) float64 {
	dr := float64(a.R) - float64(b.R)
	dg := float64(a.G) - float64(b.G)
	db := float64(a.B) - float64(b.B)
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

// LooksLikeOrthoTurnaround detects a side-by-side front+back turnaround on green/transparent backdrop.
func LooksLikeOrthoTurnaround(src *image.NRGBA) bool {
	if src == nil {
		return false
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 128 && h <= 128 {
		return false
	}

	clampX := func(x int) int { return min(w-1, max(0, x)) }
	clampY := func(y int) int { return min(h-1, max(0, y)) }

	corners := [][2]int{
		{clampX(4), clampY(4)},
		{clampX(w - 5), clampY(4)},
		{clampX(4), clampY(h - 5)},
		{clampX(w - 5), clampY(h - 5)},
	}
	bgCorners := 0
	for _, pt := range corners {
		c := src.NRGBAAt(b.Min.X+pt[0], b.Min.Y+pt[1])
		if isOrthoBgBase(c) {
			bgCorners++
		}
	}
	if bgCorners < 3 {
		return false
	}

	bgRef := src.NRGBAAt(b.Min.X+corners[0][0], b.Min.Y+corners[0][1])
	refOpaqueGreen := bgRef.A >= 128 && isChromaGreenPixel(bgRef)

	seamBg := 0
	seamTotal := 0
	xStart := max(0, w/2-2)
	xEnd := min(w-1, w/2+2)
	for y := 0; y < h; y++ {
		for x := xStart; x <= xEnd; x++ {
			c := src.NRGBAAt(b.Min.X+x, b.Min.Y+y)
			seamTotal++
			if isOrthoBgBase(c) || (refOpaqueGreen && rgbDistance(c, bgRef) < 48.0) {
				seamBg++
			}
		}
	}
	if seamTotal == 0 || float64(seamBg)/float64(seamTotal) < 0.80 {
		return false
	}

	halfHasCharacter := func(hx0, hx1 int) bool {
		minX, maxX := hx1, hx0-1
		minY, maxY := h, -1
		for y := 0; y < h; y++ {
			for x := hx0; x < hx1; x++ {
				c := src.NRGBAAt(b.Min.X+x, b.Min.Y+y)
				bg := isOrthoBgBase(c) || (refOpaqueGreen && rgbDistance(c, bgRef) < 48.0)
				if !bg {
					if x < minX {
						minX = x
					}
					if x > maxX {
						maxX = x
					}
					if y < minY {
						minY = y
					}
					if y > maxY {
						maxY = y
					}
				}
			}
		}
		return maxX-minX+1 >= 16 && maxY-minY+1 >= 32
	}

	return halfHasCharacter(0, w/2) && halfHasCharacter(w/2, w)
}

func cropNRGBA(src *image.NRGBA, x0, y0, x1, y1 int) *image.NRGBA {
	b := src.Bounds()
	x0 = max(0, min(b.Dx(), x0))
	y0 = max(0, min(b.Dy(), y0))
	x1 = max(x0, min(b.Dx(), x1))
	y1 = max(y0, min(b.Dy(), y1))
	dst := image.NewNRGBA(image.Rect(0, 0, x1-x0, y1-y0))
	for y := 0; y < y1-y0; y++ {
		for x := 0; x < x1-x0; x++ {
			dst.SetNRGBA(x, y, src.NRGBAAt(b.Min.X+x0+x, b.Min.Y+y0+y))
		}
	}
	return dst
}

func extractOrthoCharBBox(half *image.NRGBA) (*image.NRGBA, error) {
	b := half.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, errors.New("正交立绘切片为空（未扣费）")
	}

	bgRef := half.NRGBAAt(b.Min.X+min(w-1, 4), b.Min.Y+min(h-1, 4))
	isBg := make([][]bool, h)
	for y := 0; y < h; y++ {
		isBg[y] = make([]bool, w)
		for x := 0; x < w; x++ {
			c := half.NRGBAAt(b.Min.X+x, b.Min.Y+y)
			if isOrthoBgBase(c) || rgbDistance(c, bgRef) < 48.0 {
				isBg[y][x] = true
			}
		}
	}

	// Strip 1px anti-aliased green fringe halo bordering background pixels.
	halo := make([][]bool, h)
	for y := 0; y < h; y++ {
		halo[y] = make([]bool, w)
		for x := 0; x < w; x++ {
			if isBg[y][x] {
				continue
			}
			c := half.NRGBAAt(b.Min.X+x, b.Min.Y+y)
			if !(int(c.G) > int(c.R)+25 && int(c.G) > int(c.B)+25) {
				continue
			}
			if (x > 0 && isBg[y][x-1]) || (x+1 < w && isBg[y][x+1]) || (y > 0 && isBg[y-1][x]) || (y+1 < h && isBg[y+1][x]) {
				halo[y][x] = true
			}
		}
	}

	minX, maxX := w, -1
	minY, maxY := h, -1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if halo[y][x] {
				isBg[y][x] = true
			}
			if !isBg[y][x] {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX-minX+1 < 16 || maxY-minY+1 < 32 {
		return nil, errors.New("未在正交立绘中检测到完整角色轮廓（未扣费）")
	}

	cw := maxX - minX + 1
	ch := maxY - minY + 1
	out := image.NewNRGBA(image.Rect(0, 0, cw, ch))
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			if isBg[minY+y][minX+x] {
				continue
			}
			c := half.NRGBAAt(b.Min.X+minX+x, b.Min.Y+minY+y)
			c.A = 255
			out.SetNRGBA(x, y, c)
		}
	}
	return out, nil
}

func cropTightX(band *image.NRGBA) *image.NRGBA {
	b := band.Bounds()
	w, h := b.Dx(), b.Dy()
	minX, maxX := w, -1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if band.NRGBAAt(b.Min.X+x, b.Min.Y+y).A >= 128 {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
			}
		}
	}
	if maxX < minX {
		return band
	}
	return cropNRGBA(band, minX, 0, maxX+1, h)
}

func flipNRGBAHorizontal(src *image.NRGBA) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.SetNRGBA(w-1-x, y, src.NRGBAAt(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func resampleOpaque(patch *image.NRGBA, w, h int) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	b := patch.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		fillRectNRGBA(out, 0, 0, w, h, color.NRGBA{128, 128, 128, 255})
		return out
	}

	var allOpaque []color.NRGBA
	rs := make([]uint8, 0, 16)
	gs := make([]uint8, 0, 16)
	bs := make([]uint8, 0, 16)

	for y := 0; y < h; y++ {
		sy0 := max(0, min(sh-1, int(math.Floor((float64(y)+0.25)*float64(sh)/float64(h)))))
		sy1 := max(sy0+1, min(sh, int(math.Ceil((float64(y)+0.75)*float64(sh)/float64(h)))))
		for x := 0; x < w; x++ {
			sx0 := max(0, min(sw-1, int(math.Floor((float64(x)+0.25)*float64(sw)/float64(w)))))
			sx1 := max(sx0+1, min(sw, int(math.Ceil((float64(x)+0.75)*float64(sw)/float64(w)))))

			rs = rs[:0]
			gs = gs[:0]
			bs = bs[:0]
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					c := patch.NRGBAAt(b.Min.X+sx, b.Min.Y+sy)
					if c.A >= 128 {
						rs = append(rs, c.R)
						gs = append(gs, c.G)
						bs = append(bs, c.B)
						allOpaque = append(allOpaque, color.NRGBA{c.R, c.G, c.B, 255})
					}
				}
			}
			if len(rs) > 0 {
				sort.Slice(rs, func(i, j int) bool { return rs[i] < rs[j] })
				sort.Slice(gs, func(i, j int) bool { return gs[i] < gs[j] })
				sort.Slice(bs, func(i, j int) bool { return bs[i] < bs[j] })
				mid := len(rs) / 2
				out.SetNRGBA(x, y, color.NRGBA{rs[mid], gs[mid], bs[mid], 255})
			}
		}
	}

	fallback := color.NRGBA{160, 150, 145, 255}
	if len(allOpaque) > 0 {
		ar := make([]uint8, len(allOpaque))
		ag := make([]uint8, len(allOpaque))
		ab := make([]uint8, len(allOpaque))
		for i, c := range allOpaque {
			ar[i], ag[i], ab[i] = c.R, c.G, c.B
		}
		sort.Slice(ar, func(i, j int) bool { return ar[i] < ar[j] })
		sort.Slice(ag, func(i, j int) bool { return ag[i] < ag[j] })
		sort.Slice(ab, func(i, j int) bool { return ab[i] < ab[j] })
		mid := len(allOpaque) / 2
		fallback = color.NRGBA{ar[mid], ag[mid], ab[mid], 255}
	}

	// Fill any edge transparent pixel from nearest opaque pixel in the same row, else median.
	for y := 0; y < h; y++ {
		var rowOpaqueX []int
		for x := 0; x < w; x++ {
			if out.NRGBAAt(x, y).A >= 128 {
				rowOpaqueX = append(rowOpaqueX, x)
			}
		}
		for x := 0; x < w; x++ {
			c := out.NRGBAAt(x, y)
			if c.A < 128 {
				if len(rowOpaqueX) > 0 {
					bestX := rowOpaqueX[0]
					bestD := absInt(bestX - x)
					for _, rx := range rowOpaqueX[1:] {
						if d := absInt(rx - x); d < bestD {
							bestD = d
							bestX = rx
						}
					}
					c = out.NRGBAAt(bestX, y)
				} else {
					c = fallback
				}
			}
			c.A = 255
			out.SetNRGBA(x, y, c)
		}
	}
	return out
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// ProjectOrthoTurnaroundToSkin converts a front+back orthographic turnaround sheet into a crisp 64x64 skin.
func ProjectOrthoTurnaroundToSkin(src *image.NRGBA, slim bool, maxColors int) (*image.NRGBA, error) {
	if src == nil {
		return nil, errors.New("正交立绘输入为空（未扣费）")
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 32 || h < 32 {
		return nil, errors.New("正交立绘分辨率过低（未扣费）")
	}

	frontCrop, err := extractOrthoCharBBox(cropNRGBA(src, 0, 0, w/2, h))
	if err != nil {
		return nil, err
	}
	backCrop, err := extractOrthoCharBBox(cropNRGBA(src, w/2, 0, w, h))
	if err != nil {
		return nil, err
	}

	armW := 4
	if slim {
		armW = 3
	}
	fbW := armW + 8 + armW

	sliceCharacter := func(charImg *image.NRGBA, isBack bool) map[string]*image.NRGBA {
		cb := charImg.Bounds()
		cw, ch := cb.Dx(), cb.Dy()
		yHeadEnd := max(1, min(ch-2, int(math.Round(float64(ch)*8.0/32.0))))
		yBodyEnd := max(yHeadEnd+1, min(ch-1, int(math.Round(float64(ch)*20.0/32.0))))

		headBand := cropTightX(cropNRGBA(charImg, 0, 0, cw, yHeadEnd))
		bodyBand := cropTightX(cropNRGBA(charImg, 0, yHeadEnd, cw, yBodyEnd))
		legsBand := cropTightX(cropNRGBA(charImg, 0, yBodyEnd, cw, ch))

		bw, bh := bodyBand.Bounds().Dx(), bodyBand.Bounds().Dy()
		lw, lh := legsBand.Bounds().Dx(), legsBand.Bounds().Dy()
		xArm1 := max(1, min(bw-2, int(math.Round(float64(bw)*float64(armW)/float64(fbW)))))
		xArm2 := max(xArm1+1, min(bw-1, int(math.Round(float64(bw)*float64(armW+8)/float64(fbW)))))

		leftSideArm := cropNRGBA(bodyBand, 0, 0, xArm1, bh)
		torso := cropNRGBA(bodyBand, xArm1, 0, xArm2, bh)
		rightSideArm := cropNRGBA(bodyBand, xArm2, 0, bw, bh)

		leftSideLeg := cropNRGBA(legsBand, 0, 0, max(1, lw/2), lh)
		rightSideLeg := cropNRGBA(legsBand, max(1, lw/2), 0, lw, lh)

		if !isBack {
			return map[string]*image.NRGBA{
				"head":      headBand,
				"torso":     torso,
				"right_arm": leftSideArm,
				"left_arm":  rightSideArm,
				"right_leg": leftSideLeg,
				"left_leg":  rightSideLeg,
			}
		}
		return map[string]*image.NRGBA{
			"head":      headBand,
			"torso":     torso,
			"left_arm":  leftSideArm,
			"right_arm": rightSideArm,
			"left_leg":  leftSideLeg,
			"right_leg": rightSideLeg,
		}
	}

	fParts := sliceCharacter(frontCrop, false)
	bParts := sliceCharacter(backCrop, true)

	uvTable := getSkinUVTable(slim)
	skin := image.NewNRGBA(image.Rect(0, 0, 64, 64))

	put := func(part, face string, patch *image.NRGBA) {
		r := uvTable[part][face]
		sampled := resampleOpaque(patch, r.W, r.H)
		for y := 0; y < r.H; y++ {
			for x := 0; x < r.W; x++ {
				skin.SetNRGBA(r.X+x, r.Y+y, sampled.NRGBAAt(x, y))
			}
		}
	}

	for _, part := range pixelBaseParts {
		fImg := fParts[part]
		bImg := bParts[part]
		put(part, "front", fImg)
		put(part, "back", bImg)
		if part == "head" {
			// Use back hair for side faces to prevent eyes from bleeding onto ears.
			put(part, "right", bImg)
			put(part, "left", flipNRGBAHorizontal(bImg))
			bh := bImg.Bounds().Dy()
			fh := fImg.Bounds().Dy()
			put(part, "top", cropNRGBA(bImg, 0, 0, bImg.Bounds().Dx(), max(1, bh/3)))
			put(part, "bottom", cropNRGBA(fImg, 0, max(0, fh-max(1, fh/4)), fImg.Bounds().Dx(), fh))
			continue
		}
		fc := cropTightX(fImg)
		bc := cropTightX(bImg)
		fw, fh := fc.Bounds().Dx(), fc.Bounds().Dy()
		bw, bh := bc.Bounds().Dx(), bc.Bounds().Dy()
		rX0 := int(float64(fw) * 0.20)
		rX1 := max(rX0+1, int(float64(fw)*0.55))
		lX0 := int(float64(fw) * 0.45)
		lX1 := max(lX0+1, int(float64(fw)*0.80))
		put(part, "right", cropNRGBA(fc, rX0, 0, rX1, fh))
		put(part, "left", cropNRGBA(fc, lX0, 0, lX1, fh))
		put(part, "top", cropNRGBA(bc, 0, 0, bw, max(1, bh/4)))
		put(part, "bottom", cropNRGBA(fc, 0, max(0, fh-max(1, fh/4)), fw, fh))
	}

	if maxColors > 0 {
		skin = quantizeMedianCutNoDither(skin, maxColors)
	}
	return skin, nil
}

type colorCount struct {
	r, g, b uint8
	count   int
}

func quantizeMedianCutNoDither(src *image.NRGBA, maxColors int) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	freqMap := map[uint32]int{}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.NRGBAAt(b.Min.X+x, b.Min.Y+y)
			if c.A == 0 {
				continue
			}
			key := (uint32(c.R) << 16) | (uint32(c.G) << 8) | uint32(c.B)
			freqMap[key]++
		}
	}
	if len(freqMap) <= maxColors || maxColors <= 1 {
		out := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				c := src.NRGBAAt(b.Min.X+x, b.Min.Y+y)
				if c.A == 0 {
					continue
				}
				out.SetNRGBA(x, y, c)
			}
		}
		return out
	}

	items := make([]colorCount, 0, len(freqMap))
	for k, cnt := range freqMap {
		items = append(items, colorCount{
			r:     uint8((k >> 16) & 0xff),
			g:     uint8((k >> 8) & 0xff),
			b:     uint8(k & 0xff),
			count: cnt,
		})
	}

	type colorBox struct {
		items       []colorCount
		maxSpan     int
		splitAxis   int
		totalWeight int
	}
	buildBox := func(list []colorCount) colorBox {
		minR, maxR := 255, 0
		minG, maxG := 255, 0
		minB, maxB := 255, 0
		weight := 0
		for _, it := range list {
			if int(it.r) < minR {
				minR = int(it.r)
			}
			if int(it.r) > maxR {
				maxR = int(it.r)
			}
			if int(it.g) < minG {
				minG = int(it.g)
			}
			if int(it.g) > maxG {
				maxG = int(it.g)
			}
			if int(it.b) < minB {
				minB = int(it.b)
			}
			if int(it.b) > maxB {
				maxB = int(it.b)
			}
			weight += it.count
		}
		sr, sg, sb := maxR-minR, maxG-minG, maxB-minB
		axis, span := 0, sr
		if sg > span {
			axis, span = 1, sg
		}
		if sb > span {
			axis, span = 2, sb
		}
		return colorBox{items: list, maxSpan: span, splitAxis: axis, totalWeight: weight}
	}

	boxes := []colorBox{buildBox(items)}
	for len(boxes) < maxColors {
		bestIdx := -1
		bestScore := -1
		for i, bx := range boxes {
			if len(bx.items) <= 1 || bx.maxSpan == 0 {
				continue
			}
			score := bx.maxSpan * bx.totalWeight
			if score > bestScore {
				bestScore = score
				bestIdx = i
			}
		}
		if bestIdx < 0 {
			break
		}
		target := boxes[bestIdx]
		list := target.items
		sort.Slice(list, func(i, j int) bool {
			switch target.splitAxis {
			case 0:
				return list[i].r < list[j].r
			case 1:
				return list[i].g < list[j].g
			default:
				return list[i].b < list[j].b
			}
		})
		// Split at weighted median while keeping both partitions non-empty.
		halfWeight := target.totalWeight / 2
		acc := 0
		splitAt := 1
		for i := 0; i < len(list)-1; i++ {
			acc += list[i].count
			if acc >= halfWeight {
				splitAt = i + 1
				break
			}
		}
		boxes[bestIdx] = buildBox(list[:splitAt])
		boxes = append(boxes, buildBox(list[splitAt:]))
	}

	palette := make([]color.NRGBA, len(boxes))
	for i, bx := range boxes {
		var sr, sg, sb, sw int64
		for _, it := range bx.items {
			sr += int64(it.r) * int64(it.count)
			sg += int64(it.g) * int64(it.count)
			sb += int64(it.b) * int64(it.count)
			sw += int64(it.count)
		}
		if sw > 0 {
			palette[i] = color.NRGBA{
				R: uint8(sr / sw),
				G: uint8(sg / sw),
				B: uint8(sb / sw),
				A: 255,
			}
		}
	}

	lookup := make(map[uint32]color.NRGBA, len(freqMap))
	for _, it := range items {
		best := palette[0]
		bestDist := 1 << 30
		for _, p := range palette {
			dr := int(it.r) - int(p.R)
			dg := int(it.g) - int(p.G)
			db := int(it.b) - int(p.B)
			d := dr*dr + dg*dg + db*db
			if d < bestDist {
				bestDist = d
				best = p
			}
		}
		key := (uint32(it.r) << 16) | (uint32(it.g) << 8) | uint32(it.b)
		lookup[key] = best
	}

	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.NRGBAAt(b.Min.X+x, b.Min.Y+y)
			if c.A == 0 {
				continue
			}
			key := (uint32(c.R) << 16) | (uint32(c.G) << 8) | uint32(c.B)
			if mapped, ok := lookup[key]; ok {
				mapped.A = c.A
				out.SetNRGBA(x, y, mapped)
			} else {
				out.SetNRGBA(x, y, c)
			}
		}
	}
	return out
}

// RenderOrthoTurnaroundSheet renders a 1024x1024 solid #00FF00 turnaround (front left, back right) for /images/edits.
func RenderOrthoTurnaroundSheet(skin *image.NRGBA, slim bool) []byte {
	if skin == nil {
		return nil
	}
	sq := skinpng.Upgrade64x32To64x64(skin)
	front := skinpng.RenderBodyFront(sq, 26, slim, true)
	back := skinpng.RenderBodyBack(sq, 26, slim, true)

	sheet := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	green := color.NRGBA{0, 255, 0, 255}
	fillRectNRGBA(sheet, 0, 0, 1024, 1024, green)

	blitCentered := func(src *image.NRGBA, boxX, boxW int) {
		if src == nil {
			return
		}
		sb := src.Bounds()
		sw, sh := sb.Dx(), sb.Dy()
		ox := boxX + (boxW-sw)/2
		oy := (1024 - sh) / 2
		for y := 0; y < sh; y++ {
			for x := 0; x < sw; x++ {
				c := src.NRGBAAt(sb.Min.X+x, sb.Min.Y+y)
				if c.A >= 128 {
					c.A = 255
					sheet.SetNRGBA(ox+x, oy+y, c)
				}
			}
		}
	}
	blitCentered(front, 0, 512)
	blitCentered(back, 512, 512)

	b, _ := skinpng.EncodePNG(sheet)
	return b
}
