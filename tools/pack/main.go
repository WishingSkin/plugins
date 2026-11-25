package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
	"unicode/utf8"

	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

const (
	maxPackageBytes      = 128 << 20
	maxUserFrontendBytes = 2 << 20
	maxPackageEntries    = 256
	maxCatalogEntries    = 100
	signatureFile        = "plugin.sig"
)

var (
	defaultTargets = []string{
		"linux/amd64",
		"linux/arm64",
		"windows/amd64",
		"darwin/amd64",
		"darwin/arm64",
	}
	v1Permissions = []string{
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
	validPermissions = append(slices.Clone(v1Permissions),
		"hooks",
		"hooks.image",
		"user.frontend",
		"host.public.read",
		"host.textures.read",
		"host.textures.write",
		"host.characters.read",
		"host.characters.write",
		"host.users.read",
		"host.points.read",
		"host.points.spend",
	)
	validHooks            = []string{"text.check", "texture.upload.check"}
	versionPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
	routePathPattern      = regexp.MustCompile(`^/(?:[A-Za-z0-9._~-]+/)*[A-Za-z0-9._~-]*$`)
	userFrontendIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	sha256Pattern         = regexp.MustCompile(`^[0-9a-f]{64}$`)
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
	Manifest      sdk.Manifest       `json:"manifest"`
	Protocol      int                `json:"protocol"`
	Main          string             `json:"main,omitempty"`
	Routes        []Route            `json:"routes"`
	Frontend      string             `json:"frontend,omitempty"`
	UserFrontends []sdk.UserFrontend `json:"userFrontends,omitempty"`
	Hooks         []string           `json:"hooks,omitempty"`
	Migrations    []Migration        `json:"migrations,omitempty"`
}

type PackageManifest struct {
	Manifest      sdk.Manifest       `json:"manifest"`
	Protocol      int                `json:"protocol"`
	Executables   map[string]string  `json:"executables"`
	Routes        []Route            `json:"routes"`
	Frontend      string             `json:"frontend,omitempty"`
	UserFrontends []sdk.UserFrontend `json:"userFrontends,omitempty"`
	Hooks         []string           `json:"hooks,omitempty"`
	Migrations    []Migration        `json:"migrations,omitempty"`
}

type PackOptions struct {
	SigningKey  string
	RequireSign bool
}

type PackResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
	Signed  bool   `json:"signed,omitempty"`
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
		return errors.New("usage: pack <pack|catalog|keygen|verify> [flags]")
	}
	switch args[0] {
	case "pack":
		return runPack(ctx, args[1:], builder)
	case "catalog":
		return runCatalog(args[1:])
	case "keygen":
		return runKeygen(args[1:])
	case "verify":
		return runVerify(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func runPack(ctx context.Context, args []string, builder binaryBuilder) error {
	flags := flag.NewFlagSet("pack", flag.ContinueOnError)
	dir := flags.String("dir", "", "plugin directory containing manifest.json")
	outDir := flags.String("out", "dist", "output directory for packaged ZIP")
	targetsRaw := flags.String("targets", strings.Join(defaultTargets, ","), "comma-separated GOOS/GOARCH targets")
	signKey := flags.String("sign-key", os.Getenv("WS_PLUGIN_SIGNING_KEY"), "Ed25519 private signing seed/key (hex or base64)")
	requireSign := flags.Bool("require-sign", false, "require valid signing key and emit plugin.sig")
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
	result, err := packPluginWithOptions(ctx, *dir, *outDir, targets, builder, PackOptions{
		SigningKey:  strings.TrimSpace(*signKey),
		RequireSign: *requireSign,
	})
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

func runKeygen(args []string) error {
	flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	out := map[string]string{
		"privateSeedHex": hex.EncodeToString(priv.Seed()),
		"publicKeyHex":   hex.EncodeToString(pub),
	}
	encoded, _ := json.Marshal(out)
	_, err = fmt.Fprintln(os.Stdout, string(encoded))
	return err
}

func runVerify(args []string) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	file := flags.String("file", "", "plugin ZIP file path")
	pubkeyRaw := flags.String("pubkey", os.Getenv("WS_OFFICIAL_PLUGIN_PUBKEYS"), "Ed25519 public key (hex or base64)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*file) == "" {
		return errors.New("-file is required")
	}
	pub, err := parsePublicKey(*pubkeyRaw)
	if err != nil {
		return err
	}
	zipBytes, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	canonicalDigest, err := verifyArchiveSignature(zipBytes, pub)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(zipBytes)
	out := map[string]any{
		"valid":           true,
		"sha256":          hex.EncodeToString(sum[:]),
		"canonicalDigest": hex.EncodeToString(canonicalDigest),
	}
	encoded, _ := json.Marshal(out)
	_, err = fmt.Fprintln(os.Stdout, string(encoded))
	return err
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
	return packPluginWithOptions(ctx, pluginDir, outDir, targets, builder, PackOptions{})
}

func packPluginWithOptions(ctx context.Context, pluginDir, outDir string, targets []string, builder binaryBuilder, opts PackOptions) (PackResult, error) {
	var privKey ed25519.PrivateKey
	if opts.SigningKey != "" {
		var err error
		privKey, err = parseSigningKey(opts.SigningKey)
		if err != nil {
			return PackResult{}, err
		}
	} else if opts.RequireSign {
		return PackResult{}, errors.New("signing key required (-sign-key or WS_PLUGIN_SIGNING_KEY)")
	}

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
		Manifest:      src.Manifest,
		Protocol:      src.Protocol,
		Executables:   map[string]string{},
		Routes:        src.Routes,
		Frontend:      src.Frontend,
		UserFrontends: src.UserFrontends,
		Hooks:         src.Hooks,
		Migrations:    src.Migrations,
	}

	type archiveItem struct {
		name string
		mode fs.FileMode
		data []byte
	}
	var items []archiveItem
	addedPaths := map[string]bool{}
	addItem := func(name string, mode fs.FileMode, data []byte) error {
		if addedPaths[name] {
			return fmt.Errorf("duplicate archive path %q", name)
		}
		addedPaths[name] = true
		items = append(items, archiveItem{name: name, mode: mode, data: data})
		return nil
	}

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
		if err := addItem(relBin, 0o755, binBytes); err != nil {
			return PackResult{}, err
		}
	}

	if src.Frontend != "" {
		frontendBytes, err := os.ReadFile(filepath.Join(absDir, filepath.FromSlash(src.Frontend)))
		if err != nil {
			return PackResult{}, fmt.Errorf("read frontend %q: %w", src.Frontend, err)
		}
		if len(frontendBytes) > 1<<20 {
			return PackResult{}, errors.New("frontend HTML exceeds 1 MiB")
		}
		if err := addItem(src.Frontend, 0o644, frontendBytes); err != nil {
			return PackResult{}, err
		}
	}

	seenUserAssets := map[string]bool{}
	for _, uf := range src.UserFrontends {
		if uf.Path == "plugin.json" || uf.Path == signatureFile || strings.HasPrefix(uf.Path, "bin/") {
			return PackResult{}, fmt.Errorf("user frontend path %q conflicts with reserved entry", uf.Path)
		}
		if seenUserAssets[uf.Path] {
			continue
		}
		seenUserAssets[uf.Path] = true
		if addedPaths[uf.Path] {
			continue
		}
		ufBytes, err := os.ReadFile(filepath.Join(absDir, filepath.FromSlash(uf.Path)))
		if err != nil {
			return PackResult{}, fmt.Errorf("read user frontend %q: %w", uf.Path, err)
		}
		if len(ufBytes) > maxUserFrontendBytes {
			return PackResult{}, fmt.Errorf("user frontend %q exceeds 2 MiB", uf.Path)
		}
		if err := addItem(uf.Path, 0o644, ufBytes); err != nil {
			return PackResult{}, err
		}
	}

	manifestBytes, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return PackResult{}, err
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := addItem("plugin.json", 0o644, manifestBytes); err != nil {
		return PackResult{}, err
	}

	if len(privKey) == ed25519.PrivateKeySize {
		var entries []digestEntry
		for _, item := range items {
			sum := sha256.Sum256(item.data)
			entries = append(entries, digestEntry{name: item.name, hash: hex.EncodeToString(sum[:])})
		}
		sig := ed25519.Sign(privKey, computeCanonicalTranscript(entries))
		if err := addItem(signatureFile, 0o644, []byte(hex.EncodeToString(sig)+"\n")); err != nil {
			return PackResult{}, err
		}
	}

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
		Signed:  len(privKey) == ed25519.PrivateKeySize,
	}, nil
}

