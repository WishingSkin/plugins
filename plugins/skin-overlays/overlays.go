package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/WishingSkin/plugins/shared/hostclient"
	"github.com/WishingSkin/plugins/shared/skinpng"
	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

const (
	maxCustomOverlays = 16
	maxOverlayBytes   = 32 << 10
)

var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type customOverlay struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	PNGBase64 string `json:"pngBase64"`
}

type overlayItem struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Category       string `json:"category"`
	Builtin        bool   `json:"builtin"`
	Enabled        bool   `json:"enabled"`
	PreviewDataURL string `json:"previewDataUrl"`
}

type layerSpec struct {
	ID      string  `json:"id"`
	Opacity float64 `json:"opacity"`
}

type overlayConfig struct {
	PointsCost      int64
	MaxLayers       int
	AllowPublicSave bool
}

func handleRequest(ctx context.Context, req sdk.RuntimeRequest) (any, error) {
	if req.Kind != "route" {
		return sdk.RuntimeResponse{Status: 400, Body: mustJSON(map[string]string{"error": "unsupported request"})}, nil
	}
	cfg := parseOverlayConfig(req.Settings)
	switch {
	case req.Method == http.MethodGet && req.Path == "/overlays":
		return routeListOverlays(cfg, req), nil
	case req.Method == http.MethodPost && req.Path == "/preview":
		return routePreviewOverlay(ctx, cfg, req), nil
	case req.Method == http.MethodPost && req.Path == "/apply":
		return routeApplyOverlay(ctx, cfg, req), nil
	case req.Method == http.MethodGet && req.Path == "/admin/overlays":
		if !req.Principal.Admin {
			return errResp(403, "需要管理员权限"), nil
		}
		return routeAdminListOverlays(req), nil
	case req.Method == http.MethodPost && req.Path == "/admin/overlays":
		if !req.Principal.Admin {
			return errResp(403, "需要管理员权限"), nil
		}
		return routeAdminSaveOverlay(req), nil
	case req.Method == http.MethodPost && req.Path == "/admin/overlays/delete":
		if !req.Principal.Admin {
			return errResp(403, "需要管理员权限"), nil
		}
		return routeAdminDeleteOverlay(req), nil
	default:
		return errResp(404, "not found"), nil
	}
}

func parseOverlayConfig(settings map[string]any) overlayConfig {
	cfg := overlayConfig{MaxLayers: 3}
	if settings == nil {
		return cfg
	}
	if v, ok := settings["points_cost"].(float64); ok && v > 0 {
		cfg.PointsCost = int64(v)
	}
	if v, ok := settings["max_layers"].(float64); ok && v >= 1 && v <= 5 {
		cfg.MaxLayers = int(v)
	}
	if v, ok := settings["allow_public_save"].(bool); ok {
		cfg.AllowPublicSave = v
	}
	return cfg
}

func loadCatalogState(data map[string]json.RawMessage) (map[string]bool, []customOverlay) {
	disabled := map[string]bool{}
	var customIDs []string
	if data != nil {
		if raw, ok := data["disabled_presets"]; ok && len(raw) > 0 {
			var ids []string
			if json.Unmarshal(raw, &ids) == nil {
				for _, id := range ids {
					disabled[id] = true
				}
			}
		}
		if raw, ok := data["overlay_index"]; ok && len(raw) > 0 {
			_ = json.Unmarshal(raw, &customIDs)
		}
	}
	var custom []customOverlay
	for _, id := range customIDs {
		if raw, ok := data["overlay:"+id]; ok && len(raw) > 0 {
			var item customOverlay
			if json.Unmarshal(raw, &item) == nil && item.ID != "" && item.PNGBase64 != "" {
				custom = append(custom, item)
			}
		}
	}
	return disabled, custom
}

func resolveOverlayImages(data map[string]json.RawMessage) map[string]*image.NRGBA {
	disabled, custom := loadCatalogState(data)
	out := map[string]*image.NRGBA{}
	for _, p := range builtinPresets {
		if !disabled[p.ID] {
			out[p.ID] = p.Image
		}
	}
	for _, c := range custom {
		if raw, err := base64.StdEncoding.DecodeString(c.PNGBase64); err == nil {
			if img, err := skinpng.DecodePNG(raw); err == nil {
				out[c.ID] = img
			}
		}
	}
	return out
}

