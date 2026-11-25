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

func sampleSkinPNG(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{R: 120, G: 150, B: 190, A: 255})
		}
	}
	b, _ := skinpng.EncodePNG(img)
	return b
}

func TestOverlaysPreviewApplyPointsAndAdminCustomCRUD(t *testing.T) {
	basePNG := sampleSkinPNG(64, 32)
	hash := strings.Repeat("a", 64)

	var spendFail bool
	var uploadedName string
	var boundCharID string

	hostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/textures/"+hash+"/bytes":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(basePNG)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/users/9/points/spend":
			if spendFail {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "积分不足，无法完成操作"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"balance": "90", "deducted": true})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/users/9/textures":
			var body struct{ Name string `json:"name"` }
			_ = json.NewDecoder(r.Body).Decode(&body)
			uploadedName = body.Name
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          strings.Repeat("b", 32),
				"fileHash":    strings.Repeat("c", 64),
				"name":        body.Name,
				"reviewState": "approved",
			})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/users/9/characters/"):
			boundCharID = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer hostSrv.Close()

	t.Setenv("WS_HOST_API_URL", hostSrv.URL)
	t.Setenv("WS_HOST_API_TOKEN", "test-host-token")

	// 1. User /overlays lists all 6 built-in presets.
	listResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "GET",
		Path:      "/overlays",
		Principal: sdk.Principal{ID: 9},
		Settings:  map[string]any{"points_cost": float64(10), "max_layers": float64(3)},
	})
	rt := listResp.(sdk.RuntimeResponse)
	if rt.Status != 200 || !strings.Contains(string(rt.Body), "santa_hat") || !strings.Contains(string(rt.Body), "winter_scarf") {
		t.Fatalf("unexpected /overlays response: %+v", rt)
	}

	// 2. Preview upgrades 64x32 base skin to 64x64 and applies santa_hat.
	prevResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "POST",
		Path:      "/preview",
		Principal: sdk.Principal{ID: 9},
		Body:      mustJSON(map[string]any{"fileHash": hash, "layers": []layerSpec{{ID: "santa_hat", Opacity: 1.0}}}),
	})
	prt := prevResp.(sdk.RuntimeResponse)
	if prt.Status != 200 || !strings.Contains(string(prt.Body), `"width":64`) || !strings.Contains(string(prt.Body), `"height":64`) {
		t.Fatalf("preview failed: %+v", prt)
	}

	// 3. Apply when points fail (409) must NOT upload skin.
	spendFail = true
	failResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "POST",
		Path:      "/apply",
		Principal: sdk.Principal{ID: 9},
		Settings:  map[string]any{"points_cost": float64(10)},
		Body:      mustJSON(map[string]any{"fileHash": hash, "name": "BlockedSkin", "layers": []layerSpec{{ID: "santa_hat", Opacity: 1.0}}}),
	})
	if failResp.(sdk.RuntimeResponse).Status != 409 || uploadedName != "" {
		t.Fatalf("expected 409 without upload, got %+v uploaded=%q", failResp, uploadedName)
	}

	// 4. Apply succeeds when points succeed, uploads and binds character.
	spendFail = false
	okResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "POST",
		Path:      "/apply",
		Principal: sdk.Principal{ID: 9},
		Settings:  map[string]any{"points_cost": float64(10)},
		Body:      mustJSON(map[string]any{"fileHash": hash, "name": "HolidayHero", "characterId": 42, "layers": []layerSpec{{ID: "golden_crown", Opacity: 1.0}}}),
	})
	ort := okResp.(sdk.RuntimeResponse)
	if ort.Status != 200 || uploadedName != "HolidayHero" || !strings.Contains(boundCharID, "/characters/42/") {
		t.Fatalf("apply failed: resp=%+v uploaded=%q bound=%q", ort, uploadedName, boundCharID)
	}

	// 5. Admin custom overlay upload -> toggle built-in -> delete custom overlay.
	data := map[string]json.RawMessage{}
	customPNG := sampleSkinPNG(64, 64)
	addResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "POST",
		Path:      "/admin/overlays",
		Principal: sdk.Principal{ID: 1, Admin: true},
		Data:      data,
		Body:      mustJSON(map[string]any{"name": "CustomRibbon", "category": "限定", "pngBase64": base64.StdEncoding.EncodeToString(customPNG)}),
	})
	art := addResp.(sdk.RuntimeResponse)
	if art.Status != 200 {
		t.Fatalf("admin add overlay failed: %+v", art)
	}
	var addOut struct{ ID string `json:"id"` }
	_ = json.Unmarshal(art.Body, &addOut)
	for k, v := range art.Writes {
		data[k] = v
	}

	togResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "POST",
		Path:      "/admin/overlays",
		Principal: sdk.Principal{ID: 1, Admin: true},
		Data:      data,
		Body:      mustJSON(map[string]any{"action": "toggle", "id": "santa_hat", "enabled": false}),
	})
	for k, v := range togResp.(sdk.RuntimeResponse).Writes {
		data[k] = v
	}

	// Verify custom overlay works in list while santa_hat is disabled.
	listAfter, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "GET",
		Path:      "/overlays",
		Principal: sdk.Principal{ID: 9},
		Data:      data,
	})
	lBody := string(listAfter.(sdk.RuntimeResponse).Body)
	if strings.Contains(lBody, "santa_hat") || !strings.Contains(lBody, addOut.ID) {
		t.Fatalf("expected santa_hat hidden and custom %q present, got %s", addOut.ID, lBody)
	}

	delResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "POST",
		Path:      "/admin/overlays/delete",
		Principal: sdk.Principal{ID: 1, Admin: true},
		Data:      data,
		Body:      mustJSON(map[string]string{"id": addOut.ID}),
	})
	if delResp.(sdk.RuntimeResponse).Status != 200 || string(delResp.(sdk.RuntimeResponse).Writes["overlay:"+addOut.ID]) != "null" {
		t.Fatalf("delete custom overlay failed: %+v", delResp)
	}

	for _, f := range []string{"manifest.json", "studio.html", "admin.html"} {
		if _, err := os.Stat(f); err != nil {
			t.Fatal(err)
		}
	}
}
