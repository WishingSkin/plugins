package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/WishingSkin/plugins/shared/skinpng"
)

const maxUpstreamImageBytes = 12 << 20
const maxUpstreamJSONBytes = 2 << 20

type aiGenerateInput struct {
	Prompt         string
	Style          string
	Model          string
	BaseRecipe     *SkinRecipe
	BasePixelSpec  *SkinPixelSpec
	BaseSkin       *image.NRGBA
	ReferenceImage []byte
}

type aiGenerateOutput struct {
	Image     *image.NRGBA
	Recipe    *SkinRecipe
	PixelSpec *SkinPixelSpec
	Summary   string
}

// newSafeAIClient builds an HTTP client enforcing HTTPS (or loopback HTTP; private LAN IPs allowed only when allowPrivate is true)
// and strictly blocking cloud metadata / link-local / multicast addresses and redirects.
func newSafeAIClient(rawURL string, allowPrivate bool, timeout time.Duration) (*http.Client, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || len(rawURL) > 2048 || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("接口 URL 配置无效")
	}
	host := strings.ToLower(u.Hostname())
	isLoopbackHost := host == "localhost" || host == "127.0.0.1" || host == "::1"
	ipLiteral := net.ParseIP(host)
	isPrivateLiteral := ipLiteral != nil && (ipLiteral.IsPrivate() || ipLiteral.IsLoopback())

	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost && !(allowPrivate && isPrivateLiteral) {
			return nil, errors.New("仅允许 HTTPS 接口，或启用内网节点选项后使用局域网/回环 HTTP")
		}
	default:
		return nil, errors.New("不支持的接口协议，请使用 https 或允许的 http")
	}

	if ipLiteral != nil && isForbiddenIP(ipLiteral) {
		return nil, errors.New("禁止访问链路本地或元数据保留 IP 地址")
	}

	dialer := &net.Dialer{Timeout: min(15*time.Second, timeout)}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			h, p, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, h)
			if err != nil || len(ips) == 0 {
				return nil, errors.New("无法解析接口域名")
			}
			for _, ipAddr := range ips {
				ip := ipAddr.IP
				if isForbiddenIP(ip) {
					return nil, errors.New("目标地址解析到受限元数据或保留网段")
				}
				if isLoopbackHost {
					if !ip.IsLoopback() {
						return nil, errors.New("回环域名必须解析到本机回环地址")
					}
				} else if ip.IsLoopback() || ip.IsPrivate() {
					if !allowPrivate {
						return nil, errors.New("默认禁止访问内网 IP，如需对接局域网推理节点请在后台启用允许内网")
					}
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), p))
		},
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func isForbiddenIP(ip net.IP) bool {
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

func normalizeEndpointURL(rawURL, defaultSuffix, detectSuffix string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if trimmed == "" {
		return ""
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}
	p := strings.TrimRight(u.Path, "/")
	if p == "" {
		u.Path = defaultSuffix
		return u.String()
	}
	if !strings.HasSuffix(p, detectSuffix) && (strings.HasSuffix(p, "/v1") || strings.HasSuffix(p, "/openai")) {
		u.Path = p + strings.TrimPrefix(defaultSuffix, "/v1")
		return u.String()
	}
	return trimmed
}

func generateStructuredLLM(ctx context.Context, cfg aiConfig, in aiGenerateInput) (aiGenerateOutput, error) {
	endpoint := normalizeEndpointURL(cfg.LLMApiURL, "/v1/chat/completions", "/chat/completions")
	client, err := newSafeAIClient(endpoint, cfg.AllowPrivateWorker, cfg.Timeout)
	if err != nil {
		return aiGenerateOutput{}, err
	}
	modelName := cfg.LLMModel
	if modelName == "" {
		modelName = "gpt-4o-mini"
	}

	if cfg.LLMRenderMode == "recipe" || (in.BaseRecipe != nil && in.BasePixelSpec == nil) {
		return generateStructuredLLMRecipe(ctx, cfg, client, endpoint, modelName, in)
	}
	return generateStructuredLLMPixelGrid(ctx, cfg, client, endpoint, modelName, in)
}

func callStructuredChat(ctx context.Context, cfg aiConfig, client *http.Client, endpoint, modelName string, messages []map[string]any, hasRef bool) (string, error) {
	doReq := func(useJSONFormat bool) (int, []byte, error) {
		payload := map[string]any{
			"model":       modelName,
			"messages":    messages,
			"temperature": 0.55,
		}
		if useJSONFormat {
			payload["response_format"] = map[string]string{"type": "json_object"}
		}
		rawBody, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(rawBody))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if cfg.LLMApiKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.LLMApiKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return 0, nil, errors.New("文本模型请求超时，可在插件设置调高 timeout_seconds 后重试（未扣费）")
			}
			return 0, nil, fmt.Errorf("文本模型请求失败: %w", err)
		}
		defer resp.Body.Close()
		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamJSONBytes))
		if readErr != nil {
			return resp.StatusCode, nil, readErr
		}
		return resp.StatusCode, bodyBytes, nil
	}

	status, bodyBytes, err := doReq(true)
	if err != nil {
		return "", err
	}
	if status == http.StatusBadRequest && strings.Contains(strings.ToLower(string(bodyBytes)), "response_format") {
		status, bodyBytes, err = doReq(false)
		if err != nil {
			return "", err
		}
	}
	if hasRef && status == http.StatusBadRequest {
		low := strings.ToLower(string(bodyBytes))
		if strings.Contains(low, "image") || strings.Contains(low, "vision") || strings.Contains(low, "multimodal") || strings.Contains(low, "content") {
			return "", errors.New("当前配置的文本模型不支持图片输入，请在后台切换为支持视觉的多模态模型（未扣费）")
		}
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("文本模型返回 HTTP %d（未扣费）", status)
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil || len(chatResp.Choices) == 0 {
		return "", errors.New("文本模型响应格式无效（未扣费）")
	}
	return chatResp.Choices[0].Message.Content, nil
}