func routeListOverlays(cfg overlayConfig, req sdk.RuntimeRequest) sdk.RuntimeResponse {
	disabled, custom := loadCatalogState(req.Data)
	items := []overlayItem{}
	for _, p := range builtinPresets {
		if disabled[p.ID] {
			continue
		}
		items = append(items, overlayItem{
			ID:             p.ID,
			Name:           p.Name,
			Category:       p.Category,
			Builtin:        true,
			Enabled:        true,
			PreviewDataURL: p.PreviewDataURL,
		})
	}
	for _, c := range custom {
		preview := ""
		if raw, err := base64.StdEncoding.DecodeString(c.PNGBase64); err == nil {
			if img, err := skinpng.DecodePNG(raw); err == nil {
				preview = renderOverlayPreviewDataURL(img)
			}
		}
		items = append(items, overlayItem{
			ID:             c.ID,
			Name:           c.Name,
			Category:       c.Category,
			Builtin:        false,
			Enabled:        true,
			PreviewDataURL: preview,
		})
	}
	return sdk.RuntimeResponse{
		Status: 200,
		Body: mustJSON(map[string]any{
			"overlays":        items,
			"pointsCost":      cfg.PointsCost,
			"maxLayers":       cfg.MaxLayers,
			"allowPublicSave": cfg.AllowPublicSave,
		}),
	}
}

func resolveBaseSkin(ctx context.Context, userID int64, fileHash, pngBase64 string) (*image.NRGBA, error) {
	var rawPNG []byte
	if fileHash != "" {
		if !hashPattern.MatchString(fileHash) {
			return nil, errors.New("无效的材质哈希")
		}
		hc, err := hostclient.FromEnv()
		if err != nil {
			return nil, errors.New("宿主服务未连接")
		}
		rawPNG, err = hc.TextureBytes(ctx, fileHash, userID)
		if err != nil {
			return nil, err
		}
	} else if pngBase64 != "" {
		clean := strings.TrimSpace(pngBase64)
		if idx := strings.Index(clean, ","); idx >= 0 && strings.Contains(clean[:idx], "base64") {
			clean = clean[idx+1:]
		}
		var err error
		rawPNG, err = base64.StdEncoding.DecodeString(clean)
		if err != nil {
			return nil, errors.New("PNG Base64 数据无效")
		}
	} else {
		return nil, errors.New("请选择基础皮肤")
	}

	if len(rawPNG) > 8<<20 {
		return nil, errors.New("PNG 文件超过 8 MiB")
	}
	img, err := skinpng.DecodePNG(rawPNG)
	if err != nil {
		return nil, errors.New("无法解析 PNG 图像")
	}
	if err := skinpng.ValidateSkinDimensions(img.Bounds().Dx(), img.Bounds().Dy()); err != nil {
		return nil, err
	}
	return img, nil
}

func composeSkinLayers(base *image.NRGBA, data map[string]json.RawMessage, layers []layerSpec, maxLayers int, upgradeLegacy, clearHat bool) (*image.NRGBA, error) {
	if len(layers) == 0 {
		return nil, errors.New("请至少选择一个装饰叠层")
	}
	if len(layers) > maxLayers {
		return nil, fmt.Errorf("单次最多叠加 %d 个装饰层", maxLayers)
	}
	catalog := resolveOverlayImages(data)
	work := base
	if upgradeLegacy {
		work = skinpng.Upgrade64x32To64x64(work)
	} else {
		work = skinpng.ToNRGBA(work)
	}
	if clearHat {
		work = skinpng.ClearHatLayer(work)
	}
	for _, layer := range layers {
		ovImg, ok := catalog[layer.ID]
		if !ok {
			return nil, fmt.Errorf("装饰层 %q 不可用或已停用", layer.ID)
		}
		op := layer.Opacity
		if op <= 0 {
			op = 1.0
		}
		if op > 1 {
			op = 1.0
		}
		work = skinpng.CompositeOverlay(work, ovImg, op)
	}
	return work, nil
}

