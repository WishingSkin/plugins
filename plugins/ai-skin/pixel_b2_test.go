package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/WishingSkin/plugins/shared/skinpng"
	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

func boolPtr(v bool) *bool { return &v }

func makeSamplePixelSpec(slim bool) *SkinPixelSpec {
	model := "default"
	armFront := PixelRows{
		"mmmm", "pmnp", "qmnq", "pmnp", "mmnp", "pmnp",
		"mmnp", "nmpn", "pqmp", "kkkm", "k6kk", "kkkk",
	}
	armTop := PixelRows{"mpmp", "ppmp", "pmpm", "mpmp"}
	if slim {
		model = "slim"
		armFront = PixelRows{
			"mpm", "pmn", "qmn", "pmn", "mmn", "pmn",
			"mmn", "nmp", "pqm", "kkm", "m6k", "kkk",
		}
		armTop = PixelRows{"mpm", "ppm", "pmp", "mpm"}
	}
	return &SkinPixelSpec{
		Summary:       "赛博黑客少女测试样张",
		Model:         model,
		BaseSkinColor: "#F2C9AC",
		MirrorLeftArm: boolPtr(true),
		MirrorLeftLeg: boolPtr(true),
		Palette: map[string]string{
			"a": "#291C43", "b": "#4A276B", "c": "#743695", "d": "#A450BE",
			"e": "#D277DC", "1": "#AB6B80", "2": "#CD8F96", "3": "#EBAEAA",
			"4": "#FFD0BA", "5": "#F3E2DD", "6": "#563252", "k": "#181B29",
			"n": "#232838", "m": "#31374A", "p": "#444C61", "q": "#606984",
			"T": "#254D64", "B": "#276AB4", "C": "#36BCFA", "K": "#142530",
			"X": "#9B488E",
		},
		Faces: map[string]map[string]PixelRows{
			"head": {
				"top":    {"abbbbbba", "bbcbbccb", "bcccddcb", "bccddedc", "ccddeedc", "cddeeddc", "cdeeddcb", "bddccbbb"},
				"bottom": {"baaabbbb", "aaaaaabb", "aabbaaab", "abbaaaab", "abaaabbb", "b211112b", "b222222b", "b123332b"},
				"right":  {"abbbbccb", "bbccddcc", "bccdecbd", "ccddecbe", "cdeeccbd", "deddcdcd", "cdccdddd", "bcabbccb"},
				"front":  {"bddccbbb", "cdeddcba", "decec3ab", "ebc3322c", "d564465c", "d434443d", "c234332c", "b123332b"},
				"left":   {"bbbbbbba", "abccdcbb", "bcddcccb", "ccdedccc", "cedddcdc", "dedceedd", "cdccdedd", "bbcadccb"},
				"back":   {"abbbbbba", "bbccdcbb", "bcddcccb", "ccddcdcc", "cddcdeec", "deddeddd", "dddccddc", "bbccbcab"},
			},
			"hat": {
				"front": {"........", "........", ".mppppm.", "qTCBBCTq", "KCBCCBKK", "........", "........", "........"},
			},
			"torso": {
				"top":    {"mmmnnmmm", "mppnnppm", "pmn11nmp", "mmm22mmm"},
				"bottom": {"nnkknknn", "nnnknnnn", "nkknnnkn", "nnnknnnn"},
				"right":  {"nmmm", "nmmp", "mnpm", "pmpn", "mmpn", "mpmn", "nmmn", "nmmn", "nkmn", "nmmn", "pmmp", "nknn"},
				"front":  {"mmm22mmm", "pmk33kmp", "mpkkknpm", "npknnmpn", "nmpkmpmn", "nmqkmqmn", "nmnkmnmn", "nmkkmnmn", "nmnkmnmn", "nmkkmnmn", "ppmkmmpp", "nnkknknn"},
				"left":   {"mmmn", "pmnn", "mpnm", "npmp", "npmm", "nmpm", "nmmn", "nmmn", "nmkn", "nmmn", "pmmp", "nnkn"},
				"back":   {"nnmmmmnn", "nmpmmmpn", "mpmppmpm", "pmmnnmmp", "mnpmmpnm", "mmpmmpmm", "nnmmmmnn", "nmpmnpmn", "nnmnnmnn", "nmnmmnmn", "ppmmmmpp", "nnnknnnn"},
			},
			"right_arm": {
				"top":    armTop,
				"bottom": armTop,
				"right":  {"mpmm", "pmpp", "qppm", "pmpm", "mmnm", "mpnm", "mmnp", "mmpn", "mppm", "kmmk", "m6kk", "kkkk"},
				"front":  armFront,
				"left":   {"mmpm", "nmpm", "nmpq", "nmpm", "nmmm", "nmpm", "nmmm", "pnmn", "mqqm", "mkkk", "kmkk", "kkkk"},
				"back":   armFront,
			},
			"right_leg": {
				"top":    {"nmmk", "mnnk", "mnnk", "nnnk"},
				"bottom": {"kkkk", "kmnk", "knmk", "kkkk"},
				"right":  {"nnmn", "nmmm", "nmpp", "mpmm", "ppmn", "mpnn", "mpnn", "pmpn", "pmpp", "qmpq", "mpnk", "kkkk"},
				"front":  {"nmmk", "mpnk", "pmmk", "mppn", "npmn", "nmnn", "nmkk", "nmpn", "pppn", "qmpn", "kpmm", "kkkk"},
				"left":   {"kmmn", "knmn", "kmmn", "nmmn", "nmpn", "nnnn", "kmmn", "nmpn", "npmp", "npmq", "mmnm", "kkkk"},
				"back":   {"nnmn", "nmmn", "nmmn", "nmmm", "npmm", "npmm", "nnnm", "nmpp", "ppmp", "qmpq", "mmpm", "kkkk"},
			},
		},
	}
}

