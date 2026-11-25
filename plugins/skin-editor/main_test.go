package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"github.com/WishingSkin/plugins/shared/skinpng"
	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

func TestTemplatesEndpointAndStaticAssets(t *testing.T) {
	resp, err := handleRequest(context.Background(), sdk.RuntimeRequest{
		Kind:      "route",
		Method:    "GET",
		Path:      "/templates",
		Principal: sdk.Principal{ID: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	rt := resp.(sdk.RuntimeResponse)
	if rt.Status != 200 {
		t.Fatalf("unexpected status: %d", rt.Status)
	}
	var out struct {
		Templates []skinTemplate `json:"templates"`
	}
	if err := json.Unmarshal(rt.Body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Templates) != 3 {
		t.Fatalf("expected 3 templates, got %d", len(out.Templates))
	}
	for _, tpl := range out.Templates {
		raw, err := base64.StdEncoding.DecodeString(tpl.PNGBase64)
		if err != nil {
			t.Fatalf("template %s bad base64: %v", tpl.ID, err)
		}
		img, err := skinpng.DecodePNG(raw)
		if err != nil || img.Bounds().Dx() != 64 || img.Bounds().Dy() != 64 {
			t.Fatalf("template %s invalid dimensions %v err=%v", tpl.ID, img.Bounds(), err)
		}
	}

	missing, _ := handleRequest(context.Background(), sdk.RuntimeRequest{Kind: "route", Method: "GET", Path: "/unknown"})
	if missing.(sdk.RuntimeResponse).Status != 404 {
		t.Fatalf("expected 404 on unknown route, got %+v", missing)
	}

	for _, f := range []string{"manifest.json", "editor.html"} {
		if _, err := os.Stat(f); err != nil {
			t.Fatal(err)
		}
	}
}
