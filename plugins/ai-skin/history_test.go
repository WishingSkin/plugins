package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

func TestSevenDayHistoryPersistenceSaveDeleteAndIsolation(t *testing.T) {
	resetJobManagerForTest()

	var uploadedName string
	hostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/site":
			_ = json.NewEncoder(w).Encode(map[string]any{"pointsEnabled": false})
		case r.URL.Path == "/v1/users/7/summary":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "score": "100"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/users/7/textures":
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			uploadedName = body.Name
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "tex-from-history",
				"fileHash":    strings.Repeat("a", 64),
				"name":        body.Name,
				"reviewState": "approved",
			})
		default:
			w.WriteHeader(404)
		}
	}))
	defer hostSrv.Close()
	t.Setenv("WS_HOST_API_URL", hostSrv.URL)
	t.Setenv("WS_HOST_API_TOKEN", "hist-token")

	settings := map[string]any{
		"allow_local_fallback": true,
		"daily_limit":          float64(5),
		"cooldown_seconds":     float64(0),
	}

	genResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "POST",
		Path:      "/generate",
		Principal: sdk.Principal{ID: 7},
		Settings:  settings,
		Body:      mustJSON(map[string]any{"prompt": "银发蓝瞳少年穿白色科技卫衣", "engine": "local_demo", "model": "slim"}),
	})
	grt := genResp.(sdk.RuntimeResponse)
	if grt.Status != 202 {
		t.Fatalf("generate failed: %+v", grt)
	}
	var started struct {
		JobID string `json:"jobId"`
	}
	_ = json.Unmarshal(grt.Body, &started)

	// Poll until job succeeds and capture persistence writes.
	data := map[string]json.RawMessage{}
	var jobOut aiJob
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
			Kind:      "route",
			Method:    "GET",
			Path:      "/jobs",
			Query:     "id=" + started.JobID,
			Principal: sdk.Principal{ID: 7},
			Settings:  settings,
		})
		jrt := jResp.(sdk.RuntimeResponse)
		for k, v := range jrt.Writes {
			data[k] = v
		}
		_ = json.Unmarshal(jrt.Body, &jobOut)
		if jobOut.Status == "succeeded" || jobOut.Status == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if jobOut.Status != "succeeded" {
		t.Fatalf("expected succeeded job, got %+v", jobOut)
	}
	if len(data["hist_idx:7"]) == 0 || len(data["hist_item:"+started.JobID]) == 0 {
		t.Fatalf("expected hist_idx and hist_item writes, got keys: %v", data)
	}
	if len(data["hist_item:"+started.JobID]) > 32<<10 {
		t.Fatalf("persisted hist_item unexpectedly large: %d bytes", len(data["hist_item:"+started.JobID]))
	}

	// Simulate plugin restart (empty in-memory manager, hydrated from plugin_data only).
	resetJobManagerForTest()

	histResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "GET",
		Path:      "/history",
		Principal: sdk.Principal{ID: 7},
		Settings:  settings,
		Data:      data,
	})
	hrt := histResp.(sdk.RuntimeResponse)
	if hrt.Status != 200 {
		t.Fatalf("GET /history failed: %+v", hrt)
	}
	var histOut struct {
		Items []aiHistoryView `json:"items"`
	}
	if err := json.Unmarshal(hrt.Body, &histOut); err != nil || len(histOut.Items) != 1 || histOut.Items[0].JobID != started.JobID || histOut.Items[0].FrontPreviewBase64 == "" {
		t.Fatalf("expected 1 hydrated history item with preview, got err=%v body=%s", err, hrt.Body)
	}

	// Cross-user isolation: another non-admin user cannot view, save, or delete user 7's history.
	otherHist, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "GET",
		Path:      "/history",
		Principal: sdk.Principal{ID: 8},
		Settings:  settings,
		Data:      data,
	})
	if !strings.Contains(string(otherHist.(sdk.RuntimeResponse).Body), `"items":[]`) {
		t.Fatalf("user 8 should not see user 7 history: %s", otherHist.(sdk.RuntimeResponse).Body)
	}

	otherSave, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "POST",
		Path:      "/save",
		Principal: sdk.Principal{ID: 8},
		Settings:  settings,
		Data:      data,
		Body:      mustJSON(map[string]any{"jobId": started.JobID, "name": "Stolen"}),
	})
	if otherSave.(sdk.RuntimeResponse).Status != 403 {
		t.Fatalf("expected 403 on cross-user save from history, got %+v", otherSave)
	}

	otherDel, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "POST",
		Path:      "/history/delete",
		Principal: sdk.Principal{ID: 8},
		Settings:  settings,
		Data:      data,
		Body:      mustJSON(map[string]any{"jobId": started.JobID}),
	})
	if otherDel.(sdk.RuntimeResponse).Status != 403 {
		t.Fatalf("expected 403 on cross-user delete, got %+v", otherDel)
	}

	// Owner can save from persisted history after restart (no active m.jobs entry).
	saveResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "POST",
		Path:      "/save",
		Principal: sdk.Principal{ID: 7},
		Settings:  settings,
		Data:      data,
		Body:      mustJSON(map[string]any{"jobId": started.JobID, "name": "RestoredSkin", "model": "slim"}),
	})
	if saveResp.(sdk.RuntimeResponse).Status != 200 || uploadedName != "RestoredSkin" {
		t.Fatalf("expected save from persisted history to succeed, got %+v uploaded=%q", saveResp, uploadedName)
	}

	// Owner deletes history entry and receives null tombstone write.
	delResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "POST",
		Path:      "/history/delete",
		Principal: sdk.Principal{ID: 7},
		Settings:  settings,
		Data:      data,
		Body:      mustJSON(map[string]any{"jobId": started.JobID}),
	})
	drt := delResp.(sdk.RuntimeResponse)
	if drt.Status != 200 || string(drt.Writes["hist_item:"+started.JobID]) != "null" || string(drt.Writes["hist_idx:7"]) != "[]" {
		t.Fatalf("unexpected delete writes: status=%d writes=%v", drt.Status, drt.Writes)
	}
}

