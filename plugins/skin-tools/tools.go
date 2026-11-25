package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/WishingSkin/plugins/shared/hostclient"
	"github.com/WishingSkin/plugins/shared/skinpng"
	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type transformRequest struct {
	FileHash      string  `json:"fileHash"`
	PNGBase64     string  `json:"pngBase64"`
	Model         string  `json:"model"`
	UpgradeLegacy bool    `json:"upgradeLegacy"`
	ArmConvert    string  `json:"armConvert"`
	HueShift      float64 `json:"hueShift"`
	Saturation    float64 `json:"saturation"`
	Brightness    float64 `json:"brightness"`
	Grayscale     bool    `json:"grayscale"`
	StripOuter    bool    `json:"stripOuter"`
	CleanAlpha    bool    `json:"cleanAlpha"`
}

func handleRequest(ctx context.Context, req sdk.RuntimeRequest) (any, error) {
	if req.Kind != "route" {
		return errResp(400, "unsupported request"), nil
	}
	if req.Method == http.MethodPost && req.Path == "/transform" {
		return handleTransform(ctx, req), nil
	}
	return errResp(404, "not found"), nil
}

func handleTransform(ctx context.Context, req sdk.RuntimeRequest) sdk.RuntimeResponse {
	var body transformRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return errResp(400, "请求参数无效")
	}

	var rawPNG []byte
	var err error
	if body.FileHash != "" {
		if !hashPattern.MatchString(body.FileHash) {
			return errResp(400, "材质哈希格式无效")
		}
		hc, hcErr := hostclient.FromEnv()
		if hcErr != nil {
			return errResp(503, "宿主服务未连接")
		}
		rawPNG, err = hc.TextureBytes(ctx, body.FileHash, req.Principal.ID)
		if err != nil {
			var apiErr *hostclient.APIError
			if errors.As(err, &apiErr) {
				return errResp(apiErr.Status, apiErr.Message)
			}
			return errResp(503, "读取材质失败")
		}
	} else if body.PNGBase64 != "" {
		clean := strings.TrimSpace(body.PNGBase64)
		if idx := strings.Index(clean, ","); idx >= 0 && strings.Contains(clean[:idx], "base64") {
			clean = clean[idx+1:]
		}
		rawPNG, err = base64.StdEncoding.DecodeString(clean)
		if err != nil {
			return errResp(400, "PNG Base64 数据无效")
		}
	} else {
		return errResp(400, "请先选择皮肤或上传本地 PNG 文件")
	}

	if len(rawPNG) == 0 || len(rawPNG) > 8<<20 {
		return errResp(400, "PNG 文件大小无效")
	}
	img, err := skinpng.DecodePNG(rawPNG)
	if err != nil {
		return errResp(400, "无法解析 PNG 图像")
	}
	if err := skinpng.ValidateSkinDimensions(img.Bounds().Dx(), img.Bounds().Dy()); err != nil {
		return errResp(400, err.Error())
	}

	outModel := "default"
	if body.Model == "slim" {
		outModel = "slim"
	}

	// Deterministic transform pipeline order matching plan spec:
	// Upgrade64x32To64x64 -> ConvertArmModel -> StripOuterLayers -> CleanAlphaNoise -> AdjustColors
	work := skinpng.ToNRGBA(img)
	if body.UpgradeLegacy {
		work = skinpng.Upgrade64x32To64x64(work)
	}
	switch body.ArmConvert {
	case "to_slim":
		work = skinpng.ConvertArmModel(work, true)
		outModel = "slim"
	case "to_classic":
		work = skinpng.ConvertArmModel(work, false)
		outModel = "default"
	case "", "none":
	default:
		return errResp(400, "手臂模型转换参数无效")
	}
	if body.StripOuter {
		work = skinpng.StripOuterLayers(work)
	}
	if body.CleanAlpha {
		work = skinpng.CleanAlphaNoise(work, 16)
	}
	hue := clampFloat(body.HueShift, -180, 180)
	sat := clampFloat(body.Saturation, -100, 100)
	bright := clampFloat(body.Brightness, -100, 100)
	if hue != 0 || sat != 0 || bright != 0 || body.Grayscale {
		work = skinpng.AdjustColors(work, hue, sat, bright, body.Grayscale)
	}

	outBytes, err := skinpng.EncodePNG(work)
	if err != nil {
		return errResp(500, "无法编码输出图像")
	}
	slim := outModel == "slim"
	frontBytes, _ := skinpng.EncodePNG(skinpng.RenderBodyFront(work, 6, slim, true))
	backBytes, _ := skinpng.EncodePNG(skinpng.RenderBodyBack(work, 6, slim, true))

	return sdk.RuntimeResponse{
		Status: 200,
		Body: mustJSON(map[string]any{
			"pngBase64":          base64.StdEncoding.EncodeToString(outBytes),
			"frontPreviewBase64": base64.StdEncoding.EncodeToString(frontBytes),
			"backPreviewBase64":  base64.StdEncoding.EncodeToString(backBytes),
			"width":              work.Bounds().Dx(),
			"height":             work.Bounds().Dy(),
			"model":              outModel,
		}),
	}
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func errResp(status int, msg string) sdk.RuntimeResponse {
	return sdk.RuntimeResponse{Status: status, Body: mustJSON(map[string]string{"error": msg})}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}
