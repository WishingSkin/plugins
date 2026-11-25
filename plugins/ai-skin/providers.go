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
	Prompt     string
	Style      string
	Model      string
	BaseRecipe *SkinRecipe
}

type aiGenerateOutput struct {
	Image   *image.NRGBA
	Recipe  *SkinRecipe
	Summary string
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
"bottomPrimary" ("#RRGGBB"),
"bottomAccent" ("#RRGGBB"),
"shoeColor" ("#RRGGBB"),
"accessory" (one of: "none","glasses","headphones","crown","cat_ears","scarf","beanie","goggles"),
"accessoryColor" ("#RRGGBB"),
"shading" (number between 0.08 and 0.28).`

	userParts := []string{
		"Character prompt: " + in.Prompt,
		"Requested arm model: " + in.Model,
	}
	if in.Style != "" {
		userParts = append(userParts, "Art style hint: "+in.Style)
	}
	if in.BaseRecipe != nil {
		encodedBase, _ := json.Marshal(in.BaseRecipe)
		userParts = append(userParts, "Refine the existing recipe (keep attributes not changed by the user's new instruction): "+string(encodedBase))
	}

	callChat := func(useJSONFormat bool) (int, []byte, error) {
		payload := map[string]any{
			"model": modelName,
			"messages": []map[string]string{
				{"role": "system", "content": systemPrompt},
				{"role": "user", "content": strings.Join(userParts, "\n")},
			},
			"temperature": 0.65,
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
			return 0, nil, fmt.Errorf("文本模型请求失败: %w", err)
		}
		defer resp.Body.Close()
		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamJSONBytes))
		if readErr != nil {
			return resp.StatusCode, nil, readErr
		}
		return resp.StatusCode, bodyBytes, nil
	}

	status, bodyBytes, err := callChat(true)
	if err != nil {
		return aiGenerateOutput{}, err
	}
	if status == http.StatusBadRequest && strings.Contains(strings.ToLower(string(bodyBytes)), "response_format") {
		status, bodyBytes, err = callChat(false)
		if err != nil {
			return aiGenerateOutput{}, err
		}
	}
	if status < 200 || status >= 300 {
		return aiGenerateOutput{}, fmt.Errorf("文本模型返回 HTTP %d（未扣费）", status)
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil || len(chatResp.Choices) == 0 {
		return aiGenerateOutput{}, errors.New("文本模型响应格式无效（未扣费）")
	}
	jsonText := extractJSONObject(chatResp.Choices[0].Message.Content)
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

func generateImageAPI(ctx context.Context, cfg aiConfig, in aiGenerateInput) (aiGenerateOutput, error) {
	endpoint := normalizeEndpointURL(cfg.ImageApiURL, "/v1/images/generations", "/images/generations")
	client, err := newSafeAIClient(endpoint, cfg.AllowPrivateWorker, cfg.Timeout)
	if err != nil {
		return aiGenerateOutput{}, err
	}
	modelName := cfg.ImageModel
	if modelName == "" {
		modelName = "gpt-image-1"
	}

	fullPrompt := fmt.Sprintf("64x64 pixel-art Minecraft skin texture UV unwrap sheet, flat front-projected UV texture atlas (head at top-left 8x8, torso below, arms and legs on standard Minecraft skin 1:1 grid), crisp unaliased pixels, %s skin model, character: %s", in.Model, in.Prompt)
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
		part, err := mw.CreateFormFile("image", "uv-guide.png")
		if err != nil {
			return aiGenerateOutput{}, err
		}
		_, _ = part.Write(BuildUVGuidePNG(in.Model == "slim"))
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
		return aiGenerateOutput{}, fmt.Errorf("图像接口请求失败: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamImageBytes))
	if err != nil {
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
	return aiGenerateOutput{
		Image:   skinpng.ToNRGBA(decoded),
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

	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "image/") {
		decoded, _, err := image.Decode(bytes.NewReader(bodyBytes))
		if err != nil {
			return aiGenerateOutput{}, errors.New("无法解析推理节点返回的图像（未扣费）")
		}
		return aiGenerateOutput{
			Image:   skinpng.ToNRGBA(decoded),
			Summary: "外部节点生成：" + truncateRunes(in.Prompt, 36),
		}, nil
	}

	type workerPayload struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		PollURL   string `json:"pollUrl"`
		PNGBase64 string `json:"pngBase64"`
		ImageURL  string `json:"imageUrl"`
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
			return aiGenerateOutput{
				Image:   skinpng.ToNRGBA(decoded),
				Summary: "外部节点生成：" + truncateRunes(in.Prompt, 36),
			}, nil
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
			return aiGenerateOutput{
				Image:   skinpng.ToNRGBA(decoded),
				Summary: "外部节点生成：" + truncateRunes(in.Prompt, 36),
			}, nil
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
		if cfg.WorkerSecret != "" {
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
			return aiGenerateOutput{
				Image:   skinpng.ToNRGBA(decoded),
				Summary: "外部节点生成：" + truncateRunes(in.Prompt, 36),
			}, nil
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
