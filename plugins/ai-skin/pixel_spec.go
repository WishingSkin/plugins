package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	pixelBaseParts    = []string{"head", "torso", "right_leg", "left_leg", "right_arm", "left_arm"}
	pixelOverlayParts = []string{"hat", "jacket", "right_pant", "left_pant", "right_sleeve", "left_sleeve"}
	pixelFaceOrder    = []string{"top", "bottom", "right", "front", "left", "back"}
)

// PixelRows accepts either an array of row strings or a single whitespace-delimited string.
type PixelRows []string

// UnmarshalJSON decodes row grids flexibly without failing on compact whitespace-joined strings.
func (p *PixelRows) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*p = nil
		return nil
	}
	if trimmed[0] == '[' {
		var arr []string
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return err
		}
		out := make([]string, len(arr))
		for i, row := range arr {
			out[i] = strings.TrimRight(row, "\r\n")
		}
		*p = out
		return nil
	}
	var single string
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return err
	}
	fields := strings.Fields(single)
	*p = PixelRows(fields)
	return nil
}

// SkinPixelSpec represents a D2-Strict per-face 64x64 Minecraft skin grid with palette lookup.
type SkinPixelSpec struct {
	Summary              string                         `json:"summary,omitempty"`
	Model                string                         `json:"model,omitempty"`
	BaseSkinColor        string                         `json:"baseSkinColor,omitempty"`
	BaseSkinColorSnake   string                         `json:"base_skin_color,omitempty"`
	MirrorLeftArm        *bool                          `json:"mirrorLeftArmFromRight,omitempty"`
	MirrorLeftArmSnake   *bool                          `json:"mirror_left_arm_from_right,omitempty"`
	MirrorLeftLeg        *bool                          `json:"mirrorLeftLegFromRight,omitempty"`
	MirrorLeftLegSnake   *bool                          `json:"mirror_left_leg_from_right,omitempty"`
	Palette              map[string]string              `json:"palette"`
	Faces                map[string]map[string]PixelRows `json:"faces"`
}

func fillRectNRGBA(img *image.NRGBA, x0, y0, w, h int, c color.NRGBA) {
	b := img.Bounds()
	for dy := 0; dy < h; dy++ {
		y := y0 + dy
		if y < b.Min.Y || y >= b.Max.Y {
			continue
		}
		for dx := 0; dx < w; dx++ {
			x := x0 + dx
			if x >= b.Min.X && x < b.Max.X {
				img.SetNRGBA(x, y, c)
			}
		}
	}
}

func getSkinUVTable(slim bool) map[string]map[string]uvRect {
	armW := 4
	if slim {
		armW = 3
	}
	box := func(ox, oy, w, h, d int) map[string]uvRect {
		return map[string]uvRect{
			"top":    {X: ox + d, Y: oy, W: w, H: d},
			"bottom": {X: ox + d + w, Y: oy, W: w, H: d},
			"right":  {X: ox, Y: oy + d, W: d, H: h},
			"front":  {X: ox + d, Y: oy + d, W: w, H: h},
			"left":   {X: ox + d + w, Y: oy + d, W: d, H: h},
			"back":   {X: ox + d + w + d, Y: oy + d, W: w, H: h},
		}
	}
	return map[string]map[string]uvRect{
		"head":         box(0, 0, 8, 8, 8),
		"torso":        box(16, 16, 8, 12, 4),
		"right_leg":    box(0, 16, 4, 12, 4),
		"left_leg":     box(16, 48, 4, 12, 4),
		"right_arm":    box(40, 16, armW, 12, 4),
		"left_arm":     box(32, 48, armW, 12, 4),
		"hat":          box(32, 0, 8, 8, 8),
		"jacket":       box(16, 32, 8, 12, 4),
		"right_pant":   box(0, 32, 4, 12, 4),
		"left_pant":    box(0, 48, 4, 12, 4),
		"right_sleeve": box(40, 32, armW, 12, 4),
		"left_sleeve":  box(48, 48, armW, 12, 4),
	}
}

func isOverlayPartName(part string) bool {
	for _, p := range pixelOverlayParts {
		if p == part {
			return true
		}
	}
	return false
}