type digestEntry struct {
	name string
	hash string
}

func computeCanonicalTranscript(entries []digestEntry) []byte {
	sorted := slices.Clone(entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	transcript := sha256.New()
	_, _ = transcript.Write([]byte("wishingskin-plugin-sig-v1\n"))
	for _, item := range sorted {
		_, _ = fmt.Fprintf(transcript, "%s\x00%s\n", item.name, item.hash)
	}
	return transcript.Sum(nil)
}

func canonicalArchiveDigestFromZip(zipBytes []byte) ([]byte, error) {
	if len(zipBytes) > maxPackageBytes {
		return nil, errors.New("package exceeds 128 MiB")
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, err
	}
	if len(zr.File) > maxPackageEntries {
		return nil, errors.New("too many package files")
	}
	var entries []digestEntry
	var total uint64
	seen := map[string]bool{}
	for _, f := range zr.File {
		name := strings.TrimSuffix(f.Name, "/")
		if !safeRelPath(name) || seen[name] || f.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("unsafe archive entry")
		}
		seen[name] = true
		total += f.UncompressedSize64
		if total > maxPackageBytes {
			return nil, errors.New("expanded package exceeds limit")
		}
		if f.FileInfo().IsDir() || f.Name == signatureFile {
			continue
		}
		if f.Mode()&os.ModeType != 0 {
			return nil, errors.New("unsupported archive entry")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		n, copyErr := io.Copy(h, io.LimitReader(rc, maxPackageBytes+1))
		rc.Close()
		if copyErr != nil || uint64(n) > maxPackageBytes {
			return nil, errors.New("unable to hash archive entry")
		}
		entries = append(entries, digestEntry{
			name: f.Name,
			hash: hex.EncodeToString(h.Sum(nil)),
		})
	}
	return computeCanonicalTranscript(entries), nil
}

func verifyArchiveSignature(zipBytes []byte, pub ed25519.PublicKey) ([]byte, error) {
	if len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("invalid Ed25519 public key")
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, err
	}
	var sigFile *zip.File
	for _, f := range zr.File {
		if strings.TrimSuffix(f.Name, "/") == signatureFile {
			if sigFile != nil || f.FileInfo().IsDir() || f.Mode()&os.ModeSymlink != 0 || f.Mode()&os.ModeType != 0 {
				return nil, errors.New("invalid plugin.sig entry")
			}
			sigFile = f
		}
	}
	if sigFile == nil {
		return nil, errors.New("plugin.sig missing from archive")
	}
	rc, err := sigFile.Open()
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(rc, 1025))
	rc.Close()
	if err != nil || len(raw) > 1024 {
		return nil, errors.New("unable to read plugin.sig")
	}
	sigBytes, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return nil, errors.New("malformed Ed25519 signature")
	}
	digest, err := canonicalArchiveDigestFromZip(zipBytes)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(pub, digest, sigBytes) {
		return nil, errors.New("signature verification failed")
	}
	return digest, nil
}

