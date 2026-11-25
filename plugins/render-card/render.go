package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"image/color"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/WishingSkin/plugins/shared/hostclient"
	"github.com/WishingSkin/plugins/shared/skinpng"
	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

var charNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)

type renderConfig struct {
	CacheMaxAge       int
	DefaultTheme      string
	ShowVerifiedBadge bool
}

func handleRequest(ctx context.Context, req sdk.RuntimeRequest) (any, error) {
	if req.Kind != "route" {
		return errResp(400, "unsupported request"), nil
	}
	cfg := parseRenderConfig(req.Settings)
	switch {
	case req.Method == http.MethodGet && req.Path == "/avatar":
		return routeAvatar(ctx, cfg, req), nil
	case req.Method == http.MethodGet && req.Path == "/body":
		return routeBody(ctx, cfg, req), nil
	case req.Method == http.MethodGet && req.Path == "/card":
		return routeCard(ctx, cfg, req), nil
	case req.Method == http.MethodGet && req.Path == "/info":
		return routeInfo(ctx, cfg), nil
	default:
		return errResp(404, "not found"), nil
	}
}

func parseRenderConfig(settings map[string]any) renderConfig {
	cfg := renderConfig{
		CacheMaxAge:       300,
		DefaultTheme:      "light",
		ShowVerifiedBadge: true,
	}
	if settings == nil {
		return cfg
	}
	if v, ok := settings["cache_max_age"].(float64); ok && v >= 0 && v <= 86400 {
		cfg.CacheMaxAge = int(v)
	}
	if v, ok := settings["default_theme"].(string); ok {
		if t := normalizeTheme(v); t != "" {
			cfg.DefaultTheme = t
		}
	}
	if v, ok := settings["show_verified_badge"].(bool); ok {
		cfg.ShowVerifiedBadge = v
	}
	return cfg
}

