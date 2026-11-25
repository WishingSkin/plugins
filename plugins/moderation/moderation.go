package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

const (
	maxAuditEntries    = 50
	maxRegexRules      = 32
	maxRegexRuneLength = 256
	maxWebhookPayload  = 64 << 10
	webhookTimeout     = 3500 * time.Millisecond
)

type moderationConfig struct {
	Mode              string
	BlockedWords      []string
	ReviewWords       []string
	RegexRules        []*regexp.Regexp
	RegexWarnings     []string
	RejectBlankSkin   bool
	RejectSolidSkin   bool
	WebhookURL        string
	WebhookSecret     string
	WebhookFailPolicy string
	WebhookSendImage  bool
}

type moderationStats struct {
	Allowed  int64 `json:"allowed"`
	Reviewed int64 `json:"reviewed"`
	Rejected int64 `json:"rejected"`
}

type auditEntry struct {
	Timestamp int64  `json:"ts"`
	Scope     string `json:"scope"`
	UserID    int64  `json:"userId"`
	Decision  string `json:"decision"`
	Rule      string `json:"rule,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type evalResult struct {
	Decision string `json:"decision"`
	Rule     string `json:"rule,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func handleRequest(ctx context.Context, req sdk.RuntimeRequest) (any, error) {
	cfg := parseConfig(req.Settings)
	switch req.Kind {
	case "hook":
		return handleHook(ctx, cfg, req)
	case "route":
		return handleRoute(ctx, cfg, req)
	default:
		return sdk.RuntimeResponse{Status: 400, Body: mustJSON(map[string]string{"error": "unsupported request kind"})}, nil
	}
}

func handleHook(ctx context.Context, cfg moderationConfig, req sdk.RuntimeRequest) (sdk.RuntimeResponse, error) {
	if req.Hook == nil {
		return sdk.RuntimeResponse{Status: 400, Body: mustJSON(map[string]string{"error": "missing hook payload"})}, nil
	}
	res := evaluateHook(ctx, cfg, *req.Hook)
	stats, logs := loadAuditState(req.Data)
	switch res.Decision {
	case "reject":
		stats.Rejected++
	case "review":
		stats.Reviewed++
	default:
		res.Decision = "allow"
		stats.Allowed++
	}
	logs = append(logs, auditEntry{
		Timestamp: time.Now().Unix(),
		Scope:     req.Hook.Scope,
		UserID:    req.Hook.UserID,
		Decision:  res.Decision,
		Rule:      res.Rule,
		Reason:    res.Reason,
	})
	if len(logs) > maxAuditEntries {
		logs = logs[len(logs)-maxAuditEntries:]
	}
	decision := sdk.HookDecision{
		Decision: res.Decision,
		Reason:   res.Reason,
	}
	return sdk.RuntimeResponse{
		Status: 200,
		Body:   mustJSON(decision),
		Writes: map[string]json.RawMessage{
			"stats":     mustJSON(stats),
			"audit_log": mustJSON(logs),
		},
	}, nil
}

func handleRoute(ctx context.Context, cfg moderationConfig, req sdk.RuntimeRequest) (sdk.RuntimeResponse, error) {
	if !req.Principal.Admin {
		return sdk.RuntimeResponse{Status: 403, Body: mustJSON(map[string]string{"error": "admin required"})}, nil
	}
	switch {
	case req.Method == http.MethodGet && req.Path == "/status":
		stats, logs := loadAuditState(req.Data)
		slices.Reverse(logs)
		return sdk.RuntimeResponse{
			Status: 200,
			Body: mustJSON(map[string]any{
				"mode":              cfg.Mode,
				"blockedWordCount":  len(cfg.BlockedWords),
				"reviewWordCount":   len(cfg.ReviewWords),
				"regexRuleCount":    len(cfg.RegexRules),
				"regexWarnings":     cfg.RegexWarnings,
				"rejectBlankSkin":   cfg.RejectBlankSkin,
				"rejectSolidSkin":   cfg.RejectSolidSkin,
				"webhookConfigured": cfg.WebhookURL != "",
				"webhookFailPolicy": cfg.WebhookFailPolicy,
				"stats":             stats,
				"logs":              logs,
			}),
		}, nil

	case req.Method == http.MethodPost && req.Path == "/test":
		var body struct {
			Scope string `json:"scope"`
			Text  string `json:"text"`
		}
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return sdk.RuntimeResponse{Status: 400, Body: mustJSON(map[string]string{"error": "请求内容无效"})}, nil
		}
		scope := strings.TrimSpace(body.Scope)
		if scope == "" {
			scope = "texture.upload"
		}
		res := evaluateHook(ctx, cfg, sdk.HookRequest{
			Name:   "text.check",
			Scope:  scope,
			Fields: map[string]string{"text": body.Text},
		})
		return sdk.RuntimeResponse{Status: 200, Body: mustJSON(res)}, nil

	case req.Method == http.MethodPost && req.Path == "/clear-logs":
		return sdk.RuntimeResponse{
			Status: 200,
			Body:   mustJSON(map[string]bool{"cleared": true}),
			Writes: map[string]json.RawMessage{
				"stats":     mustJSON(moderationStats{}),
				"audit_log": mustJSON([]auditEntry{}),
			},
		}, nil

	default:
		return sdk.RuntimeResponse{Status: 404, Body: mustJSON(map[string]string{"error": "not found"})}, nil
	}
}

