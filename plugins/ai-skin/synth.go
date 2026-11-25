package main

import (
	"hash/fnv"
	"image"
	"image/color"
	"math"
	"slices"
	"strconv"
	"strings"
)

// SkinRecipe describes structured visual parameters synthesized by an LLM or fallback rule engine.
type SkinRecipe struct {
	Summary        string  `json:"summary"`
	Model          string  `json:"model"`
	SkinTone       string  `json:"skinTone"`
	HairColor      string  `json:"hairColor"`
	HairAccent     string  `json:"hairAccent,omitempty"`
	HairStyle      string  `json:"hairStyle"`
	EyeColor       string  `json:"eyeColor"`
	EyeStyle       string  `json:"eyeStyle"`
	OutfitStyle    string  `json:"outfitStyle"`
	TopPrimary     string  `json:"topPrimary"`
	TopSecondary   string  `json:"topSecondary"`
	TopAccent      string  `json:"topAccent"`
	BottomPrimary  string  `json:"bottomPrimary"`
	BottomAccent   string  `json:"bottomAccent"`
	ShoeColor      string  `json:"shoeColor"`
	Accessory      string  `json:"accessory"`
	AccessoryColor string  `json:"accessoryColor"`
	Shading        float64 `json:"shading"`
	Seed           int64   `json:"seed,omitempty"`
}

var (
	validHairStyles   = []string{"short", "spiky", "long", "bob", "ponytail", "hood", "helmet"}
	validEyeStyles    = []string{"classic", "anime", "cute", "cool", "visor"}
	validOutfitStyles = []string{"hoodie", "jacket", "tshirt", "armor", "coat", "dress", "suit", "kimono"}
	validAccessories  = []string{"none", "glasses", "headphones", "crown", "cat_ears", "scarf", "beanie", "goggles"}
)

// NormalizeRecipe clamps enum fields, validates hex colors, and populates missing accents.
func NormalizeRecipe(r SkinRecipe, requestedModel string) SkinRecipe {
	if requestedModel == "slim" {
		r.Model = "slim"
	} else if r.Model != "slim" {
		r.Model = "default"
	}
	r.Summary = strings.TrimSpace(r.Summary)
	if r.Summary == "" {
		r.Summary = "AI 定制角色皮肤"
	}

	r.SkinTone = normalizeHex(r.SkinTone, "#ECC0A0")
	r.HairColor = normalizeHex(r.HairColor, "#3D2B24")
	if hairHighlight, ok := tryParseHex(r.HairAccent); ok {
		r.HairAccent = formatHex(hairHighlight)
	} else {
		r.HairAccent = formatHex(shiftBrightness(mustHex(r.HairColor), 1.18))
	}

	r.EyeColor = normalizeHex(r.EyeColor, "#3676BA")
	r.TopPrimary = normalizeHex(r.TopPrimary, "#3898BC")
	r.TopSecondary = normalizeHex(r.TopSecondary, formatHex(shiftBrightness(mustHex(r.TopPrimary), 0.78)))
	r.TopAccent = normalizeHex(r.TopAccent, "#F3F4F6")
	r.BottomPrimary = normalizeHex(r.BottomPrimary, "#333C57")
	r.BottomAccent = normalizeHex(r.BottomAccent, formatHex(shiftBrightness(mustHex(r.BottomPrimary), 1.2)))
	r.ShoeColor = normalizeHex(r.ShoeColor, "#2B2D36")
	r.AccessoryColor = normalizeHex(r.AccessoryColor, "#EAB308")

	r.HairStyle = strings.ToLower(strings.TrimSpace(r.HairStyle))
	if !slices.Contains(validHairStyles, r.HairStyle) {
		r.HairStyle = "short"
	}
	r.EyeStyle = strings.ToLower(strings.TrimSpace(r.EyeStyle))
	if !slices.Contains(validEyeStyles, r.EyeStyle) {
		r.EyeStyle = "anime"
	}
	r.OutfitStyle = strings.ToLower(strings.TrimSpace(r.OutfitStyle))
	if !slices.Contains(validOutfitStyles, r.OutfitStyle) {
		r.OutfitStyle = "hoodie"
	}
	r.Accessory = strings.ToLower(strings.TrimSpace(r.Accessory))
	if !slices.Contains(validAccessories, r.Accessory) {
		r.Accessory = "none"
	}

	if r.Shading < 0.05 {
		r.Shading = 0.14
	} else if r.Shading > 0.35 {
		r.Shading = 0.35
	}
	if r.Seed == 0 {
		r.Seed = 1
	}
	return r
}

