package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WishingSkin/plugins/shared/skinpng"
	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

func pollUntilTerminal(t *testing.T, jobID string, settings map[string]any, data map[string]json.RawMessage, principal sdk.Principal) (aiJob, sdk.RuntimeResponse) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := handleRequest(context.Background(), sdk.RuntimeRequest{
			Kind:      "route",
			Method:    http.MethodGet,
			Path:      "/jobs",
			Query:     "id=" + jobID,
			Principal: principal,
			Settings:  settings,
			Data:      data,
		})
		if err != nil {
			t.Fatalf("GET /jobs error: %v", err)
		}
		rt := resp.(sdk.RuntimeResponse)
		if rt.Status != 200 {
			t.Fatalf("GET /jobs unexpected status: %+v", rt)
		}
		for k, v := range rt.Writes {
			data[k] = v
		}
		var job aiJob
		if err := json.Unmarshal(rt.Body, &job); err != nil {
			t.Fatalf("unmarshal job: %v", err)
		}
		if job.Status == "succeeded" || job.Status == "failed" {
			return job, rt
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out polling job %s", jobID)
	return aiJob{}, sdk.RuntimeResponse{}
}

func make1024x1024ValidSkinWithGarbageDeadZones() []byte {
	base := RenderRecipe(NormalizeRecipe(SkinRecipe{
		SkinTone:    "#ECC0A0",
		HairColor:   "#2F4F8F",
		EyeColor:    "#22D3EE",
		TopPrimary:  "#3898BC",
		OutfitStyle: "hoodie",
		Accessory:   "headphones",
	}, "default"))
	big := skinpng.ScaleNearest(base, 1024, 1024)
	// Paint bright magenta in unused top-left dead zone (0..7, 0..7) -> (0..127, 0..127)
	magenta := color.NRGBA{255, 0, 255, 255}
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			big.SetNRGBA(x, y, magenta)
		}
	}
	b, _ := skinpng.EncodePNG(big)
	return b
}