func decodeFlexibleKeyBytes(raw string, allowedLens ...int) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, errors.New("empty key")
	}
	tryMatch := func(b []byte) bool {
		return slices.Contains(allowedLens, len(b))
	}
	if b, err := hex.DecodeString(s); err == nil && tryMatch(b) {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && tryMatch(b) {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil && tryMatch(b) {
		return b, nil
	}
	return nil, errors.New("invalid key encoding or length")
}

func parseSigningKey(raw string) (ed25519.PrivateKey, error) {
	b, err := decodeFlexibleKeyBytes(raw, ed25519.SeedSize, ed25519.PrivateKeySize)
	if err != nil {
		return nil, fmt.Errorf("invalid signing key: %w", err)
	}
	if len(b) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(b), nil
	}
	return ed25519.PrivateKey(b), nil
}

func parsePublicKey(raw string) (ed25519.PublicKey, error) {
	b, err := decodeFlexibleKeyBytes(raw, ed25519.PublicKeySize)
	if err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}
	return ed25519.PublicKey(b), nil
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
	if (src.Protocol != 1 && src.Protocol != 2) || len(src.Manifest.Version) > 64 || !versionPattern.MatchString(src.Manifest.Version) {
		return errors.New("invalid package version or protocol")
	}
	seenPerms := map[string]bool{}
	for _, p := range src.Manifest.Permissions {
		if !slices.Contains(validPermissions, p) || seenPerms[p] {
			return fmt.Errorf("unknown or duplicate permission %q", p)
		}
		if src.Protocol == 1 && !slices.Contains(v1Permissions, p) {
			return fmt.Errorf("permission %q requires protocol 2", p)
		}
		seenPerms[p] = true
	}
	if src.Protocol == 1 && (len(src.Hooks) > 0 || len(src.UserFrontends) > 0) {
		return errors.New("hooks and userFrontends require protocol 2")
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
		{"hooks", len(src.Hooks) > 0 || seenPerms["hooks.image"]},
		{"user.frontend", len(src.UserFrontends) > 0},
	} {
		if err := need(check.perm, check.used); err != nil {
			return err
		}
	}
	seenHooks := map[string]bool{}
	for _, h := range src.Hooks {
		if !slices.Contains(validHooks, h) || seenHooks[h] {
			return fmt.Errorf("invalid or duplicate hook %q", h)
		}
		seenHooks[h] = true
	}
	if len(src.UserFrontends) > 8 {
		return errors.New("too many user frontends")
	}
	seenUF := map[string]bool{}
	for _, uf := range src.UserFrontends {
		if !userFrontendIDPattern.MatchString(uf.ID) || len(uf.ID) > 64 || seenUF[uf.ID] {
			return fmt.Errorf("invalid or duplicate user frontend id %q", uf.ID)
		}
		seenUF[uf.ID] = true
		label := strings.TrimSpace(uf.Label)
		if label == "" || !utf8.ValidString(uf.Label) || utf8.RuneCountInString(label) > 64 {
			return fmt.Errorf("invalid user frontend label for %q", uf.ID)
		}
		if !utf8.ValidString(uf.Description) || utf8.RuneCountInString(uf.Description) > 256 {
			return fmt.Errorf("invalid user frontend description for %q", uf.ID)
		}
		if uf.Slot != "user.tools" && uf.Slot != "texture.action" {
			return fmt.Errorf("invalid user frontend slot %q", uf.Slot)
		}
		if !safeRelPath(uf.Path) {
			return fmt.Errorf("invalid user frontend path %q", uf.Path)
		}
		if len(uf.Kinds) > 0 {
			if uf.Slot != "texture.action" {
				return errors.New("kinds only allowed for texture.action slot")
			}
			seenKinds := map[string]bool{}
			for _, kind := range uf.Kinds {
				if (kind != "skin" && kind != "cape") || seenKinds[kind] {
					return fmt.Errorf("invalid user frontend kind %q", kind)
				}
				seenKinds[kind] = true
			}
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