// BuildLocalDemoRecipe derives a deterministic Minecraft skin recipe from Chinese/English prompt keywords,
// preserving untouched attributes when refining an existing base recipe.
func BuildLocalDemoRecipe(prompt, style, model string, base *SkinRecipe) SkinRecipe {
	var r SkinRecipe
	if base != nil {
		r = *base
	} else {
		r = SkinRecipe{
			SkinTone:      "#ECC0A0",
			HairColor:     "#3D2B24",
			EyeColor:      "#3676BA",
			EyeStyle:      "anime",
			HairStyle:     "short",
			OutfitStyle:   "hoodie",
			TopPrimary:    "#3B82F6",
			TopSecondary:  "#1E3A8A",
			TopAccent:     "#E0F2FE",
			BottomPrimary: "#334155",
			BottomAccent:  "#64748B",
			ShoeColor:     "#1E293B",
			Accessory:     "none",
			Shading:       0.16,
		}
	}

	h := fnv.New64a()
	_, _ = h.Write([]byte(prompt + "|" + style + "|" + model))
	seed := int64(h.Sum64() & 0x7fffffffffffffff)
	if seed == 0 {
		seed = 17
	}
	r.Seed = seed

	low := strings.ToLower(prompt)
	colorMap := []struct {
		words []string
		hex   string
	}{
		{[]string{"银", "白", "silver", "white", "snow", "雪"}, "#E5E7EB"},
		{[]string{"黑", "暗夜", "black", "dark", "obsidian", "墨"}, "#262934"},
		{[]string{"金", "黄", "gold", "blonde", "yellow", "琥珀"}, "#E6B44C"},
		{[]string{"红", "赤", "绯", "red", "crimson", "scarlet", "玫瑰"}, "#C93B4B"},
		{[]string{"粉", "樱", "桃", "pink", "sakura", "peach"}, "#E67EA3"},
		{[]string{"蓝", "青", "海", "blue", "cyan", "azure", "冰"}, "#3B82F6"},
		{[]string{"紫", "薰衣草", "purple", "violet", "lavender"}, "#7C3AED"},
		{[]string{"绿", "翠", "森", "green", "emerald", "forest", "薄荷"}, "#2F9E66"},
		{[]string{"棕", "褐", "茶", "brown", "chestnut", "咖啡"}, "#6B4634"},
		{[]string{"橙", "橘", "orange", "flame", "火"}, "#E07232"},
	}

	findColorAfter := func(anchors []string) string {
		for _, a := range anchors {
			idx := strings.Index(low, a)
			if idx < 0 {
				continue
			}
			start := max(0, idx-12)
			end := min(len(low), idx+len(a)+12)
			window := low[start:end]
			for _, c := range colorMap {
				for _, w := range c.words {
					if strings.Contains(window, w) {
						return c.hex
					}
				}
			}
		}
		return ""
	}

	if c := findColorAfter([]string{"发", "hair", "马尾", "短发", "长发"}); c != "" {
		r.HairColor = c
	} else if base == nil {
		for _, c := range colorMap {
			matched := false
			for _, w := range c.words {
				if strings.Contains(low, w) {
					matched = true
					break
				}
			}
			if matched {
				r.HairColor = c.hex
				break
			}
		}
	}

	if c := findColorAfter([]string{"眼", "瞳", "eye"}); c != "" {
		r.EyeColor = c
	}
	if c := findColorAfter([]string{"衣", "服", "卫衣", "外套", "甲", "裙", "西装", "和服", "cloak", "hoodie", "jacket", "armor", "coat", "dress", "suit"}); c != "" {
		r.TopPrimary = c
		r.TopSecondary = formatHex(shiftBrightness(mustHex(c), 0.72))
	} else if base == nil && style != "" {
		switch strings.ToLower(style) {
		case "cyberpunk", "赛博":
			r.TopPrimary, r.TopSecondary, r.TopAccent = "#1F2937", "#0F172A", "#22D3EE"
			r.EyeColor = "#22D3EE"
		case "fantasy", "奇幻":
			r.TopPrimary, r.TopSecondary, r.TopAccent = "#2D4F45", "#1E352F", "#FBBF24"
		case "knight", "骑士":
			r.TopPrimary, r.TopSecondary, r.TopAccent = "#94A3B8", "#475569", "#F59E0B"
		}
	}

	if containsAny(low, "长发", "long hair") {
		r.HairStyle = "long"
	} else if containsAny(low, "马尾", "ponytail") {
		r.HairStyle = "ponytail"
	} else if containsAny(low, "短发", "bob") {
		r.HairStyle = "bob"
	} else if containsAny(low, "刺", "spiky") {
		r.HairStyle = "spiky"
	} else if containsAny(low, "头盔", "helmet") {
		r.HairStyle = "helmet"
	} else if containsAny(low, "兜帽", "hood ") {
		r.HairStyle = "hood"
	}

	if containsAny(low, "目镜", "visor", "赛博", "cyber") {
		r.EyeStyle = "visor"
	} else if containsAny(low, "酷", "cool", "冷") {
		r.EyeStyle = "cool"
	} else if containsAny(low, "萌", "可爱", "cute") {
		r.EyeStyle = "cute"
	}

	switch {
	case containsAny(low, "盔甲", "骑士", "armor", "knight"):
		r.OutfitStyle = "armor"
	case containsAny(low, "大衣", "风衣", "长袍", "coat", "cloak"):
		r.OutfitStyle = "coat"
	case containsAny(low, "西装", "制服", "suit"):
		r.OutfitStyle = "suit"
	case containsAny(low, "和服", "kimono"):
		r.OutfitStyle = "kimono"
	case containsAny(low, "裙", "dress"):
		r.OutfitStyle = "dress"
	case containsAny(low, "夹克", "冲锋衣", "jacket"):
		r.OutfitStyle = "jacket"
	case containsAny(low, "t恤", "短袖", "tshirt", "t-shirt"):
		r.OutfitStyle = "tshirt"
	}

	switch {
	case containsAny(low, "猫耳", "cat ear", "狐耳"):
		r.Accessory = "cat_ears"
		r.AccessoryColor = r.HairColor
	case containsAny(low, "耳机", "headphone"):
		r.Accessory = "headphones"
		r.AccessoryColor = r.TopAccent
	case containsAny(low, "王冠", "皇冠", "crown"):
		r.Accessory = "crown"
		r.AccessoryColor = "#FACC15"
	case containsAny(low, "护目镜", "goggles"):
		r.Accessory = "goggles"
		r.AccessoryColor = "#38BDF8"
	case containsAny(low, "眼镜", "glasses"):
		r.Accessory = "glasses"
		r.AccessoryColor = "#334155"
	case containsAny(low, "围巾", "scarf"):
		r.Accessory = "scarf"
		r.AccessoryColor = r.TopAccent
	case containsAny(low, "毛线帽", "冷帽", "beanie"):
		r.Accessory = "beanie"
		r.AccessoryColor = r.TopPrimary
	}

	if containsAny(low, "苍白", "冷白", "pale") {
		r.SkinTone = "#F4D6C4"
	} else if containsAny(low, "小麦", "tan") {
		r.SkinTone = "#C88B63"
	} else if containsAny(low, "古铜", "dark skin") {
		r.SkinTone = "#8D5B3E"
	}

	r.Summary = "本地关键词合成：" + truncateRunes(strings.TrimSpace(prompt), 36)
	return NormalizeRecipe(r, model)
}

