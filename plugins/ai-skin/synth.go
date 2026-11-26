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
	HairLayers     int     `json:"hairLayers,omitempty"`
	HairSheen      float64 `json:"hairSheen,omitempty"`
	EyeColor       string  `json:"eyeColor"`
	EyeStyle       string  `json:"eyeStyle"`
	BlushIntensity float64 `json:"blushIntensity,omitempty"`
	OutfitStyle    string  `json:"outfitStyle"`
	TopPrimary     string  `json:"topPrimary"`
	TopSecondary   string  `json:"topSecondary"`
	TopAccent      string  `json:"topAccent"`
	SleeveStyle    string  `json:"sleeveStyle,omitempty"`
	BottomStyle    string  `json:"bottomStyle,omitempty"`
	BottomPrimary  string  `json:"bottomPrimary"`
	BottomAccent   string  `json:"bottomAccent"`
	ShoeColor      string  `json:"shoeColor"`
	Accessory      string  `json:"accessory"`
	AccessoryColor string  `json:"accessoryColor"`
	DetailAccent   *bool   `json:"detailAccent,omitempty"`
	Shading        float64 `json:"shading"`
	Seed           int64   `json:"seed,omitempty"`
}

var (
	validHairStyles   = []string{"short", "spiky", "long", "bob", "ponytail", "hood", "helmet"}
	validEyeStyles    = []string{"classic", "anime", "cute", "cool", "visor"}
	validOutfitStyles = []string{"hoodie", "jacket", "tshirt", "armor", "coat", "dress", "suit", "kimono"}
	validSleeveStyles = []string{"long", "short", "rolled", "sleeveless"}
	validBottomStyles = []string{"pants", "shorts", "skirt", "armor_greaves"}
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
		r.HairAccent = formatHex(shadeHueShift(mustHex(r.HairColor), 1.18, 0.08))
	}

	r.EyeColor = normalizeHex(r.EyeColor, "#3676BA")
	r.TopPrimary = normalizeHex(r.TopPrimary, "#3898BC")
	r.TopSecondary = normalizeHex(r.TopSecondary, formatHex(shadeHueShift(mustHex(r.TopPrimary), 0.78, -0.06)))
	r.TopAccent = normalizeHex(r.TopAccent, "#F3F4F6")
	r.BottomPrimary = normalizeHex(r.BottomPrimary, "#333C57")
	r.BottomAccent = normalizeHex(r.BottomAccent, formatHex(shadeHueShift(mustHex(r.BottomPrimary), 1.2, 0.04)))
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
	r.SleeveStyle = strings.ToLower(strings.TrimSpace(r.SleeveStyle))
	if !slices.Contains(validSleeveStyles, r.SleeveStyle) {
		if r.OutfitStyle == "tshirt" {
			r.SleeveStyle = "short"
		} else {
			r.SleeveStyle = "long"
		}
	}
	r.BottomStyle = strings.ToLower(strings.TrimSpace(r.BottomStyle))
	if !slices.Contains(validBottomStyles, r.BottomStyle) {
		switch r.OutfitStyle {
		case "dress":
			r.BottomStyle = "skirt"
		case "armor":
			r.BottomStyle = "armor_greaves"
		default:
			r.BottomStyle = "pants"
		}
	}
	r.Accessory = strings.ToLower(strings.TrimSpace(r.Accessory))
	if !slices.Contains(validAccessories, r.Accessory) {
		r.Accessory = "none"
	}

	if r.HairLayers < 1 {
		r.HairLayers = 2
	} else if r.HairLayers > 3 {
		r.HairLayers = 3
	}
	if r.HairSheen <= 0 {
		r.HairSheen = 0.55
	} else if r.HairSheen > 1 {
		r.HairSheen = 1
	}
	if r.BlushIntensity < 0 {
		r.BlushIntensity = 0
	} else if r.BlushIntensity > 0.75 {
		r.BlushIntensity = 0.75
	}
	if r.DetailAccent == nil {
		v := true
		r.DetailAccent = &v
	}

	if r.Shading < 0.08 {
		r.Shading = 0.16
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
		r.BottomStyle = "armor_greaves"
	case containsAny(low, "大衣", "风衣", "长袍", "coat", "cloak"):
		r.OutfitStyle = "coat"
	case containsAny(low, "西装", "制服", "suit"):
		r.OutfitStyle = "suit"
	case containsAny(low, "和服", "kimono"):
		r.OutfitStyle = "kimono"
	case containsAny(low, "裙", "dress"):
		r.OutfitStyle = "dress"
		r.BottomStyle = "skirt"
	case containsAny(low, "夹克", "冲锋衣", "jacket"):
		r.OutfitStyle = "jacket"
	case containsAny(low, "t恤", "短袖", "tshirt", "t-shirt"):
		r.OutfitStyle = "tshirt"
		r.SleeveStyle = "short"
	}
	if containsAny(low, "短裙", "百褶裙", "skirt") {
		r.BottomStyle = "skirt"
	} else if containsAny(low, "短裤", "shorts") {
		r.BottomStyle = "shorts"
	}
	if containsAny(low, "无袖", "背心", "sleeveless") {
		r.SleeveStyle = "sleeveless"
	} else if containsAny(low, "挽袖", "卷袖", "五分袖", "rolled") {
		r.SleeveStyle = "rolled"
	}
	if containsAny(low, "少女", "可爱", "萌", "cute", "腮红") {
		r.BlushIntensity = 0.48
	}
	if containsAny(low, "银", "白", "金", "赛博", "cyber", "科技") {
		r.HairSheen = 0.75
		r.HairLayers = 3
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

// RenderRecipe synthesizes a crisp, UV-compliant 64x64 Minecraft skin from a SkinRecipe
// using hue-shifted pixel shading, directional ambient occlusion, volumetric hair locks, and layered clothing details.
func RenderRecipe(raw SkinRecipe) *image.NRGBA {
	r := NormalizeRecipe(raw, raw.Model)
	slim := r.Model == "slim"
	armW := 4
	if slim {
		armW = 3
	}
	detailOn := r.DetailAccent == nil || *r.DetailAccent

	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	skin := mustHex(r.SkinTone)
	skinShade := shadeHueShift(skin, 0.87, -0.04)
	skinDeep := shadeHueShift(skin, 0.78, -0.06)
	skinLight := shadeHueShift(skin, 1.07, 0.03)
	hair := mustHex(r.HairColor)
	hairShade := shadeHueShift(hair, 0.79, -0.05)
	hairDeep := shadeHueShift(hair, 0.68, -0.08)
	hairHi := mustHex(r.HairAccent)
	eye := mustHex(r.EyeColor)
	top1 := mustHex(r.TopPrimary)
	top1Hi := shadeHueShift(top1, 1.12, 0.04)
	top1AO := shadeHueShift(top1, 0.76, -0.06)
	top2 := mustHex(r.TopSecondary)
	topAcc := mustHex(r.TopAccent)
	bot1 := mustHex(r.BottomPrimary)
	bot1AO := shadeHueShift(bot1, 0.78, -0.05)
	botAcc := mustHex(r.BottomAccent)
	shoes := mustHex(r.ShoeColor)
	shoesHi := shadeHueShift(shoes, 1.16, 0.02)
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
				n := noiseVal(x0+px, y0+py) * r.Shading * depth
				img.SetNRGBA(x0+px, y0+py, shadeHueShift(base, 1.0+n, n*0.35))
			}
		}
	}

	paintHairBlock := func(x0, y0, w, h int) {
		for py := range h {
			for px := range w {
				strand := math.Sin(float64(x0+px)*0.95 + float64(r.Seed%7))
				n := noiseVal(x0+px, y0+py)*r.Shading*0.65 + strand*0.09*float64(r.HairLayers)
				if py == h-1 && r.HairLayers >= 2 {
					n -= 0.10
				}
				c := shadeHueShift(hair, 1.0+n, n*0.4)
				if r.HairSheen > 0.35 && py >= 1 && py <= 2 && ((x0+px+int(r.Seed))%3 == 0) {
					c = mixNRGBA(c, hairHi, r.HairSheen*0.45)
				}
				img.SetNRGBA(x0+px, y0+py, c)
			}
		}
	}

	// 1. Head Base (0,0)
	paintHairBlock(8, 0, 8, 8)              // Top
	paintRect(16, 0, 8, 8, skinDeep, 0.35)  // Bottom jaw shadow
	paintRect(0, 8, 8, 8, skin, 0.40)       // Right
	paintRect(8, 8, 8, 8, skin, 0.28)       // Front
	paintRect(16, 8, 8, 8, skin, 0.40)      // Left
	paintHairBlock(24, 8, 8, 8)             // Back

	// Crown highlight band on head top
	if r.HairSheen > 0.25 {
		for x := 9; x <= 14; x++ {
			img.SetNRGBA(x, 3, mixNRGBA(hair, hairHi, r.HairSheen*0.65))
		}
	}

	// Nose bridge highlight & cheek blush + cast AO shadow under bangs
	paintHairBlock(0, 8, 8, 3)
	paintHairBlock(16, 8, 8, 3)
	paintHairBlock(8, 8, 8, 2)
	for x := 8; x < 16; x++ {
		cBelow := img.NRGBAAt(x, 10)
		img.SetNRGBA(x, 10, shadeHueShift(cBelow, 0.88, -0.05))
	}
	img.SetNRGBA(11, 13, skinLight)
	img.SetNRGBA(12, 13, skinLight)
	img.SetNRGBA(11, 15, skinShade)
	img.SetNRGBA(12, 15, skinShade)
	if r.BlushIntensity > 0.05 {
		blushColor := color.NRGBA{235, 130, 140, 255}
		img.SetNRGBA(9, 13, mixNRGBA(img.NRGBAAt(9, 13), blushColor, r.BlushIntensity*0.75))
		img.SetNRGBA(14, 13, mixNRGBA(img.NRGBAAt(14, 13), blushColor, r.BlushIntensity*0.75))
	}
	if r.HairStyle == "spiky" {
		img.SetNRGBA(9, 10, hairHi)
		img.SetNRGBA(11, 10, hair)
		img.SetNRGBA(14, 10, hairHi)
	} else {
		img.SetNRGBA(8, 10, hairShade)
		img.SetNRGBA(15, 10, hairShade)
	}
	if r.HairStyle == "long" || r.HairStyle == "bob" {
		paintHairBlock(0, 11, 3, 5)
		paintHairBlock(21, 11, 3, 5)
	}
	if r.HairStyle == "ponytail" {
		for py := 10; py < 16; py++ {
			img.SetNRGBA(27, py, hairHi)
			img.SetNRGBA(28, py, hairShade)
		}
	}

	// Eyes on Head Front (8..15, 8..15)
	white := color.NRGBA{252, 252, 255, 255}
	eyeDark := shadeHueShift(eye, 0.52, -0.08)
	eyeSpec := mixNRGBA(eye, white, 0.65)
	switch r.EyeStyle {
	case "classic":
		img.SetNRGBA(9, 12, white)
		img.SetNRGBA(10, 12, eye)
		img.SetNRGBA(13, 12, eye)
		img.SetNRGBA(14, 12, white)
	case "cute":
		img.SetNRGBA(9, 11, white)
		img.SetNRGBA(10, 11, eyeSpec)
		img.SetNRGBA(9, 12, white)
		img.SetNRGBA(10, 12, eyeDark)
		img.SetNRGBA(13, 11, eyeSpec)
		img.SetNRGBA(14, 11, white)
		img.SetNRGBA(13, 12, eyeDark)
		img.SetNRGBA(14, 12, white)
	case "cool":
		img.SetNRGBA(9, 11, hairDeep)
		img.SetNRGBA(10, 11, hairDeep)
		img.SetNRGBA(13, 11, hairDeep)
		img.SetNRGBA(14, 11, hairDeep)
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
		img.SetNRGBA(11, 11, eyeSpec)
	default: // anime
		img.SetNRGBA(9, 11, eyeSpec)
		img.SetNRGBA(10, 11, eyeDark)
		img.SetNRGBA(9, 12, white)
		img.SetNRGBA(10, 12, eye)
		img.SetNRGBA(13, 11, eyeDark)
		img.SetNRGBA(14, 11, eyeSpec)
		img.SetNRGBA(13, 12, eye)
		img.SetNRGBA(14, 12, white)
	}

	// Hat outer layer (32,0): volumetric layered hair bangs/locks + accessories
	if r.HairStyle != "hood" && r.HairStyle != "helmet" {
		for x := 40; x < 48; x++ {
			if (x+int(r.Seed))%2 == 0 {
				img.SetNRGBA(x, 8, hairHi)
				img.SetNRGBA(x, 9, hair)
			} else {
				img.SetNRGBA(x, 8, hair)
			}
		}
		img.SetNRGBA(40, 10, hairShade)
		img.SetNRGBA(41, 10, hair)
		img.SetNRGBA(46, 10, hair)
		img.SetNRGBA(47, 10, hairShade)
		if r.HairLayers >= 2 {
			// Volumetric side locks framing ears/cheeks
			img.SetNRGBA(39, 10, hair)
			img.SetNRGBA(39, 11, hairShade)
			img.SetNRGBA(48, 10, hair)
			img.SetNRGBA(48, 11, hairShade)
		}
		if r.HairStyle == "long" || r.HairStyle == "bob" {
			paintHairBlock(56, 8, 8, 8)
			if r.HairStyle == "long" {
				paintHairBlock(32, 8, 4, 8)
				paintHairBlock(52, 8, 4, 8)
			}
		}
		if r.HairStyle == "ponytail" {
			paintHairBlock(58, 9, 4, 7)
			img.SetNRGBA(59, 9, topAcc)
			img.SetNRGBA(60, 9, topAcc)
		}
	} else if r.HairStyle == "helmet" {
		paintRect(40, 0, 8, 8, top1, 0.7)
		paintRect(32, 8, 8, 8, top2, 0.7)
		paintRect(48, 8, 8, 8, top2, 0.7)
		paintRect(56, 8, 8, 8, top1, 0.7)
		paintRect(40, 8, 8, 2, topAcc, 0.45)
	} else {
		paintRect(40, 0, 8, 8, top1, 0.75)
		paintRect(32, 8, 8, 8, top2, 0.75)
		paintRect(48, 8, 8, 8, top2, 0.75)
		paintRect(56, 8, 8, 8, top1, 0.75)
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
		for x := 40; x < 48; x++ {
			img.SetNRGBA(x, 11, acc)
		}
		img.SetNRGBA(41, 12, acc)
		img.SetNRGBA(46, 12, acc)
	case "goggles":
		paintRect(40, 9, 8, 2, acc, 0.3)
		img.SetNRGBA(41, 9, white)
		img.SetNRGBA(45, 9, white)
	case "beanie":
		paintRect(40, 0, 8, 8, acc, 0.75)
		paintRect(32, 8, 8, 3, acc, 0.75)
		paintRect(40, 8, 8, 3, acc, 0.75)
		paintRect(48, 8, 8, 3, acc, 0.75)
		paintRect(56, 8, 8, 3, acc, 0.75)
	case "cat_ears":
		img.SetNRGBA(40, 7, acc)
		img.SetNRGBA(41, 7, acc)
		img.SetNRGBA(41, 6, hairHi)
		img.SetNRGBA(46, 7, acc)
		img.SetNRGBA(47, 7, acc)
		img.SetNRGBA(46, 6, hairHi)
	}

	// 2. Torso Base (16,16) + Jacket Outer (16,32) with shoulder/neck/waist AO
	paintRect(20, 16, 8, 4, top2, 0.55)
	paintRect(28, 16, 8, 4, top2, 0.55)
	paintRect(16, 20, 4, 12, top2, 0.75)
	paintRect(20, 20, 8, 12, top1, 0.75)
	paintRect(28, 20, 4, 12, top2, 0.75)
	paintRect(32, 20, 8, 12, top2, 0.75)

	// Armpit & torso side AO + neck collarbone shadow
	if detailOn {
		paintRect(16, 20, 4, 3, top1AO, 0.35)
		paintRect(28, 20, 4, 3, top1AO, 0.35)
		for y := 20; y < 32; y++ {
			img.SetNRGBA(20, y, top1AO)
			img.SetNRGBA(27, y, top1AO)
		}
		img.SetNRGBA(22, 20, top1AO)
		img.SetNRGBA(25, 20, top1AO)
	}
	img.SetNRGBA(23, 20, skinDeep)
	img.SetNRGBA(24, 20, skinDeep)
	img.SetNRGBA(23, 21, skinShade)
	img.SetNRGBA(24, 21, skinShade)

	switch r.OutfitStyle {
	case "armor":
		paintRect(20, 21, 8, 5, top2, 0.55)
		paintRect(22, 22, 4, 3, top1Hi, 0.4)
		paintRect(20, 29, 8, 2, topAcc, 0.4)
		paintRect(20, 36, 8, 10, top1, 0.7)
		paintRect(22, 37, 4, 3, topAcc, 0.35)
	case "suit":
		paintRect(22, 21, 4, 9, topAcc, 0.25)
		for y := 21; y < 30; y++ {
			img.SetNRGBA(23, y, top2)
			img.SetNRGBA(24, y, top2)
		}
		paintRect(20, 36, 2, 10, top2, 0.55)
		paintRect(26, 36, 2, 10, top2, 0.55)
	case "jacket", "coat":
		paintRect(22, 21, 4, 11, topAcc, 0.3)
		paintRect(20, 36, 2, 12, top2, 0.6)
		paintRect(26, 36, 2, 12, top2, 0.6)
		paintRect(16, 36, 4, 12, top2, 0.65)
		paintRect(28, 36, 4, 12, top2, 0.65)
		paintRect(32, 36, 8, 12, top1, 0.7)
		for y := 22; y < 31; y++ {
			img.SetNRGBA(22, y, top1Hi)
			img.SetNRGBA(25, y, top1Hi)
		}
	case "dress":
		paintRect(20, 28, 8, 4, top2, 0.6)
		paintRect(20, 43, 8, 5, top1Hi, 0.45)
		paintRect(22, 28, 4, 1, topAcc, 0.3)
	case "kimono":
		for i := range 6 {
			img.SetNRGBA(21+i/2, 20+i, topAcc)
			img.SetNRGBA(26-i/2, 20+i, topAcc)
		}
		paintRect(20, 27, 8, 3, topAcc, 0.35)
		paintRect(20, 42, 8, 3, top2, 0.4)
	case "tshirt":
		paintRect(22, 23, 4, 3, topAcc, 0.35)
		if detailOn {
			paintRect(20, 31, 8, 1, top1AO, 0.3)
		}
	default: // hoodie
		paintRect(22, 27, 4, 4, top2, 0.45)
		img.SetNRGBA(21, 28, top1AO)
		img.SetNRGBA(26, 28, top1AO)
		for y := 21; y < 25; y++ {
			img.SetNRGBA(22, y, topAcc)
			img.SetNRGBA(25, y, topAcc)
		}
		paintRect(20, 45, 8, 3, top2, 0.45)
		paintRect(32, 36, 8, 5, top2, 0.6)
	}
	if detailOn {
		paintRect(20, 31, 8, 1, shadeHueShift(bot1, 0.85, -0.04), 0.3)
	}
	if r.Accessory == "scarf" {
		paintRect(20, 36, 8, 2, acc, 0.35)
		paintRect(32, 36, 8, 2, acc, 0.35)
		paintRect(24, 38, 2, 5, acc, 0.35)
	}

	// 3. Arms: Right (40,16 + sleeve 40,32) and Left (32,48 + sleeve 48,48)
	paintArm := func(bx, by, sx, sy int) {
		sleeveLen := 9
		switch r.SleeveStyle {
		case "short":
			sleeveLen = 4
		case "rolled":
			sleeveLen = 7
		case "sleeveless":
			sleeveLen = 1
		}
		if sleeveLen > 2 {
			paintRect(bx+4, by, armW, 4, top2, 0.55)
		} else {
			paintRect(bx+4, by, armW, 4, skin, 0.4)
		}
		paintRect(bx+4+armW, by, armW, 4, skinShade, 0.45)
		if sleeveLen > 0 {
			paintRect(bx, by+4, 4, sleeveLen, top2, 0.75)
			paintRect(bx+4, by+4, armW, sleeveLen, top1, 0.75)
			paintRect(bx+4+armW, by+4, 4, sleeveLen, top1AO, 0.6)
			paintRect(bx+8+armW, by+4, armW, sleeveLen, top2, 0.75)
		}
		remH := 12 - sleeveLen
		if remH > 0 {
			paintRect(bx, by+4+sleeveLen, 4, remH, skinShade, 0.4)
			paintRect(bx+4, by+4+sleeveLen, armW, remH, skin, 0.35)
			paintRect(bx+4+armW, by+4+sleeveLen, 4, remH, skinDeep, 0.4)
			paintRect(bx+8+armW, by+4+sleeveLen, armW, remH, skinShade, 0.4)
		}
		if sleeveLen >= 4 {
			cuffColor := topAcc
			if r.SleeveStyle == "rolled" {
				cuffColor = top1Hi
			}
			paintRect(sx, sy+4+sleeveLen-1, 4, 1, cuffColor, 0.3)
			paintRect(sx+4, sy+4+sleeveLen-1, armW, 1, cuffColor, 0.3)
			paintRect(sx+4+armW, sy+4+sleeveLen-1, 4, 1, cuffColor, 0.3)
			paintRect(sx+8+armW, sy+4+sleeveLen-1, armW, 1, cuffColor, 0.3)
		}
	}
	paintArm(40, 16, 40, 32)
	paintArm(32, 48, 48, 48)

	// 4. Legs: Right (0,16 + pants 0,32) and Left (16,48 + pants 0,48)
	paintLeg := func(bx, by, ox, oy int) {
		paintRect(bx+4, by, 4, 4, botAcc, 0.45)
		paintRect(bx+8, by, 4, 4, shoes, 0.5)
		switch r.BottomStyle {
		case "skirt":
			paintRect(bx, by+4, 16, 4, top1, 0.7)
			paintRect(bx, by+7, 16, 1, top2, 0.5)
			paintRect(ox, oy+4, 16, 4, top1Hi, 0.5)
			paintRect(bx, by+8, 16, 3, skin, 0.35)
			paintRect(bx, by+11, 16, 2, topAcc, 0.4)
		case "shorts":
			paintRect(bx, by+4, 16, 5, bot1, 0.75)
			paintRect(bx, by+8, 16, 1, bot1AO, 0.35)
			paintRect(bx, by+9, 16, 4, skin, 0.35)
		case "armor_greaves":
			paintRect(bx, by+4, 16, 9, bot1, 0.7)
			paintRect(bx+4, by+7, 4, 2, topAcc, 0.35)
			paintRect(ox+4, oy+6, 4, 5, botAcc, 0.45)
		default: // pants
			paintRect(bx, by+4, 16, 9, bot1, 0.75)
			if detailOn {
				paintRect(bx+4, by+4, 4, 1, bot1AO, 0.35)
				paintRect(bx+4, by+8, 4, 1, bot1AO, 0.35)
			}
			paintRect(bx+4, by+5, 2, 7, botAcc, 0.45)
		}
		// Detailed boots/sneakers + sole
		paintRect(bx, by+13, 16, 2, shoes, 0.55)
		paintRect(bx, by+15, 16, 1, shadeHueShift(shoes, 0.72, -0.04), 0.3)
		paintRect(bx+4, by+13, 4, 1, shoesHi, 0.35)
		paintRect(ox+4, oy+13, 4, 1, topAcc, 0.3)
	}
	paintLeg(0, 16, 0, 32)
	paintLeg(16, 48, 0, 48)

	return img
}

