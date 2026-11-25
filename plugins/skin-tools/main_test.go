package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/WishingSkin/plugins/shared/skinpng"
	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

func buildTestSkinPNG(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{R: 200, G: 80, B: 60, A: 255})
		}
	}
	// Transparent fringe pixel for cleanAlpha check
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 6})
	// Outer hat pixel for stripOuter check
	img.SetNRGBA(40, 8, color.NRGBA{R: 10, G: 255, B: 20, A: 255})
	b, _ := skinpng.EncodePNG(img)
	return b
}

func TestSkinTransformPipelineAndHostFetch(t *testing.T) {
	legacy := buildTestSkinPNG(64, 32)
	hash := strings.Repeat("d", 64)

	hostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/textures/"+hash+"/bytes" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(legacy)
			return
		}
		w.WriteHeader(404)
	}))
	defer hostSrv.Close()
	t.Setenv("WS_HOST_API_URL", hostSrv.URL)
	t.Setenv("WS_HOST_API_TOKEN", "tools-token")

	resp, err := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "POST",
		Path:      "/transform",
		Principal: sdk.Principal{ID: 3},
		Body: mustJSON(transformRequest{
			FileHash:      hash,
			Model:         "default",
			UpgradeLegacy: true,
			ArmConvert:    "to_slim",
			Grayscale:     true,
			StripOuter:    true,
			CleanAlpha:    true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	rt := resp.(sdk.RuntimeResponse)
	if rt.Status != 200 {
		t.Fatalf("transform failed: %+v", rt)
	}

	var out struct {
		PNGBase64          string `json:"pngBase64"`
		FrontPreviewBase64 string `json:"frontPreviewBase64"`
		BackPreviewBase64  string `json:"backPreviewBase64"`
		Width              int    `json:"width"`
		Height             int    `json:"height"`
		Model              string `json:"model"`
	}
	if err := json.Unmarshal(rt.Body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Width != 64 || out.Height != 64 || out.Model != "slim" || out.FrontPreviewBase64 == "" || out.BackPreviewBase64 == "" {
		t.Fatalf("unexpected transform output metadata: width=%d height=%d model=%q", out.Width, out.Height, out.Model)
	}

	decodedBytes, err := base64.StdEncoding.DecodeString(out.PNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	decodedImg, err := skinpng.DecodePNG(decodedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if decodedImg.NRGBAAt(0, 0).A != 0 {
		t.Fatalf("expected cleanAlpha to remove fringe pixel, got %v", decodedImg.NRGBAAt(0, 0))
	}
	if decodedImg.NRGBAAt(40, 8).A != 0 {
		t.Fatalf("expected stripOuter to clear hat pixel, got %v", decodedImg.NRGBAAt(40, 8))
	}
	c := decodedImg.NRGBAAt(10, 10)
	if c.R != c.G || c.G != c.B {
		t.Fatalf("expected grayscale output pixel, got %v", c)
	}

	for _, f := range []string{"manifest.json", "workbench.html"} {
		if _, err := os.Stat(f); err != nil {
			t.Fatal(err)
		}
	}
}