func buildLLMUserContent(userText string, refImage []byte) any {
	if len(refImage) == 0 {
		return userText
	}
	mime := http.DetectContentType(refImage)
	if mime != "image/jpeg" {
		mime = "image/png"
	}
	dataURI := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(refImage)
	return []map[string]any{
		{"type": "text", "text": userText},
		{"type": "image_url", "image_url": map[string]string{"url": dataURI}},
	}
}

func generateStructuredLLMRecipe(ctx context.Context, cfg aiConfig, client *http.Client, endpoint, modelName string, in aiGenerateInput) (aiGenerateOutput, error) {
	systemPrompt := `You are a Minecraft skin character designer. Output ONLY a single valid JSON object (no markdown commentary outside JSON) with keys:
"summary" (short Chinese description <= 40 chars),
"model" ("default" or "slim"),
"skinTone" ("#RRGGBB"),
"hairColor" ("#RRGGBB"),
"hairAccent" ("#RRGGBB"),
"hairStyle" (one of: "short","spiky","long","bob","ponytail","hood","helmet"),
"eyeColor" ("#RRGGBB"),
"eyeStyle" (one of: "classic","anime","cute","cool","visor"),
"outfitStyle" (one of: "hoodie","jacket","tshirt","armor","coat","dress","suit","kimono"),
"topPrimary" ("#RRGGBB"),
"topSecondary" ("#RRGGBB"),
"topAccent" ("#RRGGBB"),
"bottomStyle" (one of: "pants","shorts","skirt","armor_greaves"),
"sleeveStyle" (one of: "long","short","rolled","sleeveless"),
"bottomPrimary" ("#RRGGBB"),
"bottomAccent" ("#RRGGBB"),
"shoeColor" ("#RRGGBB"),
"accessory" (one of: "none","glasses","headphones","crown","cat_ears","scarf","beanie","goggles"),
"accessoryColor" ("#RRGGBB"),
"hairLayers" (integer 1..3),
"hairSheen" (number 0.2..0.9),
"blushIntensity" (number 0..0.75),
"detailAccent" (boolean),
"shading" (number between 0.10 and 0.30).`

	userParts := []string{
		"Character prompt: " + in.Prompt,
		"Requested arm model: " + in.Model,
	}
	if in.Style != "" {
		userParts = append(userParts, "Art style hint: "+in.Style)
	}
	if len(in.ReferenceImage) > 0 {
		userParts = append(userParts, "Analyze the attached reference character image (hair colors/style, eyes, outfit palette/silhouettes, accessories) while blending the text prompt accurately into the SkinRecipe JSON.")
	}
	if in.BaseRecipe != nil {
		encodedBase, _ := json.Marshal(in.BaseRecipe)
		userParts = append(userParts, "Refine the existing recipe (keep attributes not changed by the user's new instruction): "+string(encodedBase))
	}

	messages := []map[string]any{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": buildLLMUserContent(strings.Join(userParts, "\n"), in.ReferenceImage)},
	}
	rawContent, err := callStructuredChat(ctx, cfg, client, endpoint, modelName, messages, len(in.ReferenceImage) > 0)
	if err != nil {
		return aiGenerateOutput{}, err
	}
	jsonText := extractJSONObject(rawContent)
	if jsonText == "" {
		return aiGenerateOutput{}, errors.New("文本模型未返回有效 JSON 配方（未扣费）")
	}

	recipe := SkinRecipe{}
	if in.BaseRecipe != nil {
		recipe = *in.BaseRecipe
	}
	if err := json.Unmarshal([]byte(jsonText), &recipe); err != nil {
		return aiGenerateOutput{}, errors.New("无法解析 AI 返回的皮肤配方 JSON（未扣费）")
	}
	norm := NormalizeRecipe(recipe, in.Model)
	img := RenderRecipe(norm)
	return aiGenerateOutput{
		Image:   img,
		Recipe:  &norm,
		Summary: norm.Summary,
	}, nil
}