func makeSampleRefPNGBase64() string {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.SetNRGBA(x, y, color.NRGBA{40, 130, 220, 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestParseAIConfigTimeoutDefaultAndClamping(t *testing.T) {
	if got := parseAIConfig(nil).Timeout; got != 90*time.Second {
		t.Fatalf("nil settings Timeout = %v, want 90s", got)
	}
	if got := parseAIConfig(map[string]any{"timeout_seconds": float64(0)}).Timeout; got != 90*time.Second {
		t.Fatalf("zero timeout_seconds = %v, want 90s", got)
	}
	if got := parseAIConfig(map[string]any{"timeout_seconds": float64(5)}).Timeout; got != 15*time.Second {
		t.Fatalf("small timeout_seconds = %v, want 15s", got)
	}
	if got := parseAIConfig(map[string]any{"timeout_seconds": float64(180)}).Timeout; got != 180*time.Second {
		t.Fatalf("explicit timeout_seconds = %v, want 180s", got)
	}
	if got := parseAIConfig(map[string]any{"timeout_seconds": float64(999)}).Timeout; got != 240*time.Second {
		t.Fatalf("large timeout_seconds = %v, want 240s", got)
	}
}

func TestMultimodalReferenceImageVisionLLM(t *testing.T) {
	resetJobManagerForTest()

	var sawVisionPart atomic.Bool
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		_ = json.NewDecoder(r.Body).Decode(&raw)
		if msgs, ok := raw["messages"].([]any); ok {
			for _, m := range msgs {
				mm, _ := m.(map[string]any)
				if mm["role"] == "user" {
					if parts, ok := mm["content"].([]any); ok {
						for _, p := range parts {
							pm, _ := p.(map[string]any)
							if pm["type"] == "image_url" {
								sawVisionPart.Store(true)
							}
						}
					}
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": `{"summary":"蓝衣参考少女","model":"slim","skinTone":"#F5CBA7","hairColor":"#1E293B","hairStyle":"long","eyeColor":"#2563EB","eyeStyle":"anime","outfitStyle":"coat","topPrimary":"#2563EB","topSecondary":"#1D4ED8","topAccent":"#93C5FD","bottomStyle":"skirt","sleeveStyle":"long","bottomPrimary":"#0F172A","shoeColor":"#111827","accessory":"scarf","accessoryColor":"#38BDF8"}`,
				},
			}},
		})
	}))
	defer llmSrv.Close()

	settings := map[string]any{
		"default_engine":   "structured_llm",
		"llm_api_url":      llmSrv.URL + "/v1",
		"llm_api_key":      "sk-vis",
		"daily_limit":      float64(5),
		"cooldown_seconds": float64(0),
	}
	data := map[string]json.RawMessage{}
	user := sdk.Principal{ID: 11}

	badResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"prompt": "测试", "engine": "structured_llm", "referenceImageBase64": "not-valid-image"}),
	})
	if badResp.(sdk.RuntimeResponse).Status != 400 {
		t.Fatalf("expected 400 for invalid reference image, got %+v", badResp)
	}

	genResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{
			"prompt":               "还原参考图蓝衣少女",
			"engine":               "structured_llm",
			"model":                "slim",
			"referenceImageBase64": "data:image/png;base64," + makeSampleRefPNGBase64(),
		}),
	})
	grt := genResp.(sdk.RuntimeResponse)
	if grt.Status != 202 {
		t.Fatalf("expected 202, got %+v", grt)
	}
	var started struct {
		JobID string `json:"jobId"`
	}
	_ = json.Unmarshal(grt.Body, &started)
	job, _ := pollUntilTerminal(t, started.JobID, settings, data, user)
	if job.Status != "succeeded" || !sawVisionPart.Load() {
		t.Fatalf("expected vision multimodal call to succeed: status=%s err=%q sawVision=%v", job.Status, job.Error, sawVisionPart.Load())
	}
}