func TestPixelSpecRenderMirrorOverrideAndValidation(t *testing.T) {
	// 1. Slim model with whitespace-string PixelRows and asymmetric left_arm override.
	spec := makeSamplePixelSpec(true)
	spec.Faces["left_arm"] = map[string]PixelRows{
		"front": {"...", "...", "...", "...", "...", "...", "...", "...", "...", "CCC", "CCC", "..."},
	}
	rawJSON := mustJSON(spec)
	var decoded SkinPixelSpec
	if err := json.Unmarshal(rawJSON, &decoded); err != nil {
		t.Fatalf("unmarshal spec: %v", err)
	}
	// Replace torso.top via whitespace-delimited single string form
	var rowsFromString PixelRows
	if err := json.Unmarshal([]byte(`"mmmnnmmm mppnnppm pmn11nmp mmm22mmm"`), &rowsFromString); err != nil || len(rowsFromString) != 4 {
		t.Fatalf("expected string PixelRows unmarshal to 4 rows, got %v err=%v", rowsFromString, err)
	}
	decoded.Faces["torso"]["top"] = rowsFromString

	norm := NormalizePixelSpec(&decoded, "slim")
	img, errs, err := ValidateAndRenderPixelSpec(norm, true)
	if err != nil || len(errs) != 0 {
		t.Fatalf("slim PixelSpec render failed: err=%v errs=%v", err, errs)
	}
	cleaned, err := SanitizeAndValidateSkin(img, true)
	if err != nil {
		t.Fatalf("SanitizeAndValidateSkin failed: %v", err)
	}
	if cleaned.NRGBAAt(0, 0).A != 0 {
		t.Fatalf("dead zone (0,0) must be transparent")
	}
	// Mirrored left_arm top row was populated AND left_arm.front row 9 override ("CCC" -> #36BCFA) took effect
	if cleaned.NRGBAAt(36, 52).A != 255 {
		t.Fatalf("expected mirrored left_arm top face to be opaque")
	}
	gotC := cleaned.NRGBAAt(36, 52+9)
	if gotC != (color.NRGBA{0x36, 0xBC, 0xFA, 255}) {
		t.Fatalf("expected asymmetric left_arm override #36BCFA at (36,61), got %+v", gotC)
	}

	// 2. Classic 4px model validation.
	classicNorm := NormalizePixelSpec(makeSamplePixelSpec(false), "default")
	classicImg, cerrs, cerr := ValidateAndRenderPixelSpec(classicNorm, false)
	if cerr != nil || len(cerrs) != 0 {
		t.Fatalf("classic render failed: err=%v errs=%v", cerr, cerrs)
	}
	if _, err := SanitizeAndValidateSkin(classicImg, false); err != nil {
		t.Fatalf("classic sanitize failed: %v", err)
	}

	// 3. MergePixelSpec preserves untouched parts and merges patch faces & colors.
	patch := &SkinPixelSpec{
		Palette: map[string]string{"Z": "#FF4D9D"},
		Faces: map[string]map[string]PixelRows{
			"hat": {"front": {"........", "........", "........", ".ZZZZZZ.", "........", "........", "........", "........"}},
		},
	}
	merged := MergePixelSpec(norm, patch)
	if merged.Palette["Z"] != "#FF4D9D" || len(merged.Faces["torso"]["back"]) != 12 {
		t.Fatalf("MergePixelSpec lost base faces or missed palette patch: %+v", merged)
	}

	// 4. Validation errors catch wrong height, wrong width, missing base face, and undefined symbol.
	bad := ClonePixelSpec(classicNorm)
	bad.Faces["torso"]["back"] = bad.Faces["torso"]["back"][:10]
	delete(bad.Faces["torso"], "left")
	_, valErrs, err := ValidateAndRenderPixelSpec(bad, false)
	if err == nil || !strings.Contains(strings.Join(valErrs, "\n"), "torso.back expected 8x12") || !strings.Contains(strings.Join(valErrs, "\n"), "missing required base face torso.left") {
		t.Fatalf("expected detailed dimension & missing face validation errors, got: %v", valErrs)
	}
}