func generateStructuredLLMPixelGrid(ctx context.Context, cfg aiConfig, client *http.Client, endpoint, modelName string, in aiGenerateInput) (aiGenerateOutput, error) {
	armW := 4
	if in.Model == "slim" {
		armW = 3
	}

	systemPrompt := fmt.Sprintf(`You are an expert Minecraft pixel-art skin designer. Output ONLY a single valid JSON object (no markdown fences) adhering strictly to the D2-Strict Per-Face Pixel Grid schema.
Top-level keys:
- "summary": short Chinese description (<= 40 chars)
- "model": %q
- "baseSkinColor": "#RRGGBB" base skin color for opaque fills
- "mirrorLeftArmFromRight": boolean (true mirrors right_arm/right_sleeve into left_arm/left_sleeve)
- "mirrorLeftLegFromRight": boolean (true mirrors right_leg/right_pant into left_leg/left_pant)
- "palette": map of 18-32 single-character symbols (excluding "." and " ") -> "#RRGGBB". Use 4-6 step hue-shifted pixel-art ramps (cooler, darker shadows; warmer, brighter highlights) for hair, skin, clothes, shoes, eyes, and accents.
- "faces": map of part -> face -> array of strings (exactly W chars per string, H strings total).
Exact required dimensions (W x H) per face:
- "head" (and optional "hat"): top 8x8, bottom 8x8, right 8x8, front 8x8, left 8x8, back 8x8
- "torso" (and optional "jacket"): front 8x12, back 8x12, left 4x12, right 4x12, top 8x4, bottom 8x4
- "right_arm" / "left_arm" (and optional "right_sleeve" / "left_sleeve"): front %dx12, back %dx12, left 4x12, right 4x12, top %dx4, bottom %dx4
- "right_leg" / "left_leg" (and optional "right_pant" / "left_pant"): front 4x12, back 4x12, left 4x12, right 4x12, top 4x4, bottom 4x4
Completeness rules:
1. Every non-mirrored Base part ("head","torso","right_arm","right_leg", plus "left_arm"/"left_leg" if mirror flag is false) MUST include ALL 6 faces ("top","bottom","right","front","left","back") so limbs are never left bare from behind or the side.
2. When you want asymmetric details (like a single glove or strap), keep mirror flag true AND provide ONLY the overridden faces in "left_arm"/"left_sleeve"/"left_leg"/"left_pant".
3. Use "." for transparent pixels in Overlay layers ("hat","jacket","right_sleeve","left_sleeve","right_pant","left_pant"). Always design rich "hat" & "jacket" accents (goggles, hair bangs, headphones, jacket lapels, hood, cape emblem) for crisp 3D depth.
4. Shade faces cleanly (darker inner/bottom folds, brighter shoulders/hair crown, sclera "N"+iris "E" eyes on row 4 of head.front).
Concise 8x8 hue-shifted head.front example:
["OPHHHHPI","PHHJHHGI","OGHHHGPO","MHKKKKHM","MNEKKENM","MKKKKKKM","LKKUKKKL","MLKKKKLM"]`, in.Model, armW, armW, armW, armW)

	userParts := []string{
		"Character prompt: " + in.Prompt,
		fmt.Sprintf("Target skin model: %s (arm front/back/top/bottom width = %d px)", in.Model, armW),
	}
	if in.Style != "" {
		userParts = append(userParts, "Art style hint: "+in.Style)
	}
	if len(in.ReferenceImage) > 0 {
		userParts = append(userParts, "Analyze the attached reference image carefully (hair color/style, eye color, jacket/outfit details, footwear, hat accessories) and transcribe them crisply onto all 6 faces of the Minecraft pixel grid.")
	}
	if in.BasePixelSpec != nil {
		baseJSON, _ := json.Marshal(in.BasePixelSpec)
		userParts = append(userParts, "Refine the existing pixel skin spec below. You may return a partial JSON patch containing ONLY updated palette keys and modified faces (unchanged parts will be preserved), OR a full spec:\n"+string(baseJSON))
	}

	messages := []map[string]any{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": buildLLMUserContent(strings.Join(userParts, "\n"), in.ReferenceImage)},
	}
	rawContent, err := callStructuredChat(ctx, cfg, client, endpoint, modelName, messages, len(in.ReferenceImage) > 0)
	if err != nil {
		return aiGenerateOutput{}, err
	}

	parseAndRender := func(rawReply string) (*SkinPixelSpec, *SkinRecipe, *image.NRGBA, []string, error) {
		jsonText := extractJSONObject(rawReply)
		if jsonText == "" {
			return nil, nil, nil, []string{"response did not contain a valid JSON object"}, errors.New("文本模型未返回有效 JSON（未扣费）")
		}
		var probe struct {
			Faces      map[string]any `json:"faces"`
			SkinTone   string         `json:"skinTone"`
			HairColor  string         `json:"hairColor"`
			TopPrimary string         `json:"topPrimary"`
		}
		_ = json.Unmarshal([]byte(jsonText), &probe)
		if len(probe.Faces) == 0 && (probe.SkinTone != "" || probe.HairColor != "" || probe.TopPrimary != "") && in.BasePixelSpec == nil {
			var legacy SkinRecipe
			if uErr := json.Unmarshal([]byte(jsonText), &legacy); uErr == nil {
				norm := NormalizeRecipe(legacy, in.Model)
				return nil, &norm, RenderRecipe(norm), nil, nil
			}
		}

		var parsed SkinPixelSpec
		if uErr := json.Unmarshal([]byte(jsonText), &parsed); uErr != nil {
			return nil, nil, nil, []string{"invalid JSON structure: " + uErr.Error()}, errors.New("无法解析 AI 返回的像素网格 JSON（未扣费）")
		}
		candidate := &parsed
		if in.BasePixelSpec != nil {
			candidate = MergePixelSpec(in.BasePixelSpec, &parsed)
		}
		norm := NormalizePixelSpec(candidate, in.Model)
		img, valErrs, valErr := ValidateAndRenderPixelSpec(norm, in.Model == "slim")
		return norm, nil, img, valErrs, valErr
	}

	norm, legacyRecipe, img, valErrs, valErr := parseAndRender(rawContent)
	if valErr != nil && len(valErrs) > 0 {
		// One automatic repair turn when schema/dimensions or completeness checks fail.
		repairMsg := fmt.Sprintf("Your JSON failed D2-Strict validation with these exact errors:\n- %s\nFix EVERY listed issue (fill any missing base faces and match exact WxH dimensions for %s model armW=%d, keeping all palette symbols defined) and output ONLY the corrected valid JSON object.", strings.Join(valErrs, "\n- "), in.Model, armW)
		repairMessages := append(messages,
			map[string]any{"role": "assistant", "content": rawContent},
			map[string]any{"role": "user", "content": repairMsg},
		)
		if repairedReply, rErr := callStructuredChat(ctx, cfg, client, endpoint, modelName, repairMessages, false); rErr == nil {
			norm2, leg2, img2, _, valErr2 := parseAndRender(repairedReply)
			if valErr2 == nil && img2 != nil {
				norm, legacyRecipe, img, valErr = norm2, leg2, img2, nil
			} else if valErr2 != nil {
				valErr = valErr2
			}
		}
	}

	if valErr != nil || img == nil {
		if valErr != nil {
			return aiGenerateOutput{}, fmt.Errorf("像素网格校验未通过: %s（未扣费）", truncateRunes(valErr.Error(), 90))
		}
		return aiGenerateOutput{}, errors.New("未能渲染有效的 64×64 像素皮肤（未扣费）")
	}

	if legacyRecipe != nil {
		return aiGenerateOutput{
			Image:   img,
			Recipe:  legacyRecipe,
			Summary: legacyRecipe.Summary,
		}, nil
	}
	if norm.Summary == "" {
		norm.Summary = "AI 像素精绘：" + truncateRunes(in.Prompt, 32)
	}
	return aiGenerateOutput{
		Image:     img,
		PixelSpec: norm,
		Summary:   norm.Summary,
	}, nil
}