func shadeHueShift(c color.NRGBA, valFactor, hueShiftFrac float64) color.NRGBA {
	if c.A == 0 {
		return c
	}
	h, s, v := rgbToHSV(c)
	if valFactor < 1.0 {
		s = min(1.0, s*(1.0+(1.0-valFactor)*0.16))
	} else if valFactor > 1.0 {
		s = max(0.0, s*(1.0-(valFactor-1.0)*0.10))
	}
	h = math.Mod(h+hueShiftFrac+1.0, 1.0)
	v = min(1.0, max(0.0, v*valFactor))
	out := hsvToRGB(h, s, v)
	out.A = c.A
	return out
}

func mixNRGBA(a, b color.NRGBA, t float64) color.NRGBA {
	t = min(1.0, max(0.0, t))
	return color.NRGBA{
		R: uint8(math.Round(float64(a.R)*(1-t) + float64(b.R)*t)),
		G: uint8(math.Round(float64(a.G)*(1-t) + float64(b.G)*t)),
		B: uint8(math.Round(float64(a.B)*(1-t) + float64(b.B)*t)),
		A: a.A,
	}
}

func rgbToHSV(c color.NRGBA) (h, s, v float64) {
	r := float64(c.R) / 255.0
	g := float64(c.G) / 255.0
	b := float64(c.B) / 255.0
	mx := max(r, max(g, b))
	mn := min(r, min(g, b))
	d := mx - mn
	v = mx
	if mx > 0 {
		s = d / mx
	}
	if d == 0 {
		return 0, s, v
	}
	switch mx {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	case b:
		h = (r-g)/d + 4
	}
	h /= 6.0
	return h, s, v
}

func hsvToRGB(h, s, v float64) color.NRGBA {
	if s <= 0 {
		u := uint8(min(255, max(0, math.Round(v*255.0))))
		return color.NRGBA{R: u, G: u, B: u, A: 255}
	}
	hf := math.Mod(h, 1.0) * 6.0
	i := int(math.Floor(hf))
	f := hf - float64(i)
	p := v * (1.0 - s)
	q := v * (1.0 - s*f)
	t := v * (1.0 - s*(1.0-f))
	var r, g, b float64
	switch i % 6 {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	case 5:
		r, g, b = v, p, q
	}
	return color.NRGBA{
		R: uint8(min(255, max(0, math.Round(r*255.0)))),
		G: uint8(min(255, max(0, math.Round(g*255.0)))),
		B: uint8(min(255, max(0, math.Round(b*255.0)))),
		A: 255,
	}
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