// RenderRecipe synthesizes a crisp, UV-compliant 64x64 Minecraft skin from a SkinRecipe.
func RenderRecipe(raw SkinRecipe) *image.NRGBA {
	r := NormalizeRecipe(raw, raw.Model)
	slim := r.Model == "slim"
	armW := 4
	if slim {
		armW = 3
	}

	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	skin := mustHex(r.SkinTone)
	skinShade := shiftBrightness(skin, 0.90)
	skinLight := shiftBrightness(skin, 1.06)
	hair := mustHex(r.HairColor)
	hairShade := shiftBrightness(hair, 0.82)
	hairHi := mustHex(r.HairAccent)
	eye := mustHex(r.EyeColor)
	top1 := mustHex(r.TopPrimary)
	top2 := mustHex(r.TopSecondary)
	topAcc := mustHex(r.TopAccent)
	bot1 := mustHex(r.BottomPrimary)
	botAcc := mustHex(r.BottomAccent)
	shoes := mustHex(r.ShoeColor)
	acc := mustHex(r.AccessoryColor)

	noiseVal := func(x, y int) float64 {
		v := uint64(x*73856093) ^ uint64(y*19349663) ^ uint64(r.Seed*83492791)
		v ^= v >> 13
		v *= 0xff51afd7ed558ccd
		v ^= v >> 33
		return (float64(v%100)/100.0 - 0.5) * 2.0 // -1..1
	}

	paintRect := func(x0, y0, w, h int, base color.NRGBA, depth float64) {
		for py := range h {
			for px := range w {
				factor := 1.0 + noiseVal(x0+px, y0+py)*r.Shading*depth
				img.SetNRGBA(x0+px, y0+py, shiftBrightness(base, factor))
			}
		}
	}

	// 1. Head Base (0,0)
	paintRect(8, 0, 8, 8, hair, 1.0)            // Top
	paintRect(16, 0, 8, 8, skinShade, 0.5)      // Bottom
	paintRect(0, 8, 8, 8, skin, 0.45)           // Right
	paintRect(8, 8, 8, 8, skin, 0.35)           // Front
	paintRect(16, 8, 8, 8, skin, 0.45)          // Left
	paintRect(24, 8, 8, 8, hair, 1.0)           // Back
	// Facial blush / nose shading + Hair sides & bangs on Base
	img.SetNRGBA(11, 14, skinLight)
	img.SetNRGBA(12, 14, skinLight)
	img.SetNRGBA(11, 15, skinShade)
	img.SetNRGBA(12, 15, skinShade)
	paintRect(0, 8, 8, 3, hair, 0.9)
	paintRect(16, 8, 8, 3, hair, 0.9)
	paintRect(8, 8, 8, 2, hair, 0.9)
	if r.HairStyle == "spiky" {
		img.SetNRGBA(9, 10, hairHi)
		img.SetNRGBA(11, 10, hair)
		img.SetNRGBA(14, 10, hairHi)
	} else {
		img.SetNRGBA(8, 10, hair)
		img.SetNRGBA(15, 10, hair)
	}
	// Back/side hair extension for long/bob/ponytail
	if r.HairStyle == "long" || r.HairStyle == "bob" {
		paintRect(0, 11, 3, 5, hairShade, 0.8)
		paintRect(21, 11, 3, 5, hairShade, 0.8)
	}
	if r.HairStyle == "ponytail" {
		for py := 10; py < 16; py++ {
			img.SetNRGBA(27, py, hairHi)
			img.SetNRGBA(28, py, hairHi)
		}
	}

	// Eyes on Head Front (8..15, 8..15)
	white := color.NRGBA{250, 250, 252, 255}
	eyeDark := shiftBrightness(eye, 0.55)
	switch r.EyeStyle {
	case "classic":
		img.SetNRGBA(9, 12, white)
		img.SetNRGBA(10, 12, eye)
		img.SetNRGBA(13, 12, eye)
		img.SetNRGBA(14, 12, white)
	case "cute":
		img.SetNRGBA(9, 11, white)
		img.SetNRGBA(10, 11, eye)
		img.SetNRGBA(9, 12, white)
		img.SetNRGBA(10, 12, eyeDark)
		img.SetNRGBA(13, 11, eye)
		img.SetNRGBA(14, 11, white)
		img.SetNRGBA(13, 12, eyeDark)
		img.SetNRGBA(14, 12, white)
		img.SetNRGBA(9, 13, shiftBrightness(skin, 0.94))
		img.SetNRGBA(14, 13, shiftBrightness(skin, 0.94))
	case "cool":
		img.SetNRGBA(9, 11, hairShade)
		img.SetNRGBA(10, 11, hairShade)
		img.SetNRGBA(13, 11, hairShade)
		img.SetNRGBA(14, 11, hairShade)
		img.SetNRGBA(9, 12, white)
		img.SetNRGBA(10, 12, eyeDark)
		img.SetNRGBA(13, 12, eyeDark)
		img.SetNRGBA(14, 12, white)
	case "visor":
		for x := 8; x < 16; x++ {
			img.SetNRGBA(x, 11, eyeDark)
			img.SetNRGBA(x, 12, eye)
		}
		img.SetNRGBA(10, 11, white)
		img.SetNRGBA(11, 11, white)
	default: // anime
		img.SetNRGBA(9, 11, white)
		img.SetNRGBA(10, 11, eyeDark)
		img.SetNRGBA(9, 12, white)
		img.SetNRGBA(10, 12, eye)
		img.SetNRGBA(13, 11, eyeDark)
		img.SetNRGBA(14, 11, white)
		img.SetNRGBA(13, 12, eye)
		img.SetNRGBA(14, 12, white)
	}

	// Hat outer layer (32,0): fluffy bangs + accessories
	if r.HairStyle != "hood" && r.HairStyle != "helmet" {
		for x := 40; x < 48; x++ {
			if (x+int(r.Seed))%3 == 0 {
				img.SetNRGBA(x, 8, hairHi)
				img.SetNRGBA(x, 9, hair)
			}
		}
		img.SetNRGBA(40, 10, hairShade)
		img.SetNRGBA(47, 10, hairShade)
		if r.HairStyle == "long" {
			paintRect(56, 8, 8, 8, hair, 0.9)
		}
	} else if r.HairStyle == "helmet" {
		paintRect(40, 0, 8, 8, top1, 0.8)
		paintRect(32, 8, 8, 8, top2, 0.8)
		paintRect(48, 8, 8, 8, top2, 0.8)
		paintRect(56, 8, 8, 8, top1, 0.8)
		paintRect(40, 8, 8, 2, topAcc, 0.5)
	} else {
		paintRect(40, 0, 8, 8, top1, 0.8)
		paintRect(32, 8, 8, 8, top2, 0.8)
		paintRect(48, 8, 8, 8, top2, 0.8)
		paintRect(56, 8, 8, 8, top1, 0.8)
		paintRect(40, 8, 8, 1, topAcc, 0.4)
	}

	switch r.Accessory {
	case "crown":
		for x := 40; x < 48; x++ {
			img.SetNRGBA(x, 8, acc)
			if x%2 == 0 {
				img.SetNRGBA(x, 7, acc)
			}
		}
		img.SetNRGBA(43, 8, topAcc)
		img.SetNRGBA(44, 8, topAcc)
	case "headphones":
		paintRect(34, 10, 4, 4, acc, 0.4)
		paintRect(50, 10, 4, 4, acc, 0.4)
		paintRect(40, 3, 8, 2, acc, 0.4)
	case "glasses":
		for _, x := range []int{40, 41, 42, 43, 44, 45, 46, 47} {
			img.SetNRGBA(x, 11, acc)
		}
		img.SetNRGBA(41, 12, acc)
		img.SetNRGBA(46, 12, acc)
	case "goggles":
		paintRect(40, 9, 8, 2, acc, 0.3)
		img.SetNRGBA(41, 9, white)
		img.SetNRGBA(45, 9, white)
	case "beanie":
		paintRect(40, 0, 8, 8, acc, 0.8)
		paintRect(32, 8, 8, 3, acc, 0.8)
		paintRect(40, 8, 8, 3, acc, 0.8)
		paintRect(48, 8, 8, 3, acc, 0.8)
		paintRect(56, 8, 8, 3, acc, 0.8)
	case "cat_ears":
		img.SetNRGBA(40, 7, acc)
		img.SetNRGBA(41, 7, acc)
		img.SetNRGBA(41, 6, acc)
		img.SetNRGBA(46, 7, acc)
		img.SetNRGBA(47, 7, acc)
		img.SetNRGBA(46, 6, acc)
	}

	// 2. Torso Base (16,16) + Jacket Outer (16,32)
	paintRect(20, 16, 8, 4, top2, 0.6)
	paintRect(28, 16, 8, 4, top2, 0.6)
	paintRect(16, 20, 4, 12, top2, 0.8)
	paintRect(20, 20, 8, 12, top1, 0.8)
	paintRect(28, 20, 4, 12, top2, 0.8)
	paintRect(32, 20, 8, 12, top2, 0.8)
	// Neck / collar opening
	img.SetNRGBA(23, 20, skin)
	img.SetNRGBA(24, 20, skin)
	img.SetNRGBA(22, 20, topAcc)
	img.SetNRGBA(25, 20, topAcc)

	switch r.OutfitStyle {
	case "armor":
		paintRect(20, 21, 8, 4, top2, 0.6)
		paintRect(20, 29, 8, 2, topAcc, 0.4)
		paintRect(20, 36, 8, 10, top1, 0.8)
		paintRect(22, 37, 4, 3, topAcc, 0.4)
	case "suit":
		paintRect(22, 21, 4, 8, topAcc, 0.3)
		for y := 21; y < 29; y++ {
			img.SetNRGBA(23, y, top2)
			img.SetNRGBA(24, y, top2)
		}
		paintRect(20, 36, 2, 10, top2, 0.6)
		paintRect(26, 36, 2, 10, top2, 0.6)
	case "jacket", "coat":
		paintRect(20, 36, 3, 12, top2, 0.7)
		paintRect(25, 36, 3, 12, top2, 0.7)
		paintRect(16, 36, 4, 12, top2, 0.7)
		paintRect(28, 36, 4, 12, top2, 0.7)
		paintRect(32, 36, 8, 12, top1, 0.8)
		for y := 21; y < 31; y++ {
			img.SetNRGBA(23, y, topAcc)
			img.SetNRGBA(24, y, topAcc)
		}
	case "dress":
		paintRect(20, 28, 8, 4, top2, 0.7)
		paintRect(20, 44, 8, 4, topAcc, 0.5)
	case "kimono":
		for i := range 6 {
			img.SetNRGBA(21+i/2, 20+i, topAcc)
			img.SetNRGBA(26-i/2, 20+i, topAcc)
		}
		paintRect(20, 42, 8, 3, top2, 0.4)
	case "tshirt":
		paintRect(22, 23, 4, 3, topAcc, 0.4)
	default: // hoodie
		paintRect(22, 27, 4, 3, top2, 0.5)
		for y := 21; y < 25; y++ {
			img.SetNRGBA(22, y, topAcc)
			img.SetNRGBA(25, y, topAcc)
		}
		paintRect(20, 46, 8, 2, top2, 0.5)
	}
	if r.Accessory == "scarf" {
		paintRect(20, 36, 8, 2, acc, 0.4)
		paintRect(32, 36, 8, 2, acc, 0.4)
		paintRect(24, 38, 2, 5, acc, 0.4)
	}

	// 3. Arms: Right (40,16 + sleeve 40,32) and Left (32,48 + sleeve 48,48)
	paintArm := func(bx, by, sx, sy int) {
		sleeveLen := 9
		if r.OutfitStyle == "tshirt" {
			sleeveLen = 4
		}
		paintRect(bx+4, by, armW, 4, top2, 0.6)
		paintRect(bx+4+armW, by, armW, 4, skinShade, 0.5)
		// Upper sleeve faces
		paintRect(bx, by+4, 4, sleeveLen, top2, 0.8)
		paintRect(bx+4, by+4, armW, sleeveLen, top1, 0.8)
		paintRect(bx+4+armW, by+4, 4, sleeveLen, top2, 0.8)
		paintRect(bx+8+armW, by+4, armW, sleeveLen, top2, 0.8)
		// Lower forearm + hands
		remH := 12 - sleeveLen
		paintRect(bx, by+4+sleeveLen, 4, remH, skinShade, 0.5)
		paintRect(bx+4, by+4+sleeveLen, armW, remH, skin, 0.4)
		paintRect(bx+4+armW, by+4+sleeveLen, 4, remH, skinShade, 0.5)
		paintRect(bx+8+armW, by+4+sleeveLen, armW, remH, skinShade, 0.5)
		// Outer cuff trim
		if sleeveLen > 4 {
			paintRect(sx, sy+4+sleeveLen-1, 4, 1, topAcc, 0.3)
			paintRect(sx+4, sy+4+sleeveLen-1, armW, 1, topAcc, 0.3)
			paintRect(sx+4+armW, sy+4+sleeveLen-1, 4, 1, topAcc, 0.3)
			paintRect(sx+8+armW, sy+4+sleeveLen-1, armW, 1, topAcc, 0.3)
		}
	}
	paintArm(40, 16, 40, 32)
	paintArm(32, 48, 48, 48)

	// 4. Legs: Right (0,16 + pants 0,32) and Left (16,48 + pants 0,48)
	paintLeg := func(bx, by, ox, oy int) {
		paintRect(bx+4, by, 4, 4, botAcc, 0.5)
		paintRect(bx+8, by, 4, 4, shoes, 0.5)
		paintRect(bx, by+4, 16, 9, bot1, 0.8)
		paintRect(bx+4, by+4, 4, 9, botAcc, 0.6)
		paintRect(bx, by+13, 16, 3, shoes, 0.6)
		// Outer cuff & shoelaces
		paintRect(ox+4, oy+13, 4, 1, topAcc, 0.3)
	}
	paintLeg(0, 16, 0, 32)
	paintLeg(16, 48, 0, 48)

	return img
}