func TestStructuredLLMPrecheckAsyncPollSpendAndSave(t *testing.T) {
	resetJobManagerForTest()

	var pointsEnabled atomic.Bool
	pointsEnabled.Store(false)
	var userScore atomic.Int64
	userScore.Store(10)
	var llmCalls atomic.Int32
	var spendCalls atomic.Int32
	var lastSpendRef string
	var sawRefinePrompt atomic.Bool
	var uploadedName string
	var boundChar string

	hostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/site":
			_ = json.NewEncoder(w).Encode(map[string]any{"pointsEnabled": pointsEnabled.Load()})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/users/7/summary":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "score": formatInt64(userScore.Load())})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/users/7/points/spend":
			var body struct {
				Amount      int64  `json:"amount"`
				ReferenceID string `json:"referenceId"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			spendCalls.Add(1)
			lastSpendRef = body.ReferenceID
			userScore.Add(-body.Amount)
			_ = json.NewEncoder(w).Encode(map[string]any{"balance": formatInt64(userScore.Load()), "deducted": true})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/users/7/textures":
			var body struct {
				Name     string `json:"name"`
				IsPublic bool   `json:"isPublic"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			uploadedName = body.Name
			if body.IsPublic {
				t.Errorf("allow_public_save=false must suppress isPublic")
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          strings.Repeat("d", 32),
				"fileHash":    strings.Repeat("e", 64),
				"name":        body.Name,
				"reviewState": "approved",
			})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/users/7/characters/12/textures/skin":
			boundChar = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer hostSrv.Close()

	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCalls.Add(1)
		var reqBody struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		for _, m := range reqBody.Messages {
			if strings.Contains(m.Content, "Refine the existing recipe") {
				sawRefinePrompt.Store(true)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": "```json\n{\"summary\":\"银发蓝瞳科技少年\",\"model\":\"slim\",\"skinTone\":\"#ECC0A0\",\"hairColor\":\"#E5E7EB\",\"hairStyle\":\"ponytail\",\"eyeColor\":\"#2563EB\",\"eyeStyle\":\"anime\",\"outfitStyle\":\"hoodie\",\"topPrimary\":\"#1E293B\",\"topSecondary\":\"#0F172A\",\"topAccent\":\"#38BDF8\",\"bottomPrimary\":\"#334155\",\"shoeColor\":\"#111827\",\"accessory\":\"headphones\",\"accessoryColor\":\"#38BDF8\"}\n```",
				},
			}},
		})
	}))
	defer llmSrv.Close()

	t.Setenv("WS_HOST_API_URL", hostSrv.URL)
	t.Setenv("WS_HOST_API_TOKEN", "host-tok")

	settings := map[string]any{
		"default_engine":    "structured_llm",
		"llm_api_url":       llmSrv.URL + "/v1",
		"llm_api_key":       "sk-test",
		"points_cost":       float64(25),
		"daily_limit":       float64(5),
		"cooldown_seconds":  float64(0),
		"allow_public_save": false,
	}
	data := map[string]json.RawMessage{}
	user := sdk.Principal{ID: 7}

	// 1. Points disabled -> rejected immediately without calling LLM.
	resp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"prompt": "银发蓝瞳", "model": "slim"}),
	})
	if resp.(sdk.RuntimeResponse).Status != 409 || llmCalls.Load() != 0 {
		t.Fatalf("expected 409 with 0 LLM calls when points disabled, got %+v calls=%d", resp, llmCalls.Load())
	}

	// 2. Insufficient balance (10 < 25) -> rejected without calling LLM.
	pointsEnabled.Store(true)
	resp, _ = handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"prompt": "银发蓝瞳", "model": "slim"}),
	})
	if resp.(sdk.RuntimeResponse).Status != 409 || llmCalls.Load() != 0 {
		t.Fatalf("expected 409 with 0 LLM calls on low balance, got %+v calls=%d", resp, llmCalls.Load())
	}

	// 3. Sufficient balance -> 202 async job -> poll -> spend points once -> Writes flushed -> valid UV PNG.
	userScore.Store(100)
	genResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"prompt": "银发蓝瞳科技少年", "model": "slim"}),
	})
	grt := genResp.(sdk.RuntimeResponse)
	if grt.Status != 202 {
		t.Fatalf("expected 202 queued response, got %+v", grt)
	}
	var started struct {
		JobID string `json:"jobId"`
	}
	_ = json.Unmarshal(grt.Body, &started)

	// Other non-admin user must receive 403 on job poll.
	forbiddenResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "GET", Path: "/jobs", Query: "id=" + started.JobID,
		Principal: sdk.Principal{ID: 99}, Settings: settings, Data: data,
	})
	if forbiddenResp.(sdk.RuntimeResponse).Status != 403 {
		t.Fatalf("expected 403 for other user job access, got %+v", forbiddenResp)
	}

	job, rt := pollUntilTerminal(t, started.JobID, settings, data, user)
	if job.Status != "succeeded" || spendCalls.Load() != 1 || lastSpendRef != "aiskin-"+started.JobID {
		t.Fatalf("unexpected job completion: job=%+v spendCalls=%d ref=%q", job, spendCalls.Load(), lastSpendRef)
	}
	if len(rt.Writes["daily_usage"]) == 0 || len(rt.Writes["stats"]) == 0 || len(rt.Writes["audit_log"]) == 0 {
		t.Fatalf("expected persistence Writes on terminal job poll, got %+v", rt.Writes)
	}
	pngRaw, err := base64.StdEncoding.DecodeString(job.PNGBase64)
	if err != nil {
		t.Fatalf("invalid pngBase64: %v", err)
	}
	skinImg, err := skinpng.DecodePNG(pngRaw)
	if err != nil || skinImg.Bounds().Dx() != 64 || skinImg.Bounds().Dy() != 64 {
		t.Fatalf("decoded skin invalid: %v bounds=%v", err, skinImg.Bounds())
	}
	if skinImg.NRGBAAt(0, 0).A != 0 {
		t.Fatalf("dead zone pixel (0,0) should be transparent")
	}

	// 4. Save & equip character.
	saveResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/save", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"jobId": started.JobID, "name": "TechBoy", "model": "slim", "isPublic": true, "characterId": 12}),
	})
	srt := saveResp.(sdk.RuntimeResponse)
	if srt.Status != 200 || uploadedName != "TechBoy" || boundChar == "" || !strings.Contains(string(srt.Body), `"bound":true`) {
		t.Fatalf("save failed: resp=%+v uploaded=%q bound=%q", srt, uploadedName, boundChar)
	}

	// 5. Refine using baseRecipe.
	gen2Resp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"prompt": "把耳机换成皇冠", "model": "slim", "baseRecipe": job.Recipe}),
	})
	var started2 struct{ JobID string `json:"jobId"` }
	_ = json.Unmarshal(gen2Resp.(sdk.RuntimeResponse).Body, &started2)
	job2, _ := pollUntilTerminal(t, started2.JobID, settings, data, user)
	if job2.Status != "succeeded" || !sawRefinePrompt.Load() {
		t.Fatalf("expected refine job to pass baseRecipe prompt to LLM: status=%s saw=%v", job2.Status, sawRefinePrompt.Load())
	}
}

