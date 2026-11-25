package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

func TestEventDeliveryFormatsFilteringAndLogs(t *testing.T) {
	var lastSig, lastAuth string
	var lastBody []byte
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastSig = r.Header.Get("X-WishingSkin-Signature")
		lastAuth = r.Header.Get("Authorization")
		lastBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer sink.Close()

	// 1. Generic webhook with HMAC signature + Host API user summary enrichment.
	hostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/users/5/summary" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 5, "nickname": "CraftUser", "verifiedMinecraft": true, "mcName": "CraftMC", "mcUuid": "abc",
			})
			return
		}
		w.WriteHeader(404)
	}))
	defer hostSrv.Close()
	t.Setenv("WS_HOST_API_URL", hostSrv.URL)
	t.Setenv("WS_HOST_API_TOKEN", "sync-token")

	settings := map[string]any{
		"target_type":    "generic",
		"webhook_url":    sink.URL,
		"webhook_secret": "hmac-secret-123",
		"enabled_events": "character.create, minecraft.link",
	}
	data := map[string]json.RawMessage{}

	// Filtered-out event skips delivery.
	skipResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "event", Settings: settings, Data: data,
		Event: &sdk.Event{Name: "character.delete", UserID: 5, ResourceID: "char-1"},
	})
	if !strings.Contains(string(skipResp.(sdk.RuntimeResponse).Body), `"skipped":true`) {
		t.Fatalf("expected skipped event, got %+v", skipResp)
	}

	// Enabled event delivers with HMAC signature and user metadata.
	evResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "event", Settings: settings, Data: data,
		Event: &sdk.Event{Name: "character.create", UserID: 5, ResourceID: "char-2"},
	})
	mac := hmac.New(sha256.New, []byte("hmac-secret-123"))
	_, _ = mac.Write(lastBody)
	expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if lastSig != expectedSig || !strings.Contains(string(lastBody), `"CraftMC"`) {
		t.Fatalf("unexpected generic webhook delivery: sig=%q want=%q body=%s", lastSig, expectedSig, lastBody)
	}
	for k, v := range evResp.(sdk.RuntimeResponse).Writes {
		data[k] = v
	}

	// 2. Discord embed delivery.
	settings["target_type"] = "discord"
	_, _ = handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "event", Settings: settings,
		Event: &sdk.Event{Name: "minecraft.link", UserID: 5, ResourceID: "uuid-9"},
	})
	if !strings.Contains(string(lastBody), `"embeds"`) || !strings.Contains(string(lastBody), `"WishingSkin"`) {
		t.Fatalf("expected discord embeds payload, got %s", lastBody)
	}

	// 3. KOOK Bot token + channel target_id.
	settings["target_type"] = "kook"
	settings["kook_channel_id"] = "chan-888"
	_, _ = handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "event", Settings: settings,
		Event: &sdk.Event{Name: "minecraft.link", UserID: 5, ResourceID: "uuid-9"},
	})
	if lastAuth != "Bot hmac-secret-123" || !strings.Contains(string(lastBody), `"target_id":"chan-888"`) {
		t.Fatalf("expected kook bot delivery, got auth=%q body=%s", lastAuth, lastBody)
	}

	// 4. Test notification and clear logs + ring buffer cap at 50.
	for i := range 55 {
		res, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
			Kind: "event", Settings: map[string]any{"target_type": "generic", "webhook_url": sink.URL}, Data: data,
			Event: &sdk.Event{Name: "character.create", UserID: int64(i + 1), ResourceID: "c"},
		})
		for k, v := range res.(sdk.RuntimeResponse).Writes {
			data[k] = v
		}
	}
	_, logs := loadSyncState(data)
	if len(logs) != maxSyncLogs {
		t.Fatalf("expected 50 capped logs, got %d", len(logs))
	}

	clrResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/clear-logs", Principal: sdk.Principal{ID: 1, Admin: true}, Data: data,
	})
	for k, v := range clrResp.(sdk.RuntimeResponse).Writes {
		data[k] = v
	}
	stats, logs := loadSyncState(data)
	if stats.Sent != 0 || len(logs) != 0 {
		t.Fatalf("expected logs cleared, got stats=%+v len=%d", stats, len(logs))
	}
}

func TestServerQueryEndpointAuthAndSanitization(t *testing.T) {
	hostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/characters/by-name":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 7, "uuid": "u7", "name": "Steve", "model": "default", "verifiedMinecraft": true, "mcName": "SteveMC",
			})
		case r.URL.Path == "/v1/users/5/summary":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 5, "nickname": "Builder", "banned": false, "verifiedMinecraft": true, "mcName": "BuilderMC", "mcUuid": "m5",
			})
		default:
			w.WriteHeader(404)
		}
	}))
	defer hostSrv.Close()
	t.Setenv("WS_HOST_API_URL", hostSrv.URL)
	t.Setenv("WS_HOST_API_TOKEN", "sync-token")

	// Missing server_api_key -> 503
	unconf, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/query", Body: mustJSON(map[string]string{"apiKey": "x", "name": "Steve"}),
	})
	if unconf.(sdk.RuntimeResponse).Status != 503 {
		t.Fatalf("expected 503 when server_api_key unconfigured, got %+v", unconf)
	}

	settings := map[string]any{"server_api_key": "correct-key-0123456789"}
	badKey, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/query", Settings: settings, Body: mustJSON(map[string]string{"apiKey": "wrong", "name": "Steve"}),
	})
	if badKey.(sdk.RuntimeResponse).Status != 401 {
		t.Fatalf("expected 401 on wrong apiKey, got %+v", badKey)
	}

	okChar, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/query", Settings: settings, Body: mustJSON(map[string]string{"apiKey": "correct-key-0123456789", "name": "Steve"}),
	})
	if okChar.(sdk.RuntimeResponse).Status != 200 || !strings.Contains(string(okChar.(sdk.RuntimeResponse).Body), `"SteveMC"`) {
		t.Fatalf("expected 200 character query, got %+v", okChar)
	}

	okUser, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind: "route", Method: "POST", Path: "/query", Settings: settings, Body: mustJSON(map[string]any{"apiKey": "correct-key-0123456789", "userId": 5}),
	})
	ub := string(okUser.(sdk.RuntimeResponse).Body)
	if okUser.(sdk.RuntimeResponse).Status != 200 || !strings.Contains(ub, `"BuilderMC"`) || strings.Contains(ub, `"email"`) {
		t.Fatalf("unexpected sanitized user query output: status=%d body=%s", okUser.(sdk.RuntimeResponse).Status, ub)
	}

	for _, f := range []string{"manifest.json", "admin.html"} {
		if _, err := os.Stat(f); err != nil {
			t.Fatal(err)
		}
	}
}