func parseConfig(settings map[string]any) moderationConfig {
	cfg := moderationConfig{
		Mode:              "block",
		WebhookFailPolicy: "allow",
	}
	if mode := strings.ToLower(settingString(settings, "mode")); mode == "review" {
		cfg.Mode = "review"
	}
	cfg.BlockedWords = splitWordList(settingString(settings, "blocked_words"))
	cfg.ReviewWords = splitWordList(settingString(settings, "review_words"))
	cfg.RegexRules, cfg.RegexWarnings = compileRegexRules(settingString(settings, "regex_rules"))
	cfg.RejectBlankSkin = settingBool(settings, "reject_blank_skin")
	cfg.RejectSolidSkin = settingBool(settings, "reject_solid_skin")
	cfg.WebhookURL = strings.TrimSpace(settingString(settings, "webhook_url"))
	cfg.WebhookSecret = strings.TrimSpace(settingString(settings, "webhook_secret"))
	cfg.WebhookSendImage = settingBool(settings, "webhook_send_image")
	switch strings.ToLower(settingString(settings, "webhook_fail_policy")) {
	case "review", "reject":
		cfg.WebhookFailPolicy = strings.ToLower(settingString(settings, "webhook_fail_policy"))
	}
	return cfg
}

func settingString(settings map[string]any, key string) string {
	if settings == nil {
		return ""
	}
	v, _ := settings[key].(string)
	return strings.TrimSpace(v)
}

func settingBool(settings map[string]any, key string) bool {
	if settings == nil {
		return false
	}
	b, _ := settings[key].(bool)
	return b
}

func splitWordList(raw string) []string {
	if raw == "" {
		return nil
	}
	normalized := strings.NewReplacer("，", ",", "\r\n", ",", "\n", ",", "、", ",").Replace(raw)
	var out []string
	seen := map[string]bool{}
	for part := range strings.SplitSeq(normalized, ",") {
		part = strings.TrimSpace(part)
		if part != "" && !seen[part] {
			seen[part] = true
			out = append(out, part)
		}
	}
	return out
}

func compileRegexRules(raw string) ([]*regexp.Regexp, []string) {
	if raw == "" {
		return nil, nil
	}
	var rules []*regexp.Regexp
	var warnings []string
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for _, line := range lines {
		expr := strings.TrimSpace(line)
		if expr == "" {
			continue
		}
		if len(rules) >= maxRegexRules {
			warnings = append(warnings, fmt.Sprintf("正则规则超出上限 %d 条，已忽略后续规则", maxRegexRules))
			break
		}
		if utf8.RuneCountInString(expr) > maxRegexRuneLength {
			warnings = append(warnings, fmt.Sprintf("正则规则过长（超过 %d 字符）已跳过", maxRegexRuneLength))
			continue
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("正则语法错误 %q: %v", expr, err))
			continue
		}
		rules = append(rules, re)
	}
	return rules, warnings
}