func TestStructuredLLMPixelGridAutoRepairAndChatEdit(t *testing.T) {
	resetJobManagerForTest()

	var calls atomic.Int32
	var sawRepairHint atomic.Bool
	var sawRefinePatch atomic.Bool

	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		bodyBytes := new(bytes.Buffer)
		_, _ = bodyBytes.ReadFrom(r.Body)
		bodyStr := bodyBytes.String()

		switch n {
		case 1:
			// First response deliberately misses torso.back to trigger 1 automatic repair turn.
			incomplete := makeSamplePixelSpec(true)
			delete(incomplete.Faces["torso"], "back")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"content": string(mustJSON(incomplete))}}},
			})
		case 2:
			if strings.Contains(bodyStr, "torso.back") {
				sawRepairHint.Store(true)
			}
			full := makeSamplePixelSpec(true)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"content": string(mustJSON(full))}}},
			})
		default:
			if strings.Contains(bodyStr, "Refine the existing pixel skin spec") {
				sawRefinePatch.Store(true)
			}
			patch := map[string]any{
				"summary": "粉红霓虹护目镜微调版",
				"palette": map[string]string{"Z": "#FF4D9D"},
				"faces": map[string]any{
					"hat": map[string]any{
						"front": []string{"........", "........", "........", ".ZZZZZZ.", ".ZCCBCZ.", "........", "........", "........"},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"content": string(mustJSON(patch))}}},
			})
		}
	}))
	defer llmSrv.Close()

	settings := map[string]any{
		"default_engine":   "structured_llm",
		"llm_api_url":      llmSrv.URL + "/v1",
		"llm_api_key":      "sk-d2",
		"llm_render_mode":  "pixel_grid",
		"daily_limit":      float64(5),
		"cooldown_seconds": float64(0),
	}
	data := map[string]json.RawMessage{}
	user := sdk.Principal{ID: 31}

	gen1, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"prompt": "紫色短发赛博少女", "engine": "structured_llm", "model": "slim"}),
	})
	if gen1.(sdk.RuntimeResponse).Status != 202 {
		t.Fatalf("generate 1 failed: %+v", gen1)
	}
	var s1 struct{ JobID string `json:"jobId"` }
	_ = json.Unmarshal(gen1.(sdk.RuntimeResponse).Body, &s1)

	j1, _ := pollUntilTerminal(t, s1.JobID, settings, data, user)
	if j1.Status != "succeeded" || !sawRepairHint.Load() || j1.PixelSpec == nil {
		t.Fatalf("expected repaired D2-Strict job to succeed: status=%s err=%q sawRepair=%v specNil=%v", j1.Status, j1.Error, sawRepairHint.Load(), j1.PixelSpec == nil)
	}
	if len(data[histItemPrefix+s1.JobID]) > 32<<10 {
		t.Fatalf("persisted pixelSpec hist_item too large: %d bytes", len(data[histItemPrefix+s1.JobID]))
	}

	// Simulate restart, then Chat-to-Edit patch via baseJobId only.
	resetJobManagerForTest()

	gen2, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"prompt": "把护目镜边框改成亮粉色", "engine": "structured_llm", "model": "slim", "baseJobId": s1.JobID}),
	})
	var s2 struct{ JobID string `json:"jobId"` }
	_ = json.Unmarshal(gen2.(sdk.RuntimeResponse).Body, &s2)
	j2, _ := pollUntilTerminal(t, s2.JobID, settings, data, user)
	if j2.Status != "succeeded" || !sawRefinePatch.Load() || j2.PixelSpec == nil {
		t.Fatalf("expected patch refine to succeed: status=%s err=%q sawRefine=%v", j2.Status, j2.Error, sawRefinePatch.Load())
	}
	raw2, _ := base64.StdEncoding.DecodeString(j2.PNGBase64)
	img2, _ := skinpng.DecodePNG(raw2)
	// Hat front (40,8) + (1,3) -> (41, 11) should now be #FF4D9D
	if img2.NRGBAAt(41, 11) != (color.NRGBA{0xFF, 0x4D, 0x9D, 255}) {
		t.Fatalf("expected hat front pixel (41,11) patched to #FF4D9D, got %+v", img2.NRGBAAt(41, 11))
	}
}