func normalizeHex(raw, fallback string) string {
	if c, ok := tryParseHex(raw); ok {
		return formatHex(c)
	}
	return fallback
}

func tryParseHex(raw string) (color.NRGBA, bool) {
	s := strings.TrimSpace(strings.TrimPrefix(raw, "#"))
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return color.NRGBA{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.NRGBA{}, false
	}
	return color.NRGBA{
		R: uint8((v >> 16) & 0xff),
		G: uint8((v >> 8) & 0xff),
		B: uint8(v & 0xff),
		A: 255,
	}, true
}

func mustHex(raw string) color.NRGBA {
	if c, ok := tryParseHex(raw); ok {
		return c
	}
	return color.NRGBA{200, 200, 200, 255}
}

func formatHex(c color.NRGBA) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{
		'#',
		digits[c.R>>4], digits[c.R&0xf],
		digits[c.G>>4], digits[c.G&0xf],
		digits[c.B>>4], digits[c.B&0xf],
	})
}

func shiftBrightness(c color.NRGBA, factor float64) color.NRGBA {
	clamp := func(v float64) uint8 {
		return uint8(min(255, max(0, math.Round(v))))
	}
	return color.NRGBA{
		R: clamp(float64(c.R) * factor),
		G: clamp(float64(c.G) * factor),
		B: clamp(float64(c.B) * factor),
		A: c.A,
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func truncateRunes(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen]) + "…"
}
