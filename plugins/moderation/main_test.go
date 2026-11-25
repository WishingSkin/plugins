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
	"os"
	"strings"
	"testing"

	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

func makeTestPNGBase64(width, height int, drawFunc func(x, y int) color.NRGBA) string {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, drawFunc(x, y))
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestTextModerationNormalizationRegexAndModes(t *testing.T) {
	settings := map[string]any{
		"mode":          "review",
		"blocked_words": "违禁词, BadWord",
		"review_words":  "疑点词汇",
		"regex_rules":   "(?i)discord\\.gg/[a-z0-9]+\n[unclosed-regex",
	}

	// Fullwidth and punctuation obfuscation hits blocked_words.
	// In texture.upload with mode=review -> returns review decision.
	resp, err := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:     "hook",
		Settings: settings,
		Hook: &sdk.HookRequest{
			Name:   "text.check",
			Scope:  "texture.upload",
			UserID: 7,
			Fields: map[string]string{"name": "Ｂ.ａ_ｄ\u200bＷｏｒｄ Skin"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rt := resp.(sdk.RuntimeResponse)
	var d sdk.HookDecision
	_ = json.Unmarshal(rt.Body, &d)
	if d.Decision != "review" {
		t.Fatalf("expected review on mode=review texture.upload, got %+v", d)
	}

	// Non-texture scope (character.create) must reject even when mode=review.
	resp, _ = handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:     "hook",
		Settings: settings,
		Hook: &sdk.HookRequest{
			Name:   "text.check",
			Scope:  "character.create",
			UserID: 7,
			Fields: map[string]string{"name": "MyBadWord"},
		},
	})
	_ = json.Unmarshal(resp.(sdk.RuntimeResponse).Body, &d)
	if d.Decision != "reject" {
		t.Fatalf("expected reject on character.create, got %+v", d)
	}

	// Regex invite-link rule.
	resp, _ = handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:     "hook",
		Settings: settings,
		Hook: &sdk.HookRequest{
			Name:   "text.check",
			Scope:  "profile.nickname",
			Fields: map[string]string{"nickname": "Join DISCORD.GG/abc123"},
		},
	})
	_ = json.Unmarshal(resp.(sdk.RuntimeResponse).Body, &d)
	if d.Decision != "reject" {
		t.Fatalf("expected regex reject, got %+v", d)
	}

	// Admin /status reports invalid regex warnings.
	statusResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "GET",
		Path:      "/status",
		Principal: sdk.Principal{ID: 1, Admin: true},
		Settings:  settings,
	})
	if !strings.Contains(string(statusResp.(sdk.RuntimeResponse).Body), "正则语法错误") {
		t.Fatalf("expected regex warning in /status: %s", statusResp.(sdk.RuntimeResponse).Body)
	}
}

func TestImageHealthChecks(t *testing.T) {
	settings := map[string]any{
		"mode":              "block",
		"reject_blank_skin": true,
		"reject_solid_skin": true,
	}

	blank := makeTestPNGBase64(64, 64, func(x, y int) color.NRGBA { return color.NRGBA{0, 0, 0, 0} })
	resp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:     "hook",
		Settings: settings,
		Hook:     &sdk.HookRequest{Name: "texture.upload.check", Scope: "texture.upload", PNGBase64: blank},
	})
	var d sdk.HookDecision
	_ = json.Unmarshal(resp.(sdk.RuntimeResponse).Body, &d)
	if d.Decision != "reject" {
		t.Fatalf("expected reject on blank skin, got %+v", d)
	}

	solid := makeTestPNGBase64(64, 64, func(x, y int) color.NRGBA { return color.NRGBA{40, 90, 180, 255} })
	resp, _ = handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:     "hook",
		Settings: settings,
		Hook:     &sdk.HookRequest{Name: "texture.upload.check", Scope: "texture.upload", PNGBase64: solid},
	})
	_ = json.Unmarshal(resp.(sdk.RuntimeResponse).Body, &d)
	if d.Decision != "review" {
		t.Fatalf("expected review on solid skin, got %+v", d)
	}

	varied := makeTestPNGBase64(64, 64, func(x, y int) color.NRGBA {
		if x%2 == 0 {
			return color.NRGBA{40, 90, 180, 255}
		}
		return color.NRGBA{200, 120, 60, 255}
	})
	resp, _ = handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:     "hook",
		Settings: settings,
		Hook:     &sdk.HookRequest{Name: "texture.upload.check", Scope: "texture.upload", PNGBase64: varied},
	})
	_ = json.Unmarshal(resp.(sdk.RuntimeResponse).Body, &d)
	if d.Decision != "allow" {
		t.Fatalf("expected allow on healthy texture, got %+v", d)
	}
}

