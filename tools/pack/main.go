package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

const (
	maxPackageBytes   = 128 << 20
	maxPackageEntries = 256
	maxCatalogEntries = 100
)

var (
	defaultTargets = []string{
		"linux/amd64",
		"linux/arm64",
		"windows/amd64",
		"darwin/amd64",
		"darwin/arm64",
	}
	validPermissions = []string{
		"routes.public",
		"routes.user",
		"routes.admin",
		"events",
		"pages",
		"tasks",
		"frontend",
		"database",
		"webhooks",
	}
	versionPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
	routePathPattern = regexp.MustCompile(`^/(?:[A-Za-z0-9._~-]+/)*[A-Za-z0-9._~-]*$`)
	sha256Pattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Route struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Access string `json:"access"`
}

type Migration struct {
	Version  int      `json:"version"`
	MySQL    []string `json:"mysql"`
	Postgres []string `json:"postgres"`
}

type SourceManifest struct {
	Manifest   sdk.Manifest `json:"manifest"`
	Protocol   int          `json:"protocol"`
	Main       string       `json:"main,omitempty"`
	Routes     []Route      `json:"routes"`
	Frontend   string       `json:"frontend,omitempty"`
	Migrations []Migration  `json:"migrations,omitempty"`
}

type PackageManifest struct {
	Manifest    sdk.Manifest      `json:"manifest"`
	Protocol    int               `json:"protocol"`
	Executables map[string]string `json:"executables"`
	Routes      []Route           `json:"routes"`
	Frontend    string            `json:"frontend,omitempty"`
	Migrations  []Migration       `json:"migrations,omitempty"`
}

type PackResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
}

type CatalogEntry struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

type MarketCatalog struct {
	Packages []CatalogEntry `json:"packages"`
}

type binaryBuilder func(ctx context.Context, pluginDir, mainPkg, goos, goarch, outPath string) error