func TestImageAPIBaseJobRefinementRoutesToEdits(t *testing.T) {
	resetJobManagerForTest()

	var sawGenerations atomic.Bool
	var sawEditsUpload atomic.Bool
	var sawBaseSkinDimensions atomic.Bool

	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		skinBytes := make1024x1024ValidSkinWithGarbageDeadZones()
		switch {
		case strings.HasSuffix(r.URL.Path, "/images/generations"):
			sawGenerations.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(skinBytes)}},
			})
		case strings.HasSuffix(r.URL.Path, "/images/edits"):
			if err := r.ParseMultipartForm(16 << 20); err == nil {
				if fh, _, err := r.FormFile("image"); err == nil {
					defer fh.Close()
					if img, _, err := image.Decode(fh); err == nil {
						sawEditsUpload.Store(true)
						if img.Bounds().Dx() == 1024 && img.Bounds().Dy() == 1024 {
							sawBaseSkinDimensions.Store(true)
						}
					}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(skinBytes)}},
			})
		default:
			w.WriteHeader(404)
		}
	}))
	defer imgSrv.Close()

	settings := map[string]any{
		"default_engine":       "image_api",
		"image_api_url":        imgSrv.URL + "/v1",
		"image_api_key":        "sk-img",
		"allow_private_worker": true,
		"daily_limit":          float64(5),
		"cooldown_seconds":     float64(0),
	}
	data := map[string]json.RawMessage{}
	user := sdk.Principal{ID: 12}

	gen1, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"prompt": "白色机甲战士", "engine": "image_api"}),
	})
	var s1 struct {
		JobID string `json:"jobId"`
	}
	_ = json.Unmarshal(gen1.(sdk.RuntimeResponse).Body, &s1)
	j1, _ := pollUntilTerminal(t, s1.JobID, settings, data, user)
	if j1.Status != "succeeded" || !sawGenerations.Load() {
		t.Fatalf("first image_api job failed: %+v", j1)
	}

	// Simulate restart so refine resolves strictly from persisted 7-day history.
	resetJobManagerForTest()

	gen2, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"prompt": "把肩甲改成金色", "engine": "image_api", "baseJobId": s1.JobID}),
	})
	if gen2.(sdk.RuntimeResponse).Status != 202 {
		t.Fatalf("refine image_api generate failed: %+v", gen2)
	}
	var s2 struct {
		JobID string `json:"jobId"`
	}
	_ = json.Unmarshal(gen2.(sdk.RuntimeResponse).Body, &s2)
	j2, _ := pollUntilTerminal(t, s2.JobID, settings, data, user)
	if j2.Status != "succeeded" || !sawEditsUpload.Load() || !sawBaseSkinDimensions.Load() {
		t.Fatalf("expected /images/edits upload with 1024x1024 base skin: status=%s err=%q edits=%v dim=%v", j2.Status, j2.Error, sawEditsUpload.Load(), sawBaseSkinDimensions.Load())
	}
}