func generateImageAPI(ctx context.Context, cfg aiConfig, in aiGenerateInput) (aiGenerateOutput, error) {
	endpoint := normalizeEndpointURL(cfg.ImageApiURL, "/v1/images/generations", "/images/generations")
	useEdits := strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/edits") || in.BaseSkin != nil || len(in.ReferenceImage) > 0
	if useEdits && !strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/edits") {
		if strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/images/generations") {
			endpoint = strings.TrimSuffix(strings.TrimRight(endpoint, "/"), "/generations") + "/edits"
		}
	}
	client, err := newSafeAIClient(endpoint, cfg.AllowPrivateWorker, cfg.Timeout)
	if err != nil {
		return aiGenerateOutput{}, err
	}
	modelName := cfg.ImageModel
	if modelName == "" {
		modelName = "gpt-image-1"
	}

	isDirectUV := cfg.ImageAPIMode == "direct_uv"
	armSpec := "classic 4px arms"
	if in.Model == "slim" {
		armSpec = "slim 3px arms"
	}

	var fullPrompt string
	if isDirectUV {
		fullPrompt = fmt.Sprintf("64x64 pixel-art Minecraft skin texture UV unwrap sheet, flat front-projected UV texture atlas (head at top-left 8x8, torso below, arms and legs on standard Minecraft skin 1:1 grid), crisp unaliased pixels, %s skin model, character: %s", in.Model, in.Prompt)
		if in.BaseSkin != nil {
			fullPrompt = fmt.Sprintf("Refine the attached existing Minecraft skin UV texture sheet while strictly keeping every UV region aligned in exact 64x64 grid proportions (%s model). Requested changes: %s", in.Model, in.Prompt)
		} else if len(in.ReferenceImage) > 0 {
			fullPrompt = fmt.Sprintf("Convert the attached character reference image into a standard flat 64x64 Minecraft skin UV texture atlas (%s model), crisp pixel art matching UV layout: %s", in.Model, in.Prompt)
		}
	} else {
		fullPrompt = fmt.Sprintf("Crisp flat orthographic 2D pixel-art Minecraft character turnaround sheet on pure solid bright green #00FF00 background. Left half: full-body strictly front view; right half: full-body strictly back view. Exact blocky Minecraft proportions (head:torso:legs = 8:12:12), exact blocky 8x8 square cube head sitting directly on shoulders, keep hats/ears/ribbons flat inside the 8x8 head cube without tall protruding horns or tall headdresses above the head, %s hanging straight vertically down at sides, hard pixelated nearest-neighbor edges, flat even lighting, no 3D perspective, no shadows, no floor, character: %s", armSpec, in.Prompt)
		if in.BaseSkin != nil {
			fullPrompt = fmt.Sprintf("Edit the attached Minecraft front+back orthographic turnaround sheet while strictly keeping blocky 8:12:12 front-left and back-right silhouettes, exact 8x8 square cube head without tall protruding hats/ears/horns above the head, %s, and pure solid #00FF00 background intact. Requested edits: %s", armSpec, in.Prompt)
		} else if len(in.ReferenceImage) > 0 {
			fullPrompt = fmt.Sprintf("Convert the attached reference character into a flat orthographic 2D pixel-art Minecraft turnaround sheet (left half front view, right half back view) on pure solid #00FF00 background, exact blocky 8:12:12 proportions with flat 8x8 square head sitting directly on shoulders (no tall protruding headdress/horns above head), %s, crisp unaliased pixels: %s", armSpec, in.Prompt)
		}
	}
	if in.Style != "" {
		fullPrompt += ", style: " + in.Style
	}

	var req *http.Request
	if strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/edits") {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("model", modelName)
		_ = mw.WriteField("prompt", fullPrompt)
		_ = mw.WriteField("n", "1")
		_ = mw.WriteField("size", "1024x1024")
		imgPayload := BuildUVGuidePNG(in.Model == "slim")
		imgName := "uv-guide.png"
		if in.BaseSkin != nil {
			if isDirectUV {
				imgPayload = UpscaleSkinForEditPNG(in.BaseSkin)
				imgName = "base-skin.png"
			} else {
				imgPayload = RenderOrthoTurnaroundSheet(in.BaseSkin, in.Model == "slim")
				imgName = "base-turnaround.png"
			}
		} else if len(in.ReferenceImage) > 0 {
			imgPayload = in.ReferenceImage
			imgName = "reference.png"
			if http.DetectContentType(in.ReferenceImage) == "image/jpeg" {
				imgName = "reference.jpg"
			}
		}
		part, err := mw.CreateFormFile("image", imgName)
		if err != nil {
			return aiGenerateOutput{}, err
		}
		_, _ = part.Write(imgPayload)
		_ = mw.Close()

		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
		if err != nil {
			return aiGenerateOutput{}, err
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
	} else {
		payload := map[string]any{
			"model":  modelName,
			"prompt": fullPrompt,
			"n":      1,
			"size":   "1024x1024",
		}
		rawBody, _ := json.Marshal(payload)
		var err error
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(rawBody))
		if err != nil {
			return aiGenerateOutput{}, err
		}
		req.Header.Set("Content-Type", "application/json")
	}
	if cfg.ImageApiKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.ImageApiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return aiGenerateOutput{}, errors.New("图像接口生成超时（可在插件设置调高 timeout_seconds 或改用文本精绘，未扣费）")
		}
		return aiGenerateOutput{}, fmt.Errorf("图像接口请求失败: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamImageBytes))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return aiGenerateOutput{}, errors.New("读取图像响应超时（可在插件设置调高 timeout_seconds，未扣费）")
		}
		return aiGenerateOutput{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return aiGenerateOutput{}, fmt.Errorf("图像接口返回 HTTP %d（未扣费）", resp.StatusCode)
	}

	var out struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(bodyBytes, &out); err != nil || len(out.Data) == 0 {
		return aiGenerateOutput{}, errors.New("图像接口返回格式无效（未扣费）")
	}

	var imgBytes []byte
	if b64 := strings.TrimSpace(out.Data[0].B64JSON); b64 != "" {
		imgBytes, err = base64.StdEncoding.DecodeString(stripDataURLPrefix(b64))
		if err != nil {
			return aiGenerateOutput{}, errors.New("无法解码图像 Base64（未扣费）")
		}
	} else if imgURL := strings.TrimSpace(out.Data[0].URL); imgURL != "" {
		imgClient, err := newSafeAIClient(imgURL, cfg.AllowPrivateWorker, cfg.Timeout)
		if err != nil {
			return aiGenerateOutput{}, err
		}
		imgReq, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
		if err != nil {
			return aiGenerateOutput{}, err
		}
		imgResp, err := imgClient.Do(imgReq)
		if err != nil {
			return aiGenerateOutput{}, fmt.Errorf("下载生成图像失败: %w", err)
		}
		defer imgResp.Body.Close()
		if imgResp.StatusCode < 200 || imgResp.StatusCode >= 300 {
			return aiGenerateOutput{}, fmt.Errorf("下载生成图像返回 HTTP %d（未扣费）", imgResp.StatusCode)
		}
		imgBytes, err = io.ReadAll(io.LimitReader(imgResp.Body, maxUpstreamImageBytes))
		if err != nil {
			return aiGenerateOutput{}, err
		}
	} else {
		return aiGenerateOutput{}, errors.New("图像接口未返回图片数据（未扣费）")
	}

	decoded, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return aiGenerateOutput{}, errors.New("无法解码上游返回的图片格式（未扣费）")
	}
	nrgba := skinpng.ToNRGBA(decoded)
	if !isDirectUV && LooksLikeOrthoTurnaround(nrgba) {
		projected, projErr := ProjectOrthoTurnaroundToSkin(nrgba, in.Model == "slim", 36)
		if projErr != nil {
			return aiGenerateOutput{}, projErr
		}
		return aiGenerateOutput{
			Image:   projected,
			Summary: "正交投影生成：" + truncateRunes(in.Prompt, 32),
		}, nil
	}
	return aiGenerateOutput{
		Image:   nrgba,
		Summary: "图像模型生成：" + truncateRunes(in.Prompt, 36),
	}, nil
}