func TestExternalWebhookModerationAndFailPolicies(t *testing.T) {
	var seenAuth, seenImage string
	var modeResp = "reject"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		var body struct {
			PNGBase64 string `json:"pngBase64"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		seenImage = body.PNGBase64
		if modeResp == "fail" {
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"decision": modeResp,
			"reason":   "external reason",
		})
	}))
	defer srv.Close()

	settings := map[string]any{
		"webhook_url":         srv.URL,
		"webhook_secret":      "top-secret",
		"webhook_send_image":  true,
		"webhook_fail_policy": "review",
	}
	img := makeTestPNGBase64(64, 64, func(x, y int) color.NRGBA { return color.NRGBA{uint8(x * 3), uint8(y * 3), 100, 255} })

	resp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:     "hook",
		Settings: settings,
		Hook:     &sdk.HookRequest{Name: "texture.upload.check", Scope: "texture.upload", PNGBase64: img},
	})
	var d sdk.HookDecision
	_ = json.Unmarshal(resp.(sdk.RuntimeResponse).Body, &d)
	if d.Decision != "reject" || d.Reason != "external reason" || seenAuth != "Bearer top-secret" || seenImage != img {
		t.Fatalf("unexpected webhook result: dec=%+v auth=%q imgMatch=%v", d, seenAuth, seenImage == img)
	}

	modeResp = "fail"
	resp, _ = handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:     "hook",
		Settings: settings,
		Hook:     &sdk.HookRequest{Name: "texture.upload.check", Scope: "texture.upload", PNGBase64: img},
	})
	_ = json.Unmarshal(resp.(sdk.RuntimeResponse).Body, &d)
	if d.Decision != "review" {
		t.Fatalf("expected fallback review on error, got %+v", d)
	}
}

func TestAuditRingBufferDryRunTestAndClearLogs(t *testing.T) {
	settings := map[string]any{"blocked_words": "bad"}
	data := map[string]json.RawMessage{}

	for i := range 55 {
		resp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
			Kind:     "hook",
			Data:     data,
			Settings: settings,
			Hook:     &sdk.HookRequest{Name: "text.check", Scope: "character.create", UserID: int64(i + 1), Fields: map[string]string{"name": "Hero"}},
		})
		for k, v := range resp.(sdk.RuntimeResponse).Writes {
			data[k] = v
		}
	}

	// Dry-run POST /test must match blocked word without appending audit logs.
	testResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "POST",
		Path:      "/test",
		Principal: sdk.Principal{ID: 1, Admin: true},
		Data:      data,
		Settings:  settings,
		Body:      json.RawMessage(`{"scope":"character.create","text":"very bad"}`),
	})
	rt := testResp.(sdk.RuntimeResponse)
	if rt.Writes != nil || !strings.Contains(string(rt.Body), `"decision":"reject"`) {
		t.Fatalf("unexpected dry-run response: %+v", rt)
	}

	stats, logs := loadAuditState(data)
	if stats.Allowed != 55 || len(logs) != maxAuditEntries {
		t.Fatalf("expected 55 allowed and capped 50 audit entries, got stats=%+v len=%d", stats, len(logs))
	}

	// POST /clear-logs resets both.
	clrResp, _ := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "POST",
		Path:      "/clear-logs",
		Principal: sdk.Principal{ID: 1, Admin: true},
		Data:      data,
	})
	for k, v := range clrResp.(sdk.RuntimeResponse).Writes {
		data[k] = v
	}
	stats, logs = loadAuditState(data)
	if stats.Allowed != 0 || len(logs) != 0 {
		t.Fatalf("expected reset stats/logs, got stats=%+v len=%d", stats, len(logs))
	}

	// Confirm manifest.json parses and admin.html exists cleanly.
	if _, err := os.Stat("manifest.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("admin.html"); err != nil {
		t.Fatal(err)
	}
}