func TestImageAPIUVValidationAndBlankRejection(t *testing.T) {
	resetJobManagerForTest()

	var spendCalls atomic.Int32
	var returnBlank atomic.Bool
	returnBlank.Store(true)

	hostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/site":
			_ = json.NewEncoder(w).Encode(map[string]any{"pointsEnabled": true})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/users/5/summary":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 5, "score": "100"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/users/5/points/spend":
			spendCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"balance": "80", "deducted": true})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer hostSrv.Close()

	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		if returnBlank.Load() {
			blank := image.NewNRGBA(image.Rect(0, 0, 64, 64))
			raw, _ = skinpng.EncodePNG(blank)
		} else {
			raw = make1024x1024ValidSkinWithGarbageDeadZones()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(raw)}},
		})
	}))
	defer imgSrv.Close()

	t.Setenv("WS_HOST_API_URL", hostSrv.URL)
	t.Setenv("WS_HOST_API_TOKEN", "host-tok")

	settings := map[string]any{
		"default_engine":   "image_api",
		"image_api_url":    imgSrv.URL + "/v1/images/generations",
		"image_api_key":    "img-key",
		"points_cost":      float64(20),
		"daily_limit":      float64(5),
		"cooldown_seconds": float64(0),
	}
	data := map[string]json.RawMessage{}
	user := sdk.Principal{ID: 5}

	// 1. Blank image -> rejected by UV validator with 0 points spent and daily usage rolled back to 0.
	g1, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"prompt": "knight"}),
	})
	var j1 struct{ JobID string `json:"jobId"` }
	_ = json.Unmarshal(g1.(sdk.RuntimeResponse).Body, &j1)
	res1, _ := pollUntilTerminal(t, j1.JobID, settings, data, user)
	if res1.Status != "failed" || spendCalls.Load() != 0 || res1.PNGBase64 != "" {
		t.Fatalf("blank skin should fail without charging: %+v spend=%d", res1, spendCalls.Load())
	}
	if cnt := manager.userDailyCount(utcDateKey(time.Now()), 5); cnt != 0 {
		t.Fatalf("expected failed job daily count rollback to 0, got %d", cnt)
	}

	// 2. Valid 1024x1024 skin with dead-zone magenta -> downsampled to 64x64, dead zones stripped, charged once.
	returnBlank.Store(false)
	g2, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"prompt": "blue knight"}),
	})
	var j2 struct{ JobID string `json:"jobId"` }
	_ = json.Unmarshal(g2.(sdk.RuntimeResponse).Body, &j2)
	res2, _ := pollUntilTerminal(t, j2.JobID, settings, data, user)
	if res2.Status != "succeeded" || spendCalls.Load() != 1 {
		t.Fatalf("valid 1024x1024 skin failed: %+v spend=%d", res2, spendCalls.Load())
	}
	rawOut, _ := base64.StdEncoding.DecodeString(res2.PNGBase64)
	imgOut, _ := skinpng.DecodePNG(rawOut)
	if imgOut.Bounds().Dx() != 64 || imgOut.Bounds().Dy() != 64 || imgOut.NRGBAAt(0, 0).A != 0 {
		t.Fatalf("expected 64x64 sanitized skin with cleared dead zone (0,0), got bounds=%v pixel=%v", imgOut.Bounds(), imgOut.NRGBAAt(0, 0))
	}
}

func TestAsyncWorkerPollingAndSSRFProtection(t *testing.T) {
	resetJobManagerForTest()

	// SSRF checks
	if _, err := newSafeAIClient("http://169.254.169.254/latest/meta-data", true, 5*time.Second); err == nil {
		t.Fatalf("cloud metadata IP 169.254.169.254 must ALWAYS be blocked even with allowPrivate=true")
	}
	if _, err := newSafeAIClient("http://192.168.1.50:8000/gen", false, 5*time.Second); err == nil {
		t.Fatalf("private LAN IP must be blocked when allowPrivate=false")
	}
	if _, err := newSafeAIClient("http://192.168.1.50:8000/gen", true, 5*time.Second); err != nil {
		t.Fatalf("private LAN IP should succeed when allowPrivate=true, got: %v", err)
	}

	var pollStage atomic.Int32
	validSkin := RenderRecipe(NormalizeRecipe(SkinRecipe{
		SkinTone: "#ECC0A0", HairColor: "#3B82F6", TopPrimary: "#1D4ED8", Accessory: "crown",
	}, "default"))
	validBytes, _ := skinpng.EncodePNG(validSkin)
	validB64 := base64.StdEncoding.EncodeToString(validBytes)

	var workerURL string
	workerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer worker-sec" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/infer" {
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "w1",
				"status":  "queued",
				"pollUrl": workerURL + "/jobs/w1",
			})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/jobs/w1" {
			n := pollStage.Add(1)
			if n == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "processing"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":    "succeeded",
				"pngBase64": validB64,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer workerSrv.Close()
	workerURL = workerSrv.URL

	settings := map[string]any{
		"default_engine": "async_worker",
		"worker_url":     workerSrv.URL + "/infer",
		"worker_secret":  "worker-sec",
	}
	data := map[string]json.RawMessage{}
	user := sdk.Principal{ID: 8}

	genResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"prompt": "blue knight"}),
	})
	grt := genResp.(sdk.RuntimeResponse)
	if grt.Status != 202 {
		t.Fatalf("expected 202 from async worker start, got %+v", grt)
	}
	var started struct{ JobID string `json:"jobId"` }
	_ = json.Unmarshal(grt.Body, &started)
	job, _ := pollUntilTerminal(t, started.JobID, settings, data, user)
	if job.Status != "succeeded" || pollStage.Load() < 2 || job.PNGBase64 == "" {
		t.Fatalf("expected polled async worker completion, got %+v stage=%d", job, pollStage.Load())
	}
}

