package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackPluginProducesSafeDeterministicArchive(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "plugins", "demo")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "manifest": {
    "id": "wishingskin.demo",
    "name": "Demo",
    "version": "0.1.0",
    "settingsNamespace": "wishingskin.demo",
    "migrationVersion": 0,
    "permissions": ["routes.admin", "frontend"]
  },
  "protocol": 1,
  "routes": [{"method": "GET", "path": "/status", "access": "admin"}],
  "frontend": "index.html"
}`
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "index.html"), []byte("<!doctype html><title>Demo</title>"), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeBuilder := func(_ context.Context, _, _, goos, goarch, outPath string) error {
		return os.WriteFile(outPath, []byte("binary-"+goos+"-"+goarch), 0o755)
	}

	outDir := filepath.Join(root, "dist")
	res1, err := packPlugin(context.Background(), pluginDir, outDir, []string{"linux/amd64", "windows/amd64"}, fakeBuilder)
	if err != nil {
		t.Fatalf("packPlugin failed: %v", err)
	}
	res2, err := packPlugin(context.Background(), pluginDir, outDir, []string{"linux/amd64", "windows/amd64"}, fakeBuilder)
	if err != nil {
		t.Fatalf("second packPlugin failed: %v", err)
	}
	if res1.SHA256 != res2.SHA256 || len(res1.SHA256) != 64 {
		t.Fatalf("expected deterministic sha256, got %q vs %q", res1.SHA256, res2.SHA256)
	}

	zipBytes, err := os.ReadFile(res1.File)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(zipBytes)
	if got := hex.EncodeToString(sum[:]); got != res1.SHA256 {
		t.Fatalf("sha256 mismatch: got %s want %s", got, res1.SHA256)
	}

	zr, err := zip.OpenReader(res1.File)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	entries := map[string]*zip.File{}
	for _, f := range zr.File {
		if strings.Contains(f.Name, `\`) {
			t.Fatalf("archive entry contains backslash: %q", f.Name)
		}
		entries[f.Name] = f
	}
	for _, name := range []string{
		"plugin.json",
		"index.html",
		"bin/wishingskin.demo-linux-amd64",
		"bin/wishingskin.demo-windows-amd64.exe",
	} {
		if _, ok := entries[name]; !ok {
			t.Fatalf("missing archive entry %q", name)
		}
	}
	if mode := entries["bin/wishingskin.demo-linux-amd64"].Mode().Perm(); mode != 0o755 {
		t.Fatalf("expected 0755 mode on linux binary, got %o", mode)
	}

	rc, err := entries["plugin.json"].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	var parsed PackageManifest
	if err := json.NewDecoder(rc).Decode(&parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Executables["linux/amd64"] != "bin/wishingskin.demo-linux-amd64" ||
		parsed.Executables["windows/amd64"] != "bin/wishingskin.demo-windows-amd64.exe" {
		t.Fatalf("unexpected executables map: %#v", parsed.Executables)
	}
}

func TestPackPluginRejectsUndeclaredPermissions(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "plugins", "bad")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "manifest": {
    "id": "wishingskin.bad",
    "version": "0.1.0",
    "settingsNamespace": "wishingskin.bad",
    "permissions": []
  },
  "protocol": 1,
  "routes": [{"method": "GET", "path": "/ping", "access": "admin"}]
}`
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := packPlugin(context.Background(), pluginDir, filepath.Join(root, "dist"), []string{"linux/amd64"}, func(context.Context, string, string, string, string, string) error {
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "routes.admin") {
		t.Fatalf("expected missing permission error, got %v", err)
	}
}

func TestUpsertCatalogSortsAndUpdatesEntries(t *testing.T) {
	root := t.TempDir()
	catalogPath := filepath.Join(root, "catalog", "catalog.json")
	digestA := strings.Repeat("a", 64)
	digestB := strings.Repeat("b", 64)
	digestA2 := strings.Repeat("c", 64)

	if err := upsertCatalog(catalogPath, CatalogEntry{
		ID: "wishingskin.skin-editor", Name: "Skin Editor", Version: "0.1.0",
		URL: "https://github.com/WishingSkin/plugins/releases/download/skin-editor-v0.1.0/skin-editor.zip", SHA256: digestB,
	}); err != nil {
		t.Fatal(err)
	}
	if err := upsertCatalog(catalogPath, CatalogEntry{
		ID: "wishingskin.moderation", Name: "Moderation", Version: "0.1.0",
		URL: "https://github.com/WishingSkin/plugins/releases/download/moderation-v0.1.0/moderation.zip", SHA256: digestA,
	}); err != nil {
		t.Fatal(err)
	}
	if err := upsertCatalog(catalogPath, CatalogEntry{
		ID: "wishingskin.moderation", Name: "Moderation", Version: "0.1.1",
		URL: "https://github.com/WishingSkin/plugins/releases/download/moderation-v0.1.1/moderation.zip", SHA256: digestA2,
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var cat MarketCatalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		t.Fatal(err)
	}
	if len(cat.Packages) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(cat.Packages))
	}
	if cat.Packages[0].ID != "wishingskin.moderation" || cat.Packages[0].Version != "0.1.1" || cat.Packages[0].SHA256 != digestA2 {
		t.Fatalf("unexpected first package: %#v", cat.Packages[0])
	}
	if cat.Packages[1].ID != "wishingskin.skin-editor" {
		t.Fatalf("unexpected second package: %#v", cat.Packages[1])
	}

	if err := upsertCatalog(catalogPath, CatalogEntry{
		ID: "wishingskin.bad", Version: "0.1.0", URL: "http://example.com/bad.zip", SHA256: digestA,
	}); err == nil {
		t.Fatal("expected non-https non-loopback catalog URL to fail")
	}
}