func tryParseHexColor(raw string) (color.NRGBA, bool) {
	if c, ok := tryParseHex(raw); ok {
		return c, true
	}
	s := strings.TrimSpace(strings.TrimPrefix(raw, "#"))
	if len(s) != 8 {
		return color.NRGBA{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.NRGBA{}, false
	}
	return color.NRGBA{
		R: uint8((v >> 24) & 0xff),
		G: uint8((v >> 16) & 0xff),
		B: uint8((v >> 8) & 0xff),
		A: uint8(v & 0xff),
	}, true
}

func formatPixelHex(c color.NRGBA) string {
	if c.A == 255 {
		return formatHex(c)
	}
	return fmt.Sprintf("#%02X%02X%02X%02X", c.R, c.G, c.B, c.A)
}

func isAllTransparentRows(rows PixelRows) bool {
	if len(rows) == 0 {
		return true
	}
	for _, row := range rows {
		for _, ch := range row {
			if ch != '.' && ch != ' ' {
				return false
			}
		}
	}
	return true
}

// NormalizePixelSpec canonicalizes fields, validates hex colors, and strips empty overlay faces.
func NormalizePixelSpec(spec *SkinPixelSpec, fallbackModel string) *SkinPixelSpec {
	if spec == nil {
		return nil
	}
	out := &SkinPixelSpec{
		Summary: strings.TrimSpace(spec.Summary),
		Palette: map[string]string{},
		Faces:   map[string]map[string]PixelRows{},
	}
	m := strings.ToLower(strings.TrimSpace(spec.Model))
	switch m {
	case "slim":
		out.Model = "slim"
	case "default", "classic":
		out.Model = "default"
	default:
		if fallbackModel == "slim" {
			out.Model = "slim"
		} else {
			out.Model = "default"
		}
	}

	rawSkinColor := strings.TrimSpace(spec.BaseSkinColor)
	if rawSkinColor == "" {
		rawSkinColor = strings.TrimSpace(spec.BaseSkinColorSnake)
	}
	if c, ok := tryParseHexColor(rawSkinColor); ok {
		c.A = 255
		out.BaseSkinColor = formatHex(c)
	} else {
		out.BaseSkinColor = "#F2C9AC"
	}

	armMirror := spec.MirrorLeftArm
	if armMirror == nil {
		armMirror = spec.MirrorLeftArmSnake
	}
	if armMirror != nil {
		v := *armMirror
		out.MirrorLeftArm = &v
	}

	legMirror := spec.MirrorLeftLeg
	if legMirror == nil {
		legMirror = spec.MirrorLeftLegSnake
	}
	if legMirror != nil {
		v := *legMirror
		out.MirrorLeftLeg = &v
	}

	for k, hexVal := range spec.Palette {
		if utf8.RuneCountInString(k) != 1 || k == "." || k == " " {
			continue
		}
		if c, ok := tryParseHexColor(hexVal); ok {
			out.Palette[k] = formatPixelHex(c)
		}
	}

	for partRaw, faces := range spec.Faces {
		part := strings.ToLower(strings.TrimSpace(partRaw))
		if part == "" || len(faces) == 0 {
			continue
		}
		cleanFaces := map[string]PixelRows{}
		for faceRaw, rows := range faces {
			face := strings.ToLower(strings.TrimSpace(faceRaw))
			if face == "" || len(rows) == 0 {
				continue
			}
			if isOverlayPartName(part) && isAllTransparentRows(rows) {
				continue
			}
			cp := make(PixelRows, len(rows))
			copy(cp, rows)
			cleanFaces[face] = cp
		}
		if len(cleanFaces) > 0 {
			out.Faces[part] = cleanFaces
		}
	}
	return out
}

// ClonePixelSpec deep-copies a SkinPixelSpec safely.
func ClonePixelSpec(spec *SkinPixelSpec) *SkinPixelSpec {
	if spec == nil {
		return nil
	}
	return NormalizePixelSpec(spec, spec.Model)
}

// MergePixelSpec applies a partial or full patch onto a base SkinPixelSpec.
func MergePixelSpec(base, patch *SkinPixelSpec) *SkinPixelSpec {
	if base == nil {
		return ClonePixelSpec(patch)
	}
	merged := ClonePixelSpec(base)
	if patch == nil {
		return merged
	}
	if s := strings.TrimSpace(patch.Summary); s != "" {
		merged.Summary = s
	}
	if patch.Model == "slim" || patch.Model == "default" {
		merged.Model = patch.Model
	}
	rawSkinColor := strings.TrimSpace(patch.BaseSkinColor)
	if rawSkinColor == "" {
		rawSkinColor = strings.TrimSpace(patch.BaseSkinColorSnake)
	}
	if c, ok := tryParseHexColor(rawSkinColor); ok {
		c.A = 255
		merged.BaseSkinColor = formatHex(c)
	}
	if patch.MirrorLeftArm != nil {
		v := *patch.MirrorLeftArm
		merged.MirrorLeftArm = &v
	} else if patch.MirrorLeftArmSnake != nil {
		v := *patch.MirrorLeftArmSnake
		merged.MirrorLeftArm = &v
	}
	if patch.MirrorLeftLeg != nil {
		v := *patch.MirrorLeftLeg
		merged.MirrorLeftLeg = &v
	} else if patch.MirrorLeftLegSnake != nil {
		v := *patch.MirrorLeftLegSnake
		merged.MirrorLeftLeg = &v
	}

	for k, hexVal := range patch.Palette {
		if utf8.RuneCountInString(k) != 1 || k == "." || k == " " {
			continue
		}
		if c, ok := tryParseHexColor(hexVal); ok {
			merged.Palette[k] = formatPixelHex(c)
		}
	}

	for partRaw, patchFaces := range patch.Faces {
		part := strings.ToLower(strings.TrimSpace(partRaw))
		if part == "" || len(patchFaces) == 0 {
			continue
		}
		if merged.Faces[part] == nil {
			merged.Faces[part] = map[string]PixelRows{}
		}
		for faceRaw, rows := range patchFaces {
			face := strings.ToLower(strings.TrimSpace(faceRaw))
			if face == "" {
				continue
			}
			if isOverlayPartName(part) && isAllTransparentRows(rows) {
				delete(merged.Faces[part], face)
				continue
			}
			cp := make(PixelRows, len(rows))
			copy(cp, rows)
			merged.Faces[part][face] = cp
		}
		if len(merged.Faces[part]) == 0 {
			delete(merged.Faces, part)
		}
	}
	return merged
}

// ValidateAndRenderPixelSpec checks UV dimensions, palette keys, and completeness before rendering 64x64 RGBA pixels.
func ValidateAndRenderPixelSpec(spec *SkinPixelSpec, slim bool) (*image.NRGBA, []string, error) {
	if spec == nil {
		errs := []string{"pixel spec is nil"}
		return nil, errs, errors.New(errs[0])
	}
	uvTable := getSkinUVTable(slim)

	// Build resolved palette map keyed by rune
	paletteMap := map[rune]color.NRGBA{}
	for k, rawHex := range spec.Palette {
		r, size := utf8.DecodeRuneInString(k)
		if r == utf8.RuneError || size != len(k) || r == '.' || r == ' ' {
			continue
		}
		if c, ok := tryParseHexColor(rawHex); ok {
			paletteMap[r] = c
		}
	}

	var errs []string
	// Validate every supplied part/face geometry and palette references
	for partRaw, faces := range spec.Faces {
		part := strings.ToLower(strings.TrimSpace(partRaw))
		uvFaces, ok := uvTable[part]
		if !ok {
			errs = append(errs, fmt.Sprintf("unknown part %q", partRaw))
			continue
		}
		for faceRaw, rows := range faces {
			face := strings.ToLower(strings.TrimSpace(faceRaw))
			rect, ok := uvFaces[face]
			if !ok {
				errs = append(errs, fmt.Sprintf("unknown face %s.%s", part, faceRaw))
				continue
			}
			if len(rows) != rect.H {
				gotW := 0
				if len(rows) > 0 {
					gotW = utf8.RuneCountInString(rows[0])
				}
				errs = append(errs, fmt.Sprintf("%s.%s expected %dx%d, got %dx%d", part, face, rect.W, rect.H, gotW, len(rows)))
				continue
			}
			for rIdx, row := range rows {
				rw := utf8.RuneCountInString(row)
				if rw != rect.W {
					errs = append(errs, fmt.Sprintf("%s.%s expected %dx%d (row %d width %d)", part, face, rect.W, rect.H, rIdx, rw))
					break
				}
				for _, ch := range row {
					if ch == '.' || ch == ' ' {
						continue
					}
					if _, exists := paletteMap[ch]; !exists {
						errs = append(errs, fmt.Sprintf("undefined palette symbol %q in %s.%s", string(ch), part, face))
						break
					}
				}
			}
		}
	}

	mirrorArm := (spec.MirrorLeftArm != nil && *spec.MirrorLeftArm) || (spec.MirrorLeftArm == nil && spec.MirrorLeftArmSnake != nil && *spec.MirrorLeftArmSnake)
	mirrorLeg := (spec.MirrorLeftLeg != nil && *spec.MirrorLeftLeg) || (spec.MirrorLeftLeg == nil && spec.MirrorLeftLegSnake != nil && *spec.MirrorLeftLegSnake)

	// Enforce completeness on all required base faces
	for _, part := range pixelBaseParts {
		if part == "left_arm" && mirrorArm {
			continue
		}
		if part == "left_leg" && mirrorLeg {
			continue
		}
		pf := spec.Faces[part]
		for _, fname := range pixelFaceOrder {
			if len(pf[fname]) == 0 {
				errs = append(errs, fmt.Sprintf("missing required base face %s.%s", part, fname))
			}
		}
	}

	if len(errs) > 0 {
		return nil, errs, errors.New(strings.Join(errs, "; "))
	}

	// Render 64x64 image
	skinHex := strings.TrimSpace(spec.BaseSkinColor)
	if skinHex == "" {
		skinHex = strings.TrimSpace(spec.BaseSkinColorSnake)
	}
	baseSkinRGBA, ok := tryParseHexColor(skinHex)
	if !ok {
		baseSkinRGBA = color.NRGBA{242, 201, 172, 255}
	}
	baseSkinRGBA.A = 255

	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for _, part := range pixelBaseParts {
		for _, rect := range uvTable[part] {
			fillRectNRGBA(img, rect.X, rect.Y, rect.W, rect.H, baseSkinRGBA)
		}
	}

	drawFace := func(part, face string, rows PixelRows, forceOpaque bool) {
		rect, ok := uvTable[part][face]
		if !ok || len(rows) != rect.H {
			return
		}
		for dy, row := range rows {
			dx := 0
			for _, ch := range row {
				if dx >= rect.W {
					break
				}
				if ch != '.' && ch != ' ' {
					if c, ok := paletteMap[ch]; ok {
						if forceOpaque {
							c.A = 255
						}
						if c.A > 0 {
							img.SetNRGBA(rect.X+dx, rect.Y+dy, c)
						}
					}
				}
				dx++
			}
		}
	}

	drawPartFaces := func(part string, forceOpaque bool) {
		pf := spec.Faces[part]
		for _, fname := range pixelFaceOrder {
			if rows := pf[fname]; len(rows) > 0 {
				drawFace(part, fname, rows, forceOpaque)
			}
		}
	}

	// Draw non-left parts first
	for _, part := range []string{"head", "torso", "right_arm", "right_leg"} {
		drawPartFaces(part, true)
	}
	for _, part := range []string{"hat", "jacket", "right_sleeve", "right_pant"} {
		drawPartFaces(part, false)
	}

	mirrorPart := func(srcPart, dstPart string) {
		swap := map[string]string{
			"front": "front", "back": "back", "top": "top", "bottom": "bottom",
			"left": "right", "right": "left",
		}
		for sFace, dFace := range swap {
			sr := uvTable[srcPart][sFace]
			dr := uvTable[dstPart][dFace]
			for y := 0; y < min(sr.H, dr.H); y++ {
				for x := 0; x < min(sr.W, dr.W); x++ {
					img.SetNRGBA(dr.X+dr.W-1-x, dr.Y+y, img.NRGBAAt(sr.X+x, sr.Y+y))
				}
			}
		}
	}

	if mirrorArm {
		mirrorPart("right_arm", "left_arm")
		mirrorPart("right_sleeve", "left_sleeve")
	}
	if mirrorLeg {
		mirrorPart("right_leg", "left_leg")
		mirrorPart("right_pant", "left_pant")
	}

	// Explicit left limbs & overlays override mirrored pixels cleanly (supports asymmetric cuffs/gloves)
	drawPartFaces("left_arm", true)
	drawPartFaces("left_leg", true)
	drawPartFaces("left_sleeve", false)
	drawPartFaces("left_pant", false)

	// Require non-trivial detail: at least 4 distinct non-skin RGB colors
	nonSkinColors := map[uint32]struct{}{}
	skinKey := (uint32(baseSkinRGBA.R) << 16) | (uint32(baseSkinRGBA.G) << 8) | uint32(baseSkinRGBA.B)
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			c := img.NRGBAAt(x, y)
			if c.A == 0 {
				continue
			}
			k := (uint32(c.R) << 16) | (uint32(c.G) << 8) | uint32(c.B)
			if k != skinKey {
				nonSkinColors[k] = struct{}{}
			}
		}
	}
	if len(nonSkinColors) < 4 {
		errs = append(errs, "skin lacks visual detail: fewer than 4 non-skin colors")
		return nil, errs, errors.New(errs[0])
	}

	return img, nil, nil
}