func generateAsyncWorker(ctx context.Context, cfg aiConfig, in aiGenerateInput) (aiGenerateOutput, error) {
	client, err := newSafeAIClient(cfg.WorkerURL, cfg.AllowPrivateWorker, cfg.Timeout)
	if err != nil {
		return aiGenerateOutput{}, err
	}
	payload := map[string]any{
		"prompt": in.Prompt,
		"model":  in.Model,
		"style":  in.Style,
		"width":  64,
		"height": 64,
	}
	if len(in.ReferenceImage) > 0 {
		payload["referenceImageBase64"] = base64.StdEncoding.EncodeToString(in.ReferenceImage)
	}
	if in.BaseSkin != nil {
		if b := UpscaleSkinForEditPNG(in.BaseSkin); len(b) > 0 {
			payload["baseSkinBase64"] = base64.StdEncoding.EncodeToString(b)
		}
	}
	rawBody, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.WorkerURL, bytes.NewReader(rawBody))
	if err != nil {
		return aiGenerateOutput{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.WorkerSecret != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.WorkerSecret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return aiGenerateOutput{}, fmt.Errorf("外部推理节点连接失败: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamImageBytes))
	if err != nil {
		return aiGenerateOutput{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return aiGenerateOutput{}, fmt.Errorf("外部推理节点返回 HTTP %d（未扣费）", resp.StatusCode)
	}

	finishWorkerImage := func(img image.Image, layout string) (aiGenerateOutput, error) {
		nrgba := skinpng.ToNRGBA(img)
		if strings.EqualFold(strings.TrimSpace(layout), "ortho_b2") {
			projected, projErr := ProjectOrthoTurnaroundToSkin(nrgba, in.Model == "slim", 36)
			if projErr != nil {
				return aiGenerateOutput{}, projErr
			}
			return aiGenerateOutput{
				Image:   projected,
				Summary: "外部节点正交生成：" + truncateRunes(in.Prompt, 32),
			}, nil
		}
		return aiGenerateOutput{
			Image:   nrgba,
			Summary: "外部节点生成：" + truncateRunes(in.Prompt, 36),
		}, nil
	}

	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "image/") {
		decoded, _, err := image.Decode(bytes.NewReader(bodyBytes))
		if err != nil {
			return aiGenerateOutput{}, errors.New("无法解析推理节点返回的图像（未扣费）")
		}
		return finishWorkerImage(decoded, "")
	}

	type workerPayload struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		PollURL   string `json:"pollUrl"`
		PNGBase64 string `json:"pngBase64"`
		ImageURL  string `json:"imageUrl"`
		Layout    string `json:"layout"`
		Error     string `json:"error"`
	}
	var wp workerPayload
	if err := json.Unmarshal(bodyBytes, &wp); err != nil {
		return aiGenerateOutput{}, errors.New("外部推理节点返回格式无效（未扣费）")
	}

	var activePollURL string
	for {
		status := strings.ToLower(strings.TrimSpace(wp.Status))
		if wp.Error != "" || status == "failed" || status == "error" || status == "canceled" {
			msg := strings.TrimSpace(wp.Error)
			if msg == "" {
				msg = "外部推理节点任务失败"
			}
			return aiGenerateOutput{}, errors.New(msg + "（未扣费）")
		}
		if b64 := strings.TrimSpace(wp.PNGBase64); b64 != "" {
			imgBytes, err := base64.StdEncoding.DecodeString(stripDataURLPrefix(b64))
			if err != nil {
				return aiGenerateOutput{}, errors.New("推理节点 pngBase64 无效（未扣费）")
			}
			decoded, _, err := image.Decode(bytes.NewReader(imgBytes))
			if err != nil {
				return aiGenerateOutput{}, errors.New("无法解码推理节点返回图像（未扣费）")
			}
			return finishWorkerImage(decoded, wp.Layout)
		}
		if imgURL := strings.TrimSpace(wp.ImageURL); imgURL != "" {
			imgClient, err := newSafeAIClient(imgURL, cfg.AllowPrivateWorker, cfg.Timeout)
			if err != nil {
				return aiGenerateOutput{}, err
			}
			imgReq, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
			if err != nil {
				return aiGenerateOutput{}, err
			}
			if cfg.WorkerSecret != "" && sameOrigin(imgURL, cfg.WorkerURL) {
				imgReq.Header.Set("Authorization", "Bearer "+cfg.WorkerSecret)
			}
			imgResp, err := imgClient.Do(imgReq)
			if err != nil {
				return aiGenerateOutput{}, err
			}
			imgBytes, readErr := io.ReadAll(io.LimitReader(imgResp.Body, maxUpstreamImageBytes))
			imgResp.Body.Close()
			if readErr != nil || imgResp.StatusCode < 200 || imgResp.StatusCode >= 300 {
				return aiGenerateOutput{}, errors.New("无法获取推理节点输出图片（未扣费）")
			}
			decoded, _, err := image.Decode(bytes.NewReader(imgBytes))
			if err != nil {
				return aiGenerateOutput{}, errors.New("无法解码推理节点图片（未扣费）")
			}
			return finishWorkerImage(decoded, wp.Layout)
		}

		if status != "queued" && status != "running" && status != "processing" && status != "starting" {
			return aiGenerateOutput{}, errors.New("外部推理节点未返回有效图片或轮询状态（未扣费）")
		}
		if p := strings.TrimSpace(wp.PollURL); p != "" {
			activePollURL = p
		} else if activePollURL == "" && wp.ID != "" {
			activePollURL = strings.TrimRight(cfg.WorkerURL, "/") + "/" + url.PathEscape(wp.ID)
		}
		pollTarget := activePollURL
		if pollTarget == "" {
			return aiGenerateOutput{}, errors.New("外部异步节点未提供 pollUrl 或任务 ID（未扣费）")
		}
		pollClient, err := newSafeAIClient(pollTarget, cfg.AllowPrivateWorker, cfg.Timeout)
		if err != nil {
			return aiGenerateOutput{}, err
		}

		select {
		case <-ctx.Done():
			return aiGenerateOutput{}, errors.New("等待外部推理节点超时（未扣费）")
		case <-time.After(150 * time.Millisecond):
		}

		pReq, err := http.NewRequestWithContext(ctx, http.MethodGet, pollTarget, nil)
		if err != nil {
			return aiGenerateOutput{}, err
		}
		if cfg.WorkerSecret != "" && sameOrigin(pollTarget, cfg.WorkerURL) {
			pReq.Header.Set("Authorization", "Bearer "+cfg.WorkerSecret)
		}
		pResp, err := pollClient.Do(pReq)
		if err != nil {
			return aiGenerateOutput{}, fmt.Errorf("轮询外部节点失败: %w", err)
		}
		pBytes, readErr := io.ReadAll(io.LimitReader(pResp.Body, maxUpstreamImageBytes))
		pResp.Body.Close()
		if readErr != nil || pResp.StatusCode < 200 || pResp.StatusCode >= 300 {
			return aiGenerateOutput{}, fmt.Errorf("轮询外部节点异常 HTTP %d（未扣费）", pResp.StatusCode)
		}
		if strings.HasPrefix(strings.ToLower(pResp.Header.Get("Content-Type")), "image/") {
			decoded, _, err := image.Decode(bytes.NewReader(pBytes))
			if err != nil {
				return aiGenerateOutput{}, errors.New("无法解析轮询返回的图像（未扣费）")
			}
			return finishWorkerImage(decoded, wp.Layout)
		}
		wp = workerPayload{}
		if err := json.Unmarshal(pBytes, &wp); err != nil {
			return aiGenerateOutput{}, errors.New("外部节点轮询响应 JSON 无效（未扣费）")
		}
	}
}

func extractJSONObject(raw string) string {
	s := strings.TrimSpace(raw)
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

func stripDataURLPrefix(s string) string {
	if idx := strings.Index(s, ","); idx >= 0 && strings.Contains(s[:idx], "base64") {
		return s[idx+1:]
	}
	return s
}

func sameOrigin(aRaw, bRaw string) bool {
	a, err1 := url.Parse(aRaw)
	b, err2 := url.Parse(bRaw)
	return err1 == nil && err2 == nil && a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host)
}
