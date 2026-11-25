package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