func makeSyntheticTurnaroundPNG(slim bool, shirtColor color.NRGBA) []byte {
	spec := makeSamplePixelSpec(slim)
	skin, _, _ := ValidateAndRenderPixelSpec(NormalizePixelSpec(spec, spec.Model), slim)
	// Paint torso front area in distinct shirtColor so color diversity & quantization are easy to verify
	fillRectNRGBA(skin, 20, 20, 8, 12, shirtColor)

	sheet := RenderOrthoTurnaroundSheet(skin, slim)
	decoded, _ := skinpng.DecodePNG(sheet)
	// Inject slight gradients across body + 1px anti-aliased green fringe beside torso silhouette so both fringe removal and 36-color quantization are exercised.
	b := decoded.Bounds()
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			c := decoded.NRGBAAt(x, y)
			if isChromaGreenPixel(c) {
				continue
			}
			// Vary brightness subtly per pixel to inflate color count > 100
			shift := uint8((x + y) % 19)
			c.R = uint8(min(255, max(0, int(c.R)+int(shift)-9)))
			c.B = uint8(min(255, max(0, int(c.B)+(y%7)-3)))
			decoded.SetNRGBA(x, y, c)
		}
	}
	// Inject 1px green halo pixel right beside left character shoulder
	decoded.SetNRGBA(47, 300, color.NRGBA{20, 210, 20, 255})
	out, _ := skinpng.EncodePNG(decoded)
	return out
}

