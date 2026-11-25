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
	"os"
	"strings"
	"testing"

	"github.com/WishingSkin/plugins/shared/skinpng"
	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

func buildRenderTestSkin() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.SetNRGBA(x, y, color.NRGBA{40, 140, 210, 255})
		}
	}
	b, _ := skinpng.EncodePNG(img)
	return b
}

func TestAvatarBodyCardAndFallbackEndpoints(t *testing.T) {
	skinBytes := buildRenderTestSkin()
	hash := strings.Repeat("e", 64)

	hostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/site":
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "Aether <Skin>", "publicUrl": "https://skin.example"})
		case r.URL.Path == "/v1/characters/by-name":
			name := r.URL.Query().Get("name")
			if name == "Ghost" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": 2, "uuid": "00000000-0000-0000-0000-000000000002", "name": "Ghost", "model": "slim", "skinHash": "",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 1, "uuid": "12345678-1234-4234-8234-000000000001", "name": "Alice", "model": "default",
				"skinHash": hash, "verifiedMinecraft": true, "mcName": "AliceMC",
			})
		case r.URL.Path == "/v1/textures/"+hash+"/bytes":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(skinBytes)
		default:
			w.WriteHeader(404)
		}
	}))
	defer hostSrv.Close()
	t.Setenv("WS_HOST_API_URL", hostSrv.URL)
	t.Setenv("WS_HOST_API_TOKEN", "render-token")

	settings := map[string]any{"cache_max_age": float64(600), "default_theme": "emerald", "show_verified_badge": true}

	// 1. Avatar PNG with size clamping and headers.
	avResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{Kind: "route", Method: "GET", Path: "/avatar", Query: "name=Alice&size=999", Settings: settings})
	av := avResp.(sdk.RuntimeResponse)
	rawAv, _ := base64.StdEncoding.DecodeString(av.BodyBase64)
	if av.Status != 200 || av.ContentType != "image/png" || av.CacheControl != "public, max-age=600" || av.ETag == "" || !bytes.HasPrefix(rawAv, []byte{0x89, 0x50, 0x4e, 0x47}) {
		t.Fatalf("unexpected avatar response: status=%d ct=%q cc=%q etag=%q", av.Status, av.ContentType, av.CacheControl, av.ETag)
	}
	avImg, _ := skinpng.DecodePNG(rawAv)
	if avImg.Bounds().Dx() != 512 || avImg.Bounds().Dy() != 512 {
		t.Fatalf("expected size clamped to 512, got %v", avImg.Bounds())
	}

	// 2. Body PNG (front & back mannequin fallback when skinHash is empty).
	bdResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{Kind: "route", Method: "GET", Path: "/body", Query: "name=Ghost&side=back&scale=4", Settings: settings})
	bd := bdResp.(sdk.RuntimeResponse)
	rawBd, _ := base64.StdEncoding.DecodeString(bd.BodyBase64)
	bdImg, err := skinpng.DecodePNG(rawBd)
	if bd.Status != 200 || err != nil || bdImg.Bounds().Dx() != 64 || bdImg.Bounds().Dy() != 128 {
		t.Fatalf("unexpected body response: status=%d err=%v bounds=%v", bd.Status, err, bdImg.Bounds())
	}

	// 3. SVG Card with pure SVG pixel avatar rects (no <image>), normalized UUID, XML escaping, and verified badge.
	cdResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{Kind: "route", Method: "GET", Path: "/card", Query: "name=Alice&theme=dark", Settings: settings})
	cd := cdResp.(sdk.RuntimeResponse)
	svgBytes, _ := base64.StdEncoding.DecodeString(cd.BodyBase64)
	svg := string(svgBytes)
	if cd.Status != 200 || cd.ContentType != "image/svg+xml" || !strings.Contains(svg, "Aether &lt;Skin&gt;") || !strings.Contains(svg, "正版 Minecraft") || strings.Contains(svg, "<script") || strings.Contains(svg, "<image") || !strings.Contains(svg, `<rect x="30" y="34" width="9" height="9"`) || !strings.Contains(svg, "UUID: 12345678-1234-4234...") {
		t.Fatalf("unexpected card SVG: %s", svg)
	}

	// 4. User /info endpoint.
	infoResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{Kind: "route", Method: "GET", Path: "/info", Principal: sdk.Principal{ID: 1}, Settings: settings})
	if infoResp.(sdk.RuntimeResponse).Status != 200 || !strings.Contains(string(infoResp.(sdk.RuntimeResponse).Body), "https://skin.example") {
		t.Fatalf("unexpected /info response: %+v", infoResp)
	}

	for _, f := range []string{"manifest.json", "studio.html"} {
		if _, err := os.Stat(f); err != nil {
			t.Fatal(err)
		}
	}
}