func main() {
	if err := run(context.Background(), os.Args[1:], defaultBinaryBuilder); err != nil {
		fmt.Fprintf(os.Stderr, "pack: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, builder binaryBuilder) error {
	if len(args) == 0 {
		return errors.New("usage: pack <pack|catalog> [flags]")
	}
	switch args[0] {
	case "pack":
		return runPack(ctx, args[1:], builder)
	case "catalog":
		return runCatalog(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func runPack(ctx context.Context, args []string, builder binaryBuilder) error {
	flags := flag.NewFlagSet("pack", flag.ContinueOnError)
	dir := flags.String("dir", "", "plugin directory containing manifest.json")
	outDir := flags.String("out", "dist", "output directory for packaged ZIP")
	targetsRaw := flags.String("targets", strings.Join(defaultTargets, ","), "comma-separated GOOS/GOARCH targets")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*dir) == "" {
		return errors.New("-dir is required")
	}
	targets := splitTargets(*targetsRaw)
	if len(targets) == 0 {
		return errors.New("at least one build target is required")
	}
	result, err := packPlugin(ctx, *dir, *outDir, targets, builder)
	if err != nil {
		return err
	}
	encoded, _ := json.Marshal(result)
	_, err = fmt.Fprintln(os.Stdout, string(encoded))
	return err
}

func runCatalog(args []string) error {
	flags := flag.NewFlagSet("catalog", flag.ContinueOnError)
	file := flags.String("file", "catalog/catalog.json", "catalog JSON path")
	id := flags.String("id", "", "plugin id")
	name := flags.String("name", "", "plugin display name")
	version := flags.String("version", "", "plugin version")
	rawURL := flags.String("url", "", "package download URL")
	digest := flags.String("sha256", "", "package SHA-256 hex digest")
	if err := flags.Parse(args); err != nil {
		return err
	}
	return upsertCatalog(*file, CatalogEntry{
		ID:      strings.TrimSpace(*id),
		Name:    strings.TrimSpace(*name),
		Version: strings.TrimSpace(*version),
		URL:     strings.TrimSpace(*rawURL),
		SHA256:  strings.ToLower(strings.TrimSpace(*digest)),
	})
}

func splitTargets(raw string) []string {
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" && !slices.Contains(out, part) {
			out = append(out, part)
		}
	}
	return out
}

func defaultBinaryBuilder(ctx context.Context, pluginDir, mainPkg, goos, goarch, outPath string) error {
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags", "-s -w", "-o", outPath, mainPkg)
	cmd.Dir = pluginDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build %s/%s failed: %w\n%s", goos, goarch, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func packPlugin(ctx context.Context, pluginDir, outDir string, targets []string, builder binaryBuilder) (PackResult, error) {
	absDir, err := filepath.Abs(pluginDir)
	if err != nil {
		return PackResult{}, err
	}
	raw, err := os.ReadFile(filepath.Join(absDir, "manifest.json"))
	if err != nil {
		return PackResult{}, fmt.Errorf("read manifest.json: %w", err)
	}
	var src SourceManifest
	if err := json.Unmarshal(raw, &src); err != nil {
		return PackResult{}, fmt.Errorf("parse manifest.json: %w", err)
	}
	normalizeSourceManifest(&src)
	if err := validateSourceManifest(src); err != nil {
		return PackResult{}, err
	}

	tempDir, err := os.MkdirTemp("", "wishingskin-pack-*")
	if err != nil {
		return PackResult{}, err
	}
	defer os.RemoveAll(tempDir)

	pkg := PackageManifest{
		Manifest:    src.Manifest,
		Protocol:    src.Protocol,
		Executables: map[string]string{},
		Routes:      src.Routes,
		Frontend:    src.Frontend,
		Migrations:  src.Migrations,
	}

	type archiveItem struct {
		name string
		mode fs.FileMode
		data []byte
	}
	var items []archiveItem

	for _, target := range targets {
		goos, goarch, ok := strings.Cut(target, "/")
		if !ok || goos == "" || goarch == "" {
			return PackResult{}, fmt.Errorf("invalid target %q", target)
		}
		ext := ""
		if goos == "windows" {
			ext = ".exe"
		}
		relBin := path.Join("bin", fmt.Sprintf("%s-%s-%s%s", src.Manifest.ID, goos, goarch, ext))
		outPath := filepath.Join(tempDir, fmt.Sprintf("%s-%s-%s%s", src.Manifest.ID, goos, goarch, ext))
		if err := builder(ctx, absDir, src.Main, goos, goarch, outPath); err != nil {
			return PackResult{}, err
		}
		binBytes, err := os.ReadFile(outPath)
		if err != nil {
			return PackResult{}, err
		}
		pkg.Executables[target] = relBin
		items = append(items, archiveItem{name: relBin, mode: 0o755, data: binBytes})
	}

	if src.Frontend != "" {
		frontendBytes, err := os.ReadFile(filepath.Join(absDir, filepath.FromSlash(src.Frontend)))
		if err != nil {
			return PackResult{}, fmt.Errorf("read frontend %q: %w", src.Frontend, err)
		}
		if len(frontendBytes) > 1<<20 {
			return PackResult{}, errors.New("frontend HTML exceeds 1 MiB")
		}
		items = append(items, archiveItem{name: src.Frontend, mode: 0o644, data: frontendBytes})
	}

	manifestBytes, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return PackResult{}, err
	}
	manifestBytes = append(manifestBytes, '\n')
	items = append(items, archiveItem{name: "plugin.json", mode: 0o644, data: manifestBytes})

	if len(items) > maxPackageEntries {
		return PackResult{}, fmt.Errorf("package has %d entries, limit is %d", len(items), maxPackageEntries)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].name < items[j].name })

	var totalUncompressed uint64
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fixedTime := time.Unix(0, 0).UTC()
	for _, item := range items {
		totalUncompressed += uint64(len(item.data))
		if totalUncompressed > maxPackageBytes {
			return PackResult{}, errors.New("uncompressed package exceeds 128 MiB")
		}
		hdr := &zip.FileHeader{
			Name:     item.name,
			Method:   zip.Deflate,
			Modified: fixedTime,
		}
		hdr.SetMode(item.mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return PackResult{}, err
		}
		if _, err := w.Write(item.data); err != nil {
			return PackResult{}, err
		}
	}
	if err := zw.Close(); err != nil {
		return PackResult{}, err
	}
	zipBytes := buf.Bytes()
	if len(zipBytes) > maxPackageBytes {
		return PackResult{}, errors.New("compressed package exceeds 128 MiB")
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return PackResult{}, err
	}
	outFile := filepath.Join(outDir, fmt.Sprintf("%s-%s.zip", src.Manifest.ID, src.Manifest.Version))
	if err := os.WriteFile(outFile, zipBytes, 0o644); err != nil {
		return PackResult{}, err
	}

	sum := sha256.Sum256(zipBytes)
	displayName := src.Manifest.Name
	if displayName == "" {
		displayName = src.Manifest.ID
	}
	return PackResult{
		ID:      src.Manifest.ID,
		Name:    displayName,
		Version: src.Manifest.Version,
		File:    filepath.ToSlash(outFile),
		SHA256:  hex.EncodeToString(sum[:]),
	}, nil
}