func TestOrthoB2ProjectionClassicSlimChromaKeyAndQuantize(t *testing.T) {
	// 1. Direct small 64x64 and 1024x1024 UV sheets must NOT mis-trigger LooksLikeOrthoTurnaround.
	small := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	if LooksLikeOrthoTurnaround(small) {
		t.Fatalf("64x64 image must never trigger LooksLikeOrthoTurnaround")
	}
	uvImg, err := skinpng.DecodePNG(make1024x1024ValidSkinWithGarbageDeadZones())
	if err != nil {
		t.Fatalf("decode UV sheet: %v", err)
	}
	if LooksLikeOrthoTurnaround(uvImg) {
		t.Fatalf("1024x1024 UV unwrap sheet must NOT trigger LooksLikeOrthoTurnaround")
	}

	// 2. Test both slim=false (classic) and slim=true turnaround projection.
	for _, slim := range []bool{false, true} {
		rawPNG := makeSyntheticTurnaroundPNG(slim, color.NRGBA{39, 106, 180, 255})
		srcImg, err := skinpng.DecodePNG(rawPNG)
		if err != nil {
			t.Fatalf("decode turnaround (slim=%v): %v", slim, err)
		}
		if !LooksLikeOrthoTurnaround(srcImg) {
			t.Fatalf("synthetic turnaround (slim=%v) must trigger LooksLikeOrthoTurnaround", slim)
		}
		skin, err := ProjectOrthoTurnaroundToSkin(srcImg, slim, 36)
		if err != nil {
			t.Fatalf("ProjectOrthoTurnaroundToSkin (slim=%v) failed: %v", slim, err)
		}
		cleaned, err := SanitizeAndValidateSkin(skin, slim)
		if err != nil {
			t.Fatalf("SanitizeAndValidateSkin after B2 projection (slim=%v) failed: %v", slim, err)
		}

		uniqueColors := map[uint32]struct{}{}
		for y := 0; y < 64; y++ {
			for x := 0; x < 64; x++ {
				c := cleaned.NRGBAAt(x, y)
				if c.A == 0 {
					continue
				}
				if isChromaGreenPixel(c) {
					t.Fatalf("chroma green leaked into skin at (%d,%d): %+v", x, y, c)
				}
				uniqueColors[(uint32(c.R)<<16)|(uint32(c.G)<<8)|uint32(c.B)] = struct{}{}
			}
		}
		if len(uniqueColors) > 36 {
			t.Fatalf("expected <= 36 quantized RGB colors, got %d", len(uniqueColors))
		}
		// Verify RenderOrthoTurnaroundSheet round-trip looks valid as a turnaround
		rtBytes := RenderOrthoTurnaroundSheet(cleaned, slim)
		rtImg, err := skinpng.DecodePNG(rtBytes)
		if err != nil || !LooksLikeOrthoTurnaround(rtImg) {
			t.Fatalf("RenderOrthoTurnaroundSheet round-trip invalid (slim=%v): err=%v", slim, err)
		}
	}
}