func TestAdminTestAsyncDryRunDoesNotChargeOrWriteHistory(t *testing.T) {
	resetJobManagerForTest()

	var spendCalls atomic.Int32
	hostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/points/spend") {
			spendCalls.Add(1)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer hostSrv.Close()
	t.Setenv("WS_HOST_API_URL", hostSrv.URL)
	t.Setenv("WS_HOST_API_TOKEN", "admin-dry")

	settings := map[string]any{
		"allow_local_fallback": true,
		"points_cost":          float64(50),
		"daily_limit":          float64(0),
	}
	data := map[string]json.RawMessage{}
	admin := sdk.Principal{ID: 1, Admin: true}

	resp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/admin/test", Principal: admin, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"engine": "local_demo", "prompt": "骑士盔甲", "async": true}),
	})
	rt := resp.(sdk.RuntimeResponse)
	if rt.Status != 202 {
		t.Fatalf("expected 202 for async admin test, got %+v", rt)
	}
	var started struct {
		JobID  string `json:"jobId"`
		DryRun bool   `json:"dryRun"`
	}
	_ = json.Unmarshal(rt.Body, &started)
	if !started.DryRun || started.JobID == "" {
		t.Fatalf("expected dryRun jobId, got %s", rt.Body)
	}

	job, jobRT := pollUntilTerminal(t, started.JobID, settings, data, admin)
	if job.Status != "succeeded" || job.PointsSpent != 0 || spendCalls.Load() != 0 {
		t.Fatalf("dryRun job should succeed with 0 points spent: job=%+v spendCalls=%d", job, spendCalls.Load())
	}
	if _, hasHist := jobRT.Writes["hist_idx:1"]; hasHist {
		t.Fatalf("dryRun admin test must not write user history index")
	}
}

func TestGlobalHistoryCapEvictsOldestAt48Items(t *testing.T) {
	resetJobManagerForTest()

	settings := map[string]any{
		"allow_local_fallback": true,
		"daily_limit":          float64(100),
		"cooldown_seconds":     float64(0),
	}
	data := map[string]json.RawMessage{}
	var allJobIDs []string

	for uid := int64(21); uid <= 25; uid++ {
		user := sdk.Principal{ID: uid}
		for i := 0; i < 10; i++ {
			resp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
				Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
				Body: mustJSON(map[string]any{"prompt": "测试角色", "engine": "local_demo"}),
			})
			rt := resp.(sdk.RuntimeResponse)
			if rt.Status != 202 {
				t.Fatalf("generate %d/%d failed: %+v", uid, i, rt)
			}
			var s struct {
				JobID string `json:"jobId"`
			}
			_ = json.Unmarshal(rt.Body, &s)
			allJobIDs = append(allJobIDs, s.JobID)
			job, _ := pollUntilTerminal(t, s.JobID, settings, data, user)
			if job.Status != "succeeded" {
				t.Fatalf("job %s failed: %+v", s.JobID, job)
			}
			if len(allJobIDs) <= 2 {
				time.Sleep(5 * time.Millisecond)
			}
		}
	}

	// Count live non-null hist_item keys persisted across all users (should be capped at maxGlobalHistoryItems = 48).
	liveItems := 0
	for k, v := range data {
		if strings.HasPrefix(k, histItemPrefix) && string(v) != "null" && len(v) > 0 {
			liveItems++
		}
	}
	if liveItems != maxGlobalHistoryItems {
		t.Fatalf("expected %d live persisted history items, got %d", maxGlobalHistoryItems, liveItems)
	}
	if string(data[histItemPrefix+allJobIDs[0]]) != "null" || string(data[histItemPrefix+allJobIDs[1]]) != "null" {
		t.Fatalf("expected oldest two items (%s, %s) to be evicted with null tombstone", allJobIDs[0], allJobIDs[1])
	}
}