func normalizeSourceManifest(src *SourceManifest) {
	if src.Main == "" {
		src.Main = "."
	}
	if src.Manifest.Dependencies == nil {
		src.Manifest.Dependencies = []string{}
	}
	if src.Manifest.Routes == nil {
		src.Manifest.Routes = []string{}
	}
	if src.Manifest.Capabilities == nil {
		src.Manifest.Capabilities = []string{}
	}
	if src.Manifest.Events == nil {
		src.Manifest.Events = []string{}
	}
	if src.Manifest.Pages == nil {
		src.Manifest.Pages = []sdk.PageContribution{}
	}
	if src.Manifest.Tasks == nil {
		src.Manifest.Tasks = []sdk.TaskDeclaration{}
	}
	if src.Manifest.Permissions == nil {
		src.Manifest.Permissions = []string{}
	}
	if src.Routes == nil {
		src.Routes = []Route{}
	}
}

func validateSourceManifest(src SourceManifest) error {
	if err := src.Manifest.Validate(); err != nil {
		return err
	}
	if src.Protocol != 1 || len(src.Manifest.Version) > 64 || !versionPattern.MatchString(src.Manifest.Version) {
		return errors.New("invalid package version or protocol")
	}
	seenPerms := map[string]bool{}
	for _, p := range src.Manifest.Permissions {
		if !slices.Contains(validPermissions, p) || seenPerms[p] {
			return fmt.Errorf("unknown or duplicate permission %q", p)
		}
		seenPerms[p] = true
	}
	need := func(perm string, used bool) error {
		if used && !seenPerms[perm] {
			return fmt.Errorf("permission %s required", perm)
		}
		return nil
	}
	seenRoutes := map[string]bool{}
	for _, r := range src.Routes {
		if !slices.Contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, r.Method) {
			return fmt.Errorf("invalid route method %q", r.Method)
		}
		if !slices.Contains([]string{"public", "user", "admin"}, r.Access) {
			return fmt.Errorf("invalid route access %q", r.Access)
		}
		if !validRoutePath(r.Path) {
			return fmt.Errorf("invalid route path %q", r.Path)
		}
		key := r.Method + " " + r.Path
		if seenRoutes[key] {
			return fmt.Errorf("duplicate route %q", key)
		}
		seenRoutes[key] = true
		if err := need("routes."+r.Access, true); err != nil {
			return err
		}
	}
	for _, check := range []struct {
		perm string
		used bool
	}{
		{"events", len(src.Manifest.Events) > 0},
		{"pages", len(src.Manifest.Pages) > 0},
		{"tasks", len(src.Manifest.Tasks) > 0},
		{"frontend", src.Frontend != ""},
		{"database", len(src.Migrations) > 0},
	} {
		if err := need(check.perm, check.used); err != nil {
			return err
		}
	}
	if src.Frontend != "" && !safeRelPath(src.Frontend) {
		return fmt.Errorf("invalid frontend path %q", src.Frontend)
	}
	for i, m := range src.Migrations {
		if m.Version != i+1 || len(m.MySQL) == 0 || len(m.Postgres) == 0 {
			return errors.New("migrations must be consecutive and support both MySQL and PostgreSQL")
		}
	}
	if src.Manifest.MigrationVersion != len(src.Migrations) {
		return errors.New("migrationVersion must match migrations length")
	}
	return nil
}

func validRoutePath(value string) bool {
	if !routePathPattern.MatchString(value) {
		return false
	}
	for segment := range strings.SplitSeq(value, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func safeRelPath(name string) bool {
	return name != "" && name != "." && path.Clean(name) == name &&
		!strings.HasPrefix(name, "/") && !strings.HasPrefix(name, "../") &&
		!strings.Contains(name, ":") && strings.IndexByte(name, '\\') < 0 &&
		strings.IndexFunc(name, func(c rune) bool { return c < 32 }) < 0
}

func upsertCatalog(catalogPath string, entry CatalogEntry) error {
	if entry.ID == "" || entry.Version == "" || !sha256Pattern.MatchString(entry.SHA256) || !validCatalogURL(entry.URL) {
		return errors.New("invalid catalog entry fields")
	}
	if entry.Name == "" {
		entry.Name = entry.ID
	}

	catalog := MarketCatalog{Packages: []CatalogEntry{}}
	if raw, err := os.ReadFile(catalogPath); err == nil {
		if err := json.Unmarshal(raw, &catalog); err != nil {
			return fmt.Errorf("parse %s: %w", catalogPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	replaced := false
	for i := range catalog.Packages {
		if catalog.Packages[i].ID == entry.ID {
			catalog.Packages[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		catalog.Packages = append(catalog.Packages, entry)
	}
	if len(catalog.Packages) > maxCatalogEntries {
		return fmt.Errorf("catalog exceeds %d packages", maxCatalogEntries)
	}
	sort.Slice(catalog.Packages, func(i, j int) bool { return catalog.Packages[i].ID < catalog.Packages[j].ID })

	encoded, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(catalogPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(catalogPath, encoded, 0o644)
}

func validCatalogURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	host := strings.ToLower(u.Hostname())
	return u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")
}
