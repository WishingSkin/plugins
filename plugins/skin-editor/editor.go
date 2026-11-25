package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"net/http"

	"github.com/WishingSkin/plugins/shared/skinpng"
	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

type skinTemplate struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Model     string `json:"model"`
	PNGBase64 string `json:"pngBase64"`
}

func handleRequest(_ context.Context, req sdk.RuntimeRequest) (any, error) {
	if req.Kind != "route" {
		return errResp(400, "unsupported request"), nil
	}
	if req.Method == http.MethodGet && req.Path == "/templates" {
		return sdk.RuntimeResponse{
			Status: 200,
			Body:   mustJSON(map[string]any{"templates": buildTemplates()}),
		}, nil
	}
	return errResp(404, "not found"), nil
}

func buildTemplates() []skinTemplate {
	classic := buildMannequinTemplate(false)
	slim := buildMannequinTemplate(true)
	blank := image.NewNRGBA(image.Rect(0, 0, 64, 64))

	encode := func(img *image.NRGBA) string {
		b, _ := skinpng.EncodePNG(img)
		return base64.StdEncoding.EncodeToString(b)
	}
	return []skinTemplate{
		{ID: "classic_base", Name: "标准基础人模 (Steve 4px)", Model: "default", PNGBase64: encode(classic)},
		{ID: "slim_base", Name: "纤细基础人模 (Alex 3px)", Model: "slim", PNGBase64: encode(slim)},
		{ID: "blank", Name: "空白透明画布 (64×64)", Model: "default", PNGBase64: encode(blank)},
	}
}

func buildMannequinTemplate(slim bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	skin := color.NRGBA{234, 192, 160, 255}
	skinShade := color.NRGBA{214, 168, 136, 255}
	hair := color.NRGBA{72, 52, 42, 255}
	eyeWhite := color.NRGBA{255, 255, 255, 255}
	eyePupil := color.NRGBA{54, 118, 186, 255}
	shirt := color.NRGBA{56, 152, 188, 255}
	shirtShade := color.NRGBA{42, 124, 156, 255}
	pants := color.NRGBA{68, 76, 132, 255}
	pantsShade := color.NRGBA{54, 62, 112, 255}
	shoe := color.NRGBA{76, 76, 84, 255}

	fill := func(x, y, w, h int, c color.NRGBA) {
		for py := range h {
			for px := range w {
				img.SetNRGBA(x+px, y+py, c)
			}
		}
	}

	// Head cuboid (0,0) -> top, bottom, right, front, left, back
	fill(8, 0, 8, 8, hair)
	fill(16, 0, 8, 8, skinShade)
	fill(0, 8, 8, 8, skinShade)
	fill(8, 8, 8, 8, skin)
	fill(16, 8, 8, 8, skinShade)
	fill(24, 8, 8, 8, hair)
	fill(0, 8, 8, 3, hair)
	fill(8, 8, 8, 2, hair)
	fill(16, 8, 8, 3, hair)
	// Eyes
	img.SetNRGBA(9, 12, eyeWhite)
	img.SetNRGBA(10, 12, eyePupil)
	img.SetNRGBA(13, 12, eyePupil)
	img.SetNRGBA(14, 12, eyeWhite)

	// Torso cuboid (16,16)
	fill(20, 16, 8, 4, shirtShade)
	fill(28, 16, 8, 4, shirtShade)
	fill(16, 20, 4, 12, shirtShade)
	fill(20, 20, 8, 12, shirt)
	fill(28, 20, 4, 12, shirtShade)
	fill(32, 20, 8, 12, shirtShade)

	// Legs: Right (0,16), Left (16,48)
	fillLeg := func(x0, y0 int) {
		fill(x0+4, y0, 4, 4, pantsShade)
		fill(x0+8, y0, 4, 4, shoe)
		fill(x0, y0+4, 4, 10, pantsShade)
		fill(x0+4, y0+4, 4, 10, pants)
		fill(x0+8, y0+4, 4, 10, pantsShade)
		fill(x0+12, y0+4, 4, 10, pantsShade)
		fill(x0, y0+14, 16, 2, shoe)
	}
	fillLeg(0, 16)
	fillLeg(16, 48)

	// Arms: Right (40,16), Left (32,48)
	armW := 4
	if slim {
		armW = 3
	}
	fillArm := func(x0, y0 int) {
		fill(x0+4, y0, armW, 4, shirtShade)
		fill(x0+4+armW, y0, armW, 4, skinShade)
		fill(x0, y0+4, 4, 4, shirtShade)
		fill(x0+4, y0+4, armW, 4, shirt)
		fill(x0+4+armW, y0+4, 4, 4, shirtShade)
		fill(x0+8+armW, y0+4, armW, 4, shirtShade)
		fill(x0, y0+8, 4, 8, skinShade)
		fill(x0+4, y0+8, armW, 8, skin)
		fill(x0+4+armW, y0+8, 4, 8, skinShade)
		fill(x0+8+armW, y0+8, armW, 8, skinShade)
	}
	fillArm(40, 16)
	fillArm(32, 48)
	return img
}

func errResp(status int, msg string) sdk.RuntimeResponse {
	return sdk.RuntimeResponse{Status: status, Body: mustJSON(map[string]string{"error": msg})}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}