func TestImageAPIOrthoB2GenerateAndRefineEdits(t *testing.T) {
	resetJobManagerForTest()

	var sawGenerations atomic.Bool
	var sawEditsTurnaround atomic.Bool

	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/images/generations"):
			var reqBody struct {
				Prompt string `json:"prompt"`
			}
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			if strings.Contains(reqBody.Prompt, "#00FF00") && strings.Contains(reqBody.Prompt, "turnaround") {
				sawGenerations.Store(true)
			}
			raw := makeSyntheticTurnaroundPNG(true, color.NRGBA{39, 106, 180, 255})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(raw)}},
			})
		case strings.HasSuffix(r.URL.Path, "/images/edits"):
			if err := r.ParseMultipartForm(16 << 20); err == nil {
				if fh, _, err := r.FormFile("image"); err == nil {
					defer fh.Close()
					if decoded, _, err := image.Decode(fh); err == nil {
						if LooksLikeOrthoTurnaround(skinpng.ToNRGBA(decoded)) {
							sawEditsTurnaround.Store(true)
						}
					}
				}
			}
			raw := makeSyntheticTurnaroundPNG(true, color.NRGBA{210, 64, 120, 255})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(raw)}},
			})
		default:
			w.WriteHeader(404)
		}
	}))
	defer imgSrv.Close()

	settings := map[string]any{
		"default_engine":       "image_api",
		"image_api_url":        imgSrv.URL + "/v1",
		"image_api_key":        "sk-b2",
		"image_api_mode":       "ortho_b2",
		"allow_private_worker": true,
		"daily_limit":          float64(5),
		"cooldown_seconds":     float64(0),
	}
	data := map[string]json.RawMessage{}
	user := sdk.Principal{ID: 41}

	gen1, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"prompt": "赛博少女", "engine": "image_api", "model": "slim"}),
	})
	var s1 struct{ JobID string `json:"jobId"` }
	_ = json.Unmarshal(gen1.(sdk.RuntimeResponse).Body, &s1)
	j1, _ := pollUntilTerminal(t, s1.JobID, settings, data, user)
	if j1.Status != "succeeded" || !sawGenerations.Load() {
		t.Fatalf("ortho_b2 initial generation failed: job=%+v sawGen=%v", j1, sawGenerations.Load())
	}

	resetJobManagerForTest()

	gen2, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"prompt": "把外套改成玫红色", "engine": "image_api", "model": "slim", "baseJobId": s1.JobID}),
	})
	var s2 struct{ JobID string `json:"jobId"` }
	_ = json.Unmarshal(gen2.(sdk.RuntimeResponse).Body, &s2)
	j2, _ := pollUntilTerminal(t, s2.JobID, settings, data, user)
	if j2.Status != "succeeded" || !sawEditsTurnaround.Load() {
		t.Fatalf("ortho_b2 refine /edits failed: job=%+v sawEditsTurnaround=%v", j2, sawEditsTurnaround.Load())
	}
}

func TestOrthoB2TallHeaddressAndOversizedHeadAlignment(t *testing.T) {
	sheet := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	fillRectNRGBA(sheet, 0, 0, 1024, 1024, color.NRGBA{0, 255, 0, 255})

	blueHat := color.NRGBA{40, 90, 200, 255}
	cyanEye := color.NRGBA{0, 230, 255, 255}
	peachSkin := color.NRGBA{245, 205, 180, 255}
	navyBody := color.NRGBA{24, 48, 120, 255}
	bootDark := color.NRGBA{45, 45, 55, 255}

	drawChar := func(bx int, isFront bool) {
		y0 := 80
		// Tall protruding headdress spire (0..140) above 8x8 square head (140..326) -> 38% head ratio.
		fillRectNRGBA(sheet, bx+80, y0+0, 40, 60, blueHat)
		fillRectNRGBA(sheet, bx+60, y0+60, 80, 80, blueHat)
		// Core head cube (width 110, height 186): hair crown + face with centered cyan eyes + peach cheeks
		fillRectNRGBA(sheet, bx+45, y0+140, 110, 70, blueHat)
		if isFront {
			fillRectNRGBA(sheet, bx+45, y0+210, 110, 116, peachSkin)
			// Eyes centered vertically in core square head (y0+228..264)
			fillRectNRGBA(sheet, bx+60, y0+228, 22, 36, cyanEye)
			fillRectNRGBA(sheet, bx+118, y0+228, 22, 36, cyanEye)
		} else {
			fillRectNRGBA(sheet, bx+45, y0+210, 110, 116, blueHat)
		}
		// Torso + arms (width 200, height 267, y0+326..593)
		fillRectNRGBA(sheet, bx+0, y0+326, 200, 267, navyBody)
		// Legs (width 110, height 267, y0+593..860)
		fillRectNRGBA(sheet, bx+45, y0+593, 110, 267, bootDark)
	}
	drawChar(156, true)
	drawChar(668, false)

	skin, err := ProjectOrthoTurnaroundToSkin(sheet, false, 36)
	if err != nil {
		t.Fatalf("ProjectOrthoTurnaroundToSkin failed: %v", err)
	}
	cleaned, err := SanitizeAndValidateSkin(skin, false)
	if err != nil {
		t.Fatalf("SanitizeAndValidateSkin failed: %v", err)
	}

	// Verify cyan eyes landed in head.front middle rows (ly in 2..6), NOT sliced to bottom edge or missing.
	eyeHitsMid := 0
	for ly := 2; ly <= 6; ly++ {
		for lx := 0; lx < 8; lx++ {
			c := cleaned.NRGBAAt(8+lx, 8+ly)
			if c.B > 180 && c.G > 170 && c.R < 90 {
				eyeHitsMid++
			}
		}
	}
	if eyeHitsMid < 2 {
		t.Fatalf("expected cyan eye pixels inside head.front rows 2..6, got %d hits", eyeHitsMid)
	}

	// Verify torso.front top rows (y in 20..22) have ZERO cyan eye pixels and ZERO peach cheek pixels.
	for ly := 0; ly < 3; ly++ {
		for lx := 0; lx < 8; lx++ {
			c := cleaned.NRGBAAt(20+lx, 20+ly)
			if c.B > 180 && c.G > 170 && c.R < 90 {
				t.Fatalf("eye pixel leaked onto torso.front at (20+%d,20+%d): %+v", lx, ly, c)
			}
			if c.R > 210 && c.G > 170 && c.B > 140 {
				t.Fatalf("cheek skin pixel leaked onto torso.front at (20+%d,20+%d): %+v", lx, ly, c)
			}
		}
	}
}