func TestConcurrentSpendRaceHidesResult(t *testing.T) {
	resetJobManagerForTest()

	hostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/site":
			_ = json.NewEncoder(w).Encode(map[string]any{"pointsEnabled": true})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/users/3/summary":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 3, "score": "50"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/users/3/points/spend":
			// Simulate user spending their balance concurrently while AI generation was running.
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "积分不足，无法完成操作"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer hostSrv.Close()

	t.Setenv("WS_HOST_API_URL", hostSrv.URL)
	t.Setenv("WS_HOST_API_TOKEN", "host-tok")

	settings := map[string]any{
		"default_engine":       "local_demo",
		"allow_local_fallback": true,
		"points_cost":          float64(30),
		"cooldown_seconds":     float64(0),
	}
	data := map[string]json.RawMessage{}
	user := sdk.Principal{ID: 3}

	genResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: user, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"prompt": "红色连帽衫"}),
	})
	var started struct{ JobID string `json:"jobId"` }
	_ = json.Unmarshal(genResp.(sdk.RuntimeResponse).Body, &started)
	job, _ := pollUntilTerminal(t, started.JobID, settings, data, user)
	if job.Status != "failed" || job.PNGBase64 != "" || job.FrontPreviewBase64 != "" || job.BackPreviewBase64 != "" {
		t.Fatalf("expected failed job with NO leaked png/previews on 409 spend race, got %+v", job)
	}
}

func TestLocalDemoGuardDailyLimitAndAdminRoutes(t *testing.T) {
	resetJobManagerForTest()
	today := utcDateKey(time.Now())

	// 1. local_demo disabled by default -> 503.
	resp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: sdk.Principal{ID: 4},
		Settings: map[string]any{"allow_local_fallback": false},
		Body:     mustJSON(map[string]any{"prompt": "knight", "engine": "local_demo"}),
	})
	if resp.(sdk.RuntimeResponse).Status != 503 {
		t.Fatalf("expected 503 when local_demo disabled, got %+v", resp)
	}

	// 2. Daily limit hydrated from Data blocks 3rd non-admin request (429).
	data := map[string]json.RawMessage{
		"daily_usage": mustJSON(map[string]map[string]int{today: {"4": 2}}),
	}
	settings := map[string]any{"allow_local_fallback": true, "daily_limit": float64(2), "cooldown_seconds": float64(0)}
	limResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/generate", Principal: sdk.Principal{ID: 4},
		Settings: settings, Data: data, Body: mustJSON(map[string]any{"prompt": "knight"}),
	})
	if limResp.(sdk.RuntimeResponse).Status != 429 {
		t.Fatalf("expected 429 on daily quota exhausted, got %+v", limResp)
	}

	// 3. Admin /admin/status, /admin/test, /admin/clear-logs.
	admin := sdk.Principal{ID: 1, Admin: true}
	stResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "GET", Path: "/admin/status", Principal: admin, Settings: settings, Data: data,
	})
	if stResp.(sdk.RuntimeResponse).Status != 200 || !strings.Contains(string(stResp.(sdk.RuntimeResponse).Body), `"defaultEngine":"local_demo"`) {
		t.Fatalf("unexpected admin status: %+v", stResp)
	}

	testResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/admin/test", Principal: admin, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{"engine": "local_demo", "prompt": "紫发猫耳连衣裙"}),
	})
	trt := testResp.(sdk.RuntimeResponse)
	if trt.Status != 200 || !strings.Contains(string(trt.Body), `"ok":true`) {
		t.Fatalf("admin test failed: %+v", trt)
	}

	clrResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/admin/clear-logs", Principal: admin, Settings: settings, Data: data,
		Body: mustJSON(map[string]any{}),
	})
	if clrResp.(sdk.RuntimeResponse).Status != 200 || string(clrResp.(sdk.RuntimeResponse).Writes["audit_log"]) != "[]" {
		t.Fatalf("clear logs failed: %+v", clrResp)
	}
}

func formatInt64(v int64) string {
	return string(mustJSON(v))
}