func evaluateHook(ctx context.Context, cfg moderationConfig, hook sdk.HookRequest) evalResult {
	var textFields []string
	for _, k := range sortedKeys(hook.Fields) {
		if v := strings.TrimSpace(hook.Fields[k]); v != "" {
			textFields = append(textFields, v)
		}
	}
	if len(hook.Tags) > 0 {
		textFields = append(textFields, strings.Join(hook.Tags, " "))
	}

	// 1. Blocked words & regex checks (strictest).
	for _, rawVal := range textFields {
		plain, compact := normalizeText(rawVal)
		for _, word := range cfg.BlockedWords {
			if matchNormalizedWord(plain, compact, word) {
				return evalResult{
					Decision: applyPolicyDecision(cfg.Mode, hook.Scope, "reject"),
					Rule:     "blocked_word",
					Reason:   "内容包含敏感词汇，请修改后重试",
				}
			}
		}
		for _, re := range cfg.RegexRules {
			if re.MatchString(rawVal) || re.MatchString(plain) {
				return evalResult{
					Decision: applyPolicyDecision(cfg.Mode, hook.Scope, "reject"),
					Rule:     "regex_rule",
					Reason:   "内容触发正则合规规则，请调整后重试",
				}
			}
		}
	}

	// 2. Review words (always mark for review if matched).
	reviewTriggered := false
	for _, rawVal := range textFields {
		plain, compact := normalizeText(rawVal)
		for _, word := range cfg.ReviewWords {
			if matchNormalizedWord(plain, compact, word) {
				reviewTriggered = true
				break
			}
		}
		if reviewTriggered {
			break
		}
	}

	// 3. Local PNG image health check on texture.upload.check.
	if hook.PNGBase64 != "" && (cfg.RejectBlankSkin || cfg.RejectSolidSkin) {
		if imgRes := checkPNGHealth(cfg, hook.Scope, hook.PNGBase64); imgRes.Decision != "allow" {
			if imgRes.Decision == "reject" {
				return imgRes
			}
			reviewTriggered = true
			if imgRes.Reason != "" {
				return evalResult{
					Decision: "review",
					Rule:     imgRes.Rule,
					Reason:   imgRes.Reason,
				}
			}
		}
	}

	if reviewTriggered {
		return evalResult{
			Decision: "review",
			Rule:     "review_word",
			Reason:   "内容命中复核词表，已转入人工审核",
		}
	}

	// 4. Optional external moderation webhook check.
	if cfg.WebhookURL != "" {
		extRes := callExternalWebhook(ctx, cfg, hook)
		switch extRes.Decision {
		case "reject":
			extRes.Decision = applyPolicyDecision(cfg.Mode, hook.Scope, "reject")
			return extRes
		case "review":
			return extRes
		}
	}

	return evalResult{Decision: "allow"}
}

func isTextureScope(scope string) bool {
	return scope == "texture.upload" || scope == "texture.update"
}

func applyPolicyDecision(mode, scope, base string) string {
	if base == "reject" && mode == "review" && isTextureScope(scope) {
		return "review"
	}
	return base
}

func normalizeText(raw string) (string, string) {
	var plainBuf strings.Builder
	var compactBuf strings.Builder
	plainBuf.Grow(len(raw))
	compactBuf.Grow(len(raw))
	for _, r := range raw {
		if unicode.Is(unicode.Cf, r) || (r >= 0x200B && r <= 0x200F) || (r >= 0x202A && r <= 0x202E) || r == 0x2060 || r == 0xFEFF {
			continue
		}
		if r == 0x3000 {
			r = ' '
		} else if r >= 0xFF01 && r <= 0xFF5E {
			r -= 0xFEE0
		}
		r = unicode.ToLower(r)
		plainBuf.WriteRune(r)
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			compactBuf.WriteRune(r)
		}
	}
	return plainBuf.String(), compactBuf.String()
}

func matchNormalizedWord(plain, compact, rawWord string) bool {
	wPlain, wCompact := normalizeText(rawWord)
	wPlain = strings.TrimSpace(wPlain)
	if wPlain != "" && strings.Contains(plain, wPlain) {
		return true
	}
	if utf8.RuneCountInString(wCompact) >= 2 && strings.Contains(compact, wCompact) {
		return true
	}
	return false
}

func checkPNGHealth(cfg moderationConfig, scope, pngBase64 string) evalResult {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pngBase64))
	if err != nil {
		return evalResult{Decision: "allow"}
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return evalResult{Decision: "allow"}
	}
	bounds := img.Bounds()
	total := bounds.Dx() * bounds.Dy()
	if total <= 0 {
		return evalResult{Decision: "allow"}
	}
	var opaque int
	colors := map[uint32]struct{}{}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			alpha8 := a >> 8
			if alpha8 < 16 {
				continue
			}
			opaque++
			if len(colors) <= 2 {
				key := ((r >> 11) << 10) | ((g >> 11) << 5) | (b >> 11)
				colors[key] = struct{}{}
			}
		}
	}
	if cfg.RejectBlankSkin && float64(opaque)/float64(total) < 0.01 {
		return evalResult{
			Decision: applyPolicyDecision(cfg.Mode, scope, "reject"),
			Rule:     "image_blank",
			Reason:   "上传材质的有效非透明像素过少",
		}
	}
	if cfg.RejectSolidSkin && opaque > 0 && len(colors) <= 1 {
		return evalResult{
			Decision: "review",
			Rule:     "image_solid",
			Reason:   "上传材质几乎为纯单色，已转入人工审核",
		}
	}
	return evalResult{Decision: "allow"}
}