func normalizeTheme(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "light", "dark", "emerald":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

func loadCharacterAndSkin(ctx context.Context, queryRaw string) (hostclient.CharacterInfo, *image.NRGBA, url.Values, *sdk.RuntimeResponse) {
	q, _ := url.ParseQuery(queryRaw)
	name := strings.TrimSpace(q.Get("name"))
	if !charNamePattern.MatchString(name) {
		r := errResp(400, "invalid character name")
		return hostclient.CharacterInfo{}, nil, q, &r
	}
	hc, err := hostclient.FromEnv()
	if err != nil {
		r := errResp(503, "host api unavailable")
		return hostclient.CharacterInfo{}, nil, q, &r
	}
	ch, err := hc.CharacterByName(ctx, name)
	if err != nil {
		var apiErr *hostclient.APIError
		if errors.As(err, &apiErr) {
			r := errResp(apiErr.Status, apiErr.Message)
			return hostclient.CharacterInfo{}, nil, q, &r
		}
		r := errResp(503, "unable to fetch character")
		return hostclient.CharacterInfo{}, nil, q, &r
	}
	var skin *image.NRGBA
	if ch.SkinHash != "" {
		if rawPNG, err := hc.TextureBytes(ctx, ch.SkinHash, 0); err == nil {
			if decoded, err := skinpng.DecodePNG(rawPNG); err == nil {
				skin = decoded
			}
		}
	}
	if skin == nil {
		skin = defaultMannequinSkin(ch.Model == "slim")
	}
	return ch, skin, q, nil
}

func defaultMannequinSkin(slim bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	skinTone := color.NRGBA{230, 188, 156, 255}
	hair := color.NRGBA{72, 54, 44, 255}
	shirt := color.NRGBA{56, 152, 188, 255}
	pants := color.NRGBA{68, 76, 132, 255}
	eye := color.NRGBA{48, 86, 148, 255}
	fill := func(x, y, w, h int, c color.NRGBA) {
		for py := range h {
			for px := range w {
				img.SetNRGBA(x+px, y+py, c)
			}
		}
	}
	fill(8, 0, 24, 8, hair)
	fill(0, 8, 32, 8, skinTone)
	fill(8, 8, 8, 2, hair)
	img.SetNRGBA(10, 12, eye)
	img.SetNRGBA(13, 12, eye)
	fill(16, 16, 24, 16, shirt)
	fill(0, 16, 16, 16, pants)
	fill(16, 48, 16, 16, pants)
	armW := 4
	if slim {
		armW = 3
	}
	fill(44, 20, armW, 4, shirt)
	fill(44, 24, armW, 8, skinTone)
	fill(36, 52, armW, 4, shirt)
	fill(36, 56, armW, 8, skinTone)
	return img
}

func makeETag(payload []byte) string {
	sum := sha256.Sum256(payload)
	return `"rc-` + hex.EncodeToString(sum[:8]) + `"`
}

func routeAvatar(ctx context.Context, cfg renderConfig, req sdk.RuntimeRequest) sdk.RuntimeResponse {
	_, skin, q, errR := loadCharacterAndSkin(ctx, req.Query)
	if errR != nil {
		return *errR
	}
	size := 128
	if parsed, err := strconv.Atoi(q.Get("size")); err == nil && parsed > 0 {
		size = min(512, max(16, parsed))
	}
	withHat := q.Get("hat") != "0"
	avatar := skinpng.RenderHead(skin, size, withHat)
	raw, err := skinpng.EncodePNG(avatar)
	if err != nil {
		return errResp(500, "encode failed")
	}
	return sdk.RuntimeResponse{
		Status:       200,
		ContentType:  "image/png",
		BodyBase64:   base64.StdEncoding.EncodeToString(raw),
		CacheControl: fmt.Sprintf("public, max-age=%d", cfg.CacheMaxAge),
		ETag:         makeETag(raw),
	}
}

func routeBody(ctx context.Context, cfg renderConfig, req sdk.RuntimeRequest) sdk.RuntimeResponse {
	ch, skin, q, errR := loadCharacterAndSkin(ctx, req.Query)
	if errR != nil {
		return *errR
	}
	scale := 6
	if parsed, err := strconv.Atoi(q.Get("scale")); err == nil && parsed > 0 {
		scale = min(16, max(2, parsed))
	}
	withOuter := q.Get("outer") != "0"
	slim := ch.Model == "slim"
	var bodyImg *image.NRGBA
	if strings.EqualFold(q.Get("side"), "back") {
		bodyImg = skinpng.RenderBodyBack(skin, scale, slim, withOuter)
	} else {
		bodyImg = skinpng.RenderBodyFront(skin, scale, slim, withOuter)
	}
	raw, err := skinpng.EncodePNG(bodyImg)
	if err != nil {
		return errResp(500, "encode failed")
	}
	return sdk.RuntimeResponse{
		Status:       200,
		ContentType:  "image/png",
		BodyBase64:   base64.StdEncoding.EncodeToString(raw),
		CacheControl: fmt.Sprintf("public, max-age=%d", cfg.CacheMaxAge),
		ETag:         makeETag(raw),
	}
}

func routeCard(ctx context.Context, cfg renderConfig, req sdk.RuntimeRequest) sdk.RuntimeResponse {
	ch, skin, q, errR := loadCharacterAndSkin(ctx, req.Query)
	if errR != nil {
		return *errR
	}
	theme := normalizeTheme(q.Get("theme"))
	if theme == "" {
		theme = cfg.DefaultTheme
	}
	siteName := "WishingSkin"
	if hc, err := hostclient.FromEnv(); err == nil {
		if info, err := hc.Site(ctx); err == nil && strings.TrimSpace(info.Name) != "" {
			siteName = info.Name
		}
	}

	avatarRaw, _ := skinpng.EncodePNG(skinpng.RenderHead(skin, 96, true))
	avatarURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(avatarRaw)

	bg, border, fg, muted, pillBg, pillFg := cardPalette(theme)
	modelLabel := "Classic (Steve)"
	if ch.Model == "slim" {
		modelLabel = "Slim (Alex)"
	}

	verifiedBadge := ""
	if cfg.ShowVerifiedBadge && ch.VerifiedMinecraft {
		verifiedBadge = fmt.Sprintf(`<rect x="310" y="24" width="110" height="24" rx="12" fill="%s"/><text x="365" y="40" text-anchor="middle" font-family="system-ui,-apple-system,sans-serif" font-size="11" font-weight="600" fill="%s">正版 Minecraft</text>`, pillBg, pillFg)
	}

	shortUUID := ch.UUID
	if len(shortUUID) > 16 {
		shortUUID = shortUUID[:8] + "-" + shortUUID[8:12] + "-" + shortUUID[12:16] + "..."
	}

	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="440" height="140" viewBox="0 0 440 140" role="img" aria-label="%s">
<rect x="0.5" y="0.5" width="439" height="139" rx="14" fill="%s" stroke="%s"/>
<rect x="20" y="24" width="92" height="92" rx="12" fill="%s" stroke="%s"/>
<image x="28" y="32" width="76" height="76" href="%s" style="image-rendering:pixelated"/>
<text x="128" y="46" font-family="system-ui,-apple-system,sans-serif" font-size="20" font-weight="700" fill="%s">%s</text>
%s
<text x="128" y="72" font-family="system-ui,-apple-system,sans-serif" font-size="12" fill="%s">模型: %s  站点: %s</text>
<text x="128" y="96" font-family="ui-monospace,SFMono-Regular,Consolas,monospace" font-size="11" fill="%s">UUID: %s</text>
<text x="128" y="116" font-family="system-ui,-apple-system,sans-serif" font-size="11" fill="%s">Powered by WishingSkin</text>
</svg>`,
		html.EscapeString(ch.Name),
		bg, border,
		pillBg, border, avatarURI,
		fg, html.EscapeString(ch.Name),
		verifiedBadge,
		muted, html.EscapeString(modelLabel), html.EscapeString(siteName),
		muted, html.EscapeString(shortUUID),
		muted,
	)

	svgBytes := []byte(svg)
	return sdk.RuntimeResponse{
		Status:       200,
		ContentType:  "image/svg+xml",
		BodyBase64:   base64.StdEncoding.EncodeToString(svgBytes),
		CacheControl: fmt.Sprintf("public, max-age=%d", cfg.CacheMaxAge),
		ETag:         makeETag(svgBytes),
	}
}

func cardPalette(theme string) (bg, border, fg, muted, pillBg, pillFg string) {
	switch theme {
	case "dark":
		return "#18181b", "#2e2e34", "#f4f4f5", "#a1a1aa", "#27272a", "#4ade80"
	case "emerald":
		return "#0d1f17", "#1c3d2d", "#ecfdf5", "#a7f3d0", "#14532d", "#86efac"
	default:
		return "#ffffff", "#e4e4e7", "#18181b", "#52525b", "#eff6ff", "#1d4ed8"
	}
}

func routeInfo(ctx context.Context, cfg renderConfig) sdk.RuntimeResponse {
	var publicURL, siteName string
	if hc, err := hostclient.FromEnv(); err == nil {
		if info, err := hc.Site(ctx); err == nil {
			publicURL = strings.TrimRight(info.PublicURL, "/")
			siteName = info.Name
		}
	}
	return sdk.RuntimeResponse{
		Status: 200,
		Body: mustJSON(map[string]any{
			"publicUrl":         publicURL,
			"siteName":          siteName,
			"defaultTheme":      cfg.DefaultTheme,
			"showVerifiedBadge": cfg.ShowVerifiedBadge,
		}),
	}
}

func errResp(status int, msg string) sdk.RuntimeResponse {
	return sdk.RuntimeResponse{Status: status, Body: mustJSON(map[string]string{"error": msg})}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}