func routePreviewOverlay(ctx context.Context, cfg overlayConfig, req sdk.RuntimeRequest) sdk.RuntimeResponse {
	var body struct {
		FileHash      string      `json:"fileHash"`
		PNGBase64     string      `json:"pngBase64"`
		Model         string      `json:"model"`
		UpgradeLegacy *bool       `json:"upgradeLegacy"`
		ClearHat      bool        `json:"clearHat"`
		Layers        []layerSpec `json:"layers"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return errResp(400, "请求参数无效")
	}
	base, err := resolveBaseSkin(ctx, req.Principal.ID, body.FileHash, body.PNGBase64)
	if err != nil {
		var apiErr *hostclient.APIError
		if errors.As(err, &apiErr) {
			return errResp(apiErr.Status, apiErr.Message)
		}
		return errResp(400, err.Error())
	}
	upgrade := body.UpgradeLegacy == nil || *body.UpgradeLegacy
	comp, err := composeSkinLayers(base, req.Data, body.Layers, cfg.MaxLayers, upgrade, body.ClearHat)
	if err != nil {
		return errResp(400, err.Error())
	}
	outBytes, err := skinpng.EncodePNG(comp)
	if err != nil {
		return errResp(500, "无法编码合成图像")
	}
	slim := body.Model == "slim"
	frontBytes, _ := skinpng.EncodePNG(skinpng.RenderBodyFront(comp, 6, slim, true))
	backBytes, _ := skinpng.EncodePNG(skinpng.RenderBodyBack(comp, 6, slim, true))
	return sdk.RuntimeResponse{
		Status: 200,
		Body: mustJSON(map[string]any{
			"pngBase64":          base64.StdEncoding.EncodeToString(outBytes),
			"frontPreviewBase64": base64.StdEncoding.EncodeToString(frontBytes),
			"backPreviewBase64":  base64.StdEncoding.EncodeToString(backBytes),
			"width":              comp.Bounds().Dx(),
			"height":             comp.Bounds().Dy(),
		}),
	}
}

func routeApplyOverlay(ctx context.Context, cfg overlayConfig, req sdk.RuntimeRequest) sdk.RuntimeResponse {
	var body struct {
		FileHash      string      `json:"fileHash"`
		PNGBase64     string      `json:"pngBase64"`
		Name          string      `json:"name"`
		Model         string      `json:"model"`
		IsPublic      bool        `json:"isPublic"`
		CharacterID   int64       `json:"characterId"`
		UpgradeLegacy *bool       `json:"upgradeLegacy"`
		ClearHat      bool        `json:"clearHat"`
		Layers        []layerSpec `json:"layers"`
		RequestID     string      `json:"requestId"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return errResp(400, "请求参数无效")
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || utf8.RuneCountInString(name) > 255 {
		return errResp(400, "请填写合成后的皮肤名称")
	}
	model := "default"
	if body.Model == "slim" {
		model = "slim"
	}
	isPublic := body.IsPublic && cfg.AllowPublicSave

	base, err := resolveBaseSkin(ctx, req.Principal.ID, body.FileHash, body.PNGBase64)
	if err != nil {
		return errResp(400, err.Error())
	}
	upgrade := body.UpgradeLegacy == nil || *body.UpgradeLegacy
	comp, err := composeSkinLayers(base, req.Data, body.Layers, cfg.MaxLayers, upgrade, body.ClearHat)
	if err != nil {
		return errResp(400, err.Error())
	}
	outBytes, err := skinpng.EncodePNG(comp)
	if err != nil {
		return errResp(500, "无法编码合成图像")
	}

	hc, err := hostclient.FromEnv()
	if err != nil {
		return errResp(503, "宿主服务未连接")
	}

	var balance string
	var pointsSpent int64
	if cfg.PointsCost > 0 {
		refID := strings.TrimSpace(body.RequestID)
		if refID == "" || utf8.RuneCountInString(refID) > 64 {
			refID = "overlay-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		spendRes, err := hc.SpendPoints(ctx, req.Principal.ID, cfg.PointsCost, refID)
		if err != nil {
			var apiErr *hostclient.APIError
			if errors.As(err, &apiErr) {
				return errResp(apiErr.Status, apiErr.Message)
			}
			return errResp(503, "扣减积分失败")
		}
		balance = spendRes.Balance
		pointsSpent = cfg.PointsCost
	}

	rec, err := hc.UploadTexture(ctx, req.Principal.ID, hostclient.UploadTextureRequest{
		Name:     name,
		Kind:     "skin",
		Model:    model,
		IsPublic: isPublic,
		PNGBytes: outBytes,
	})
	if err != nil {
		var apiErr *hostclient.APIError
		if errors.As(err, &apiErr) {
			return errResp(apiErr.Status, apiErr.Message)
		}
		return errResp(503, "保存合成皮肤失败")
	}

	bound := false
	pendingReview := rec.ReviewState != "" && rec.ReviewState != "approved"
	if body.CharacterID > 0 && !pendingReview {
		if err := hc.BindCharacterTexture(ctx, req.Principal.ID, body.CharacterID, "skin", rec.ID, model); err == nil {
			bound = true
		}
	}

	return sdk.RuntimeResponse{
		Status: 200,
		Body: mustJSON(map[string]any{
			"record":        rec,
			"bound":         bound,
			"pendingReview": pendingReview,
			"pointsSpent":   pointsSpent,
			"balance":       balance,
		}),
	}
}

func routeAdminListOverlays(req sdk.RuntimeRequest) sdk.RuntimeResponse {
	disabled, custom := loadCatalogState(req.Data)
	presets := make([]overlayItem, 0, len(builtinPresets))
	for _, p := range builtinPresets {
		presets = append(presets, overlayItem{
			ID:             p.ID,
			Name:           p.Name,
			Category:       p.Category,
			Builtin:        true,
			Enabled:        !disabled[p.ID],
			PreviewDataURL: p.PreviewDataURL,
		})
	}
	customItems := make([]overlayItem, 0, len(custom))
	for _, c := range custom {
		preview := ""
		if raw, err := base64.StdEncoding.DecodeString(c.PNGBase64); err == nil {
			if img, err := skinpng.DecodePNG(raw); err == nil {
				preview = renderOverlayPreviewDataURL(img)
			}
		}
		customItems = append(customItems, overlayItem{
			ID:             c.ID,
			Name:           c.Name,
			Category:       c.Category,
			Builtin:        false,
			Enabled:        true,
			PreviewDataURL: preview,
		})
	}
	return sdk.RuntimeResponse{
		Status: 200,
		Body: mustJSON(map[string]any{
			"presets": presets,
			"custom":  customItems,
		}),
	}
}

func routeAdminSaveOverlay(req sdk.RuntimeRequest) sdk.RuntimeResponse {
	var body struct {
		Action    string `json:"action"`
		ID        string `json:"id"`
		Enabled   bool   `json:"enabled"`
		Name      string `json:"name"`
		Category  string `json:"category"`
		PNGBase64 string `json:"pngBase64"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return errResp(400, "请求参数无效")
	}

	disabled, custom := loadCatalogState(req.Data)
	if body.Action == "toggle" {
		found := false
		for _, p := range builtinPresets {
			if p.ID == body.ID {
				found = true
				break
			}
		}
		if !found {
			return errResp(404, "内置装饰层不存在")
		}
		if body.Enabled {
			delete(disabled, body.ID)
		} else {
			disabled[body.ID] = true
		}
		var disabledIDs []string
		for id := range disabled {
			disabledIDs = append(disabledIDs, id)
		}
		slices.Sort(disabledIDs)
		return sdk.RuntimeResponse{
			Status: 200,
			Body:   mustJSON(map[string]bool{"saved": true}),
			Writes: map[string]json.RawMessage{
				"disabled_presets": mustJSON(disabledIDs),
			},
		}
	}

	if len(custom) >= maxCustomOverlays {
		return errResp(409, fmt.Sprintf("最多支持 %d 个自定义装饰层", maxCustomOverlays))
	}
	name := strings.TrimSpace(body.Name)
	category := strings.TrimSpace(body.Category)
	if category == "" {
		category = "自定义"
	}
	if name == "" || utf8.RuneCountInString(name) > 64 || utf8.RuneCountInString(category) > 32 {
		return errResp(400, "名称或分类格式无效")
	}
	cleanB64 := strings.TrimSpace(body.PNGBase64)
	if idx := strings.Index(cleanB64, ","); idx >= 0 && strings.Contains(cleanB64[:idx], "base64") {
		cleanB64 = cleanB64[idx+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(cleanB64)
	if err != nil || len(raw) == 0 || len(raw) > maxOverlayBytes {
		return errResp(400, "装饰层 PNG 不能超过 32 KiB 且须为有效 Base64")
	}
	img, err := skinpng.DecodePNG(raw)
	if err != nil {
		return errResp(400, "无法解析装饰层 PNG")
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if (w != 64 && w != 128) || h != w {
		return errResp(400, "自定义装饰层尺寸须为 64×64 或 128×128 正方形 PNG")
	}

	id := "custom_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	entry := customOverlay{
		ID:        id,
		Name:      name,
		Category:  category,
		PNGBase64: base64.StdEncoding.EncodeToString(raw),
	}
	var ids []string
	for _, c := range custom {
		ids = append(ids, c.ID)
	}
	ids = append(ids, id)
	return sdk.RuntimeResponse{
		Status: 200,
		Body:   mustJSON(map[string]any{"id": id, "saved": true}),
		Writes: map[string]json.RawMessage{
			"overlay_index": mustJSON(ids),
			"overlay:" + id: mustJSON(entry),
		},
	}
}

func routeAdminDeleteOverlay(req sdk.RuntimeRequest) sdk.RuntimeResponse {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil || strings.TrimSpace(body.ID) == "" {
		return errResp(400, "缺少装饰层 ID")
	}
	_, custom := loadCatalogState(req.Data)
	var nextIDs []string
	found := false
	for _, c := range custom {
		if c.ID == body.ID {
			found = true
			continue
		}
		nextIDs = append(nextIDs, c.ID)
	}
	if !found {
		return errResp(404, "自定义装饰层不存在")
	}
	if nextIDs == nil {
		nextIDs = []string{}
	}
	return sdk.RuntimeResponse{
		Status: 200,
		Body:   mustJSON(map[string]bool{"deleted": true}),
		Writes: map[string]json.RawMessage{
			"overlay_index":      mustJSON(nextIDs),
			"overlay:" + body.ID: json.RawMessage("null"),
		},
	}
}

func errResp(status int, msg string) sdk.RuntimeResponse {
	return sdk.RuntimeResponse{Status: status, Body: mustJSON(map[string]string{"error": msg})}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}