func TestOrthoB2WideDressWaistFallback(t *testing.T) {
	sheet := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	fillRectNRGBA(sheet, 0, 0, 1024, 1024, color.NRGBA{0, 255, 0, 255})

	hairColor := color.NRGBA{180, 120, 210, 255}
	magentaEye := color.NRGBA{230, 40, 160, 255}
	peachSkin := color.NRGBA{245, 205, 180, 255}
	dressColor := color.NRGBA{220, 90, 130, 255}
	shoeColor := color.NRGBA{50, 50, 60, 255}

	drawChar := func(bx int, isFront bool) {
		y0 := 80
		// Oversized head (33% of 860 = 284px, width 120)
		fillRectNRGBA(sheet, bx+50, y0+0, 120, 100, hairColor)
		if isFront {
			fillRectNRGBA(sheet, bx+50, y0+100, 120, 184, peachSkin)
			fillRectNRGBA(sheet, bx+66, y0+135, 24, 40, magentaEye)
			fillRectNRGBA(sheet, bx+130, y0+135, 24, 40, magentaEye)
		} else {
			fillRectNRGBA(sheet, bx+50, y0+100, 120, 184, hairColor)
		}
		// Wide uninterrupted dress across torso + upper legs (y0+284..760, width 220) -> hides waist step
		fillRectNRGBA(sheet, bx+0, y0+284, 220, 476, dressColor)
		// Shoes/legs only at bottom (y0+760..860)
		fillRectNRGBA(sheet, bx+50, y0+760, 120, 100, shoeColor)
	}
	drawChar(146, true)
	drawChar(658, false)

	skin, err := ProjectOrthoTurnaroundToSkin(sheet, true, 36)
	if err != nil {
		t.Fatalf("ProjectOrthoTurnaroundToSkin failed: %v", err)
	}
	cleaned, err := SanitizeAndValidateSkin(skin, true)
	if err != nil {
		t.Fatalf("SanitizeAndValidateSkin failed: %v", err)
	}

	eyeHits := 0
	for ly := 2; ly <= 6; ly++ {
		for lx := 0; lx < 8; lx++ {
			c := cleaned.NRGBAAt(8+lx, 8+ly)
			if c.R > 190 && c.G < 90 && c.B > 120 {
				eyeHits++
			}
		}
	}
	if eyeHits < 2 {
		t.Fatalf("expected magenta eye pixels in head.front, got %d", eyeHits)
	}
	for ly := 0; ly < 3; ly++ {
		for lx := 0; lx < 8; lx++ {
			c := cleaned.NRGBAAt(20+lx, 20+ly)
			if c.R > 210 && c.G > 170 && c.B > 140 {
				t.Fatalf("cheek skin pixel leaked into torso.front under wide dress at (20+%d,20+%d): %+v", lx, ly, c)
			}
		}
	}
}