func callExternalWebhook(ctx context.Context, cfg moderationConfig, hook sdk.HookRequest) evalResult {
	failDecision := func(reason string) evalResult {
		switch cfg.WebhookFailPolicy {
		case "reject":
			return evalResult{Decision: "reject", Rule: "webhook_error", Reason: reason}
		case "review":
			if isTextureScope(hook.Scope) {
				return evalResult{Decision: "review", Rule: "webhook_error", Reason: reason}
			}
		}
		return evalResult{Decision: "allow"}
	}

	client, err := newSafeWebhookClient(cfg.WebhookURL)
	if err != nil {
		return failDecision("外部审核地址配置无效")
	}

	payload := map[string]any{
		"name":      hook.Name,
		"scope":     hook.Scope,
		"userId":    hook.UserID,
		"fields":    hook.Fields,
		"kind":      hook.Kind,
		"model":     hook.Model,
		"tags":      hook.Tags,
		"width":     hook.Width,
		"height":    hook.Height,
		"sizeBytes": hook.SizeBytes,
		"sha256":    hook.SHA256,
	}
	if cfg.WebhookSendImage && hook.PNGBase64 != "" {
		payload["pngBase64"] = hook.PNGBase64
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return failDecision("无法序列化外部审核请求")
	}
	callCtx, cancel := context.WithTimeout(ctx, webhookTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, cfg.WebhookURL, bytes.NewReader(encoded))
	if err != nil {
		return failDecision("外部审核请求无效")
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.WebhookSecret != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.WebhookSecret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return failDecision("外部审核接口超时或不可达")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return failDecision("外部审核接口返回异常状态")
	}
	rawResp, err := io.ReadAll(io.LimitReader(resp.Body, maxWebhookPayload))
	if err != nil {
		return failDecision("外部审核响应读取失败")
	}
	var out struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(rawResp, &out); err != nil {
		return failDecision("外部审核响应格式无效")
	}
	switch out.Decision {
	case "reject", "review":
		reason := strings.TrimSpace(out.Reason)
		if reason == "" {
			if out.Decision == "reject" {
				reason = "未通过外部合规审核"
			} else {
				reason = "外部审核建议人工复核"
			}
		}
		return evalResult{Decision: out.Decision, Rule: "external_webhook", Reason: reason}
	case "allow", "":
		return evalResult{Decision: "allow"}
	default:
		return failDecision("外部审核响应决策无效")
	}
}

func newSafeWebhookClient(rawURL string) (*http.Client, error) {
	u, err := url.Parse(rawURL)
	if err != nil || len(rawURL) > 2048 || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid webhook URL")
	}
	host := strings.ToLower(u.Hostname())
	isLoopbackHost := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost) {
		return nil, errors.New("webhook URL must use https or loopback http")
	}

	dialer := &net.Dialer{Timeout: webhookTimeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			h, p, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, h)
			if err != nil || len(ips) == 0 {
				return nil, errors.New("dns resolution failed")
			}
			for _, ipAddr := range ips {
				ip := ipAddr.IP
				if isLoopbackHost {
					if !ip.IsLoopback() {
						return nil, errors.New("loopback hostname resolved outside loopback")
					}
				} else if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
					return nil, errors.New("refusing private or link-local webhook target")
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), p))
		},
	}
	return &http.Client{
		Timeout:   webhookTimeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func loadAuditState(data map[string]json.RawMessage) (moderationStats, []auditEntry) {
	var stats moderationStats
	var logs []auditEntry
	if data != nil {
		if raw, ok := data["stats"]; ok && len(raw) > 0 {
			_ = json.Unmarshal(raw, &stats)
		}
		if raw, ok := data["audit_log"]; ok && len(raw) > 0 {
			_ = json.Unmarshal(raw, &logs)
		}
	}
	if logs == nil {
		logs = []auditEntry{}
	}
	return stats, logs
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}
