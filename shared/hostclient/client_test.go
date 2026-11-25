package hostclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientEndpointsAndErrors(t *testing.T) {
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/site":
			_ = json.NewEncoder(w).Encode(SiteInfo{Name: "WishingSkin", PublicURL: "https://skin.example", PointsEnabled: true})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/characters/by-name":
			if r.URL.Query().Get("name") != "Steve" {
				t.Fatalf("unexpected character query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(CharacterInfo{ID: 7, Name: "Steve", Model: "default"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/textures/abcd/bytes":
			if r.URL.Query().Get("userId") != "5" {
				t.Fatalf("expected userId=5, got %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte{0x89, 0x50, 0x4e, 0x47})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/users/5/points/spend":
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "积分不足，无法完成操作"})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/users/5/characters/7/textures/skin":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	site, err := c.Site(ctx)
	if err != nil || site.Name != "WishingSkin" || !site.PointsEnabled {
		t.Fatalf("Site failed: %+v err=%v", site, err)
	}
	if seenAuth != "Bearer secret-token" {
		t.Fatalf("unexpected Authorization header: %q", seenAuth)
	}
	ch, err := c.CharacterByName(ctx, "Steve")
	if err != nil || ch.ID != 7 {
		t.Fatalf("CharacterByName failed: %+v err=%v", ch, err)
	}
	raw, err := c.TextureBytes(ctx, "abcd", 5)
	if err != nil || len(raw) != 4 || raw[0] != 0x89 {
		t.Fatalf("TextureBytes failed: %v %x", err, raw)
	}
	if err := c.BindCharacterTexture(ctx, 5, 7, "skin", "00000000000000000000000000000001", "slim"); err != nil {
		t.Fatalf("BindCharacterTexture failed: %v", err)
	}
	_, err = c.SpendPoints(ctx, 5, 10, "ref-1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 409 || !strings.Contains(apiErr.Message, "积分不足") {
		t.Fatalf("expected 409 APIError, got %v", err)
	}
}
