package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.1.0", "1.0.9", 1},
		{"1.1.0", "1.1.0", 0},
		{"v1.1.0", "1.1.0", 0},
		{"1.1.0", "1.2.0", -1},
		{"1.10.0", "1.9.9", 1},
		{"1.1", "1.1.0", 0},
		{"1.2.0-beta", "1.2.0", 0},
	}
	for _, tc := range cases {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q,%q)=%d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestChecksumFromText(t *testing.T) {
	sum := strings.Repeat("a", 64)
	text := "# checksums\n" + sum + "  ykt-darwin-arm64.zip\n" + strings.Repeat("b", 64) + " *ykt-linux-amd64.tar.gz\n"
	if got := checksumFromText(text, "ykt-darwin-arm64.zip"); got != sum {
		t.Fatalf("checksumFromText = %q", got)
	}
	if got := checksumFromText(text, "missing.zip"); got != "" {
		t.Fatalf("missing file checksum = %q", got)
	}
}

func TestPickAsset(t *testing.T) {
	release := &githubRelease{Assets: []githubAsset{
		{Name: "ykt-linux-amd64.tar.gz"},
		{Name: "ykt-darwin-arm64.zip", BrowserDownloadURL: "https://example.com/a.zip"},
	}}
	asset, err := pickAsset(release, "darwin", "arm64")
	if err != nil || asset.Name != "ykt-darwin-arm64.zip" {
		t.Fatalf("pickAsset = %+v, %v", asset, err)
	}
	if _, err := pickAsset(release, "windows", "amd64"); err == nil || !strings.Contains(err.Error(), "暂无更新包") {
		t.Fatalf("expected missing asset error, got %v", err)
	}
}

func TestCheckAvailableAndNoUpdate(t *testing.T) {
	zipBytes := buildAppZip(t, "1.2.0")
	sum := sha256.Sum256(zipBytes)
	checksum := hex.EncodeToString(sum[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name":     "v1.2.0",
				"body":         "修复若干问题",
				"published_at": "2026-11-05T00:00:00Z",
				"assets": []map[string]any{
					{"name": "ykt-darwin-arm64.zip", "browser_download_url": "http://" + r.Host + "/download", "size": len(zipBytes)},
					{"name": "checksums.txt", "browser_download_url": "http://" + r.Host + "/checksums.txt"},
				},
			})
		case "/checksums.txt":
			_, _ = w.Write([]byte(checksum + "  ykt-darwin-arm64.zip\n"))
		case "/download":
			_, _ = w.Write(zipBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	opts := Options{
		CurrentVersion: "1.1.0",
		APIURL:         server.URL + "/releases/latest",
		Client:         server.Client(),
		GOOS:           "darwin",
		GOARCH:         "arm64",
	}
	info, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("Check 失败: %v", err)
	}
	if !info.Available || info.LatestVersion != "1.2.0" || info.Checksum != checksum {
		t.Fatalf("Check info = %+v", info)
	}
	if info.AssetURL == "" || info.Notes == "" {
		t.Fatalf("Check 缺少资产或说明: %+v", info)
	}

	opts.CurrentVersion = "1.2.0"
	info, err = Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("Check(最新版) 失败: %v", err)
	}
	if info.Available {
		t.Fatalf("已是最新版本时不应 Available: %+v", info)
	}
}

func TestCheckMissingChecksum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v1.2.0",
			"assets": []map[string]any{
				{"name": "ykt-darwin-arm64.zip", "browser_download_url": "http://" + r.Host + "/download", "size": 10},
			},
		})
	}))
	defer server.Close()
	_, err := Check(context.Background(), Options{
		CurrentVersion: "1.1.0",
		APIURL:         server.URL,
		Client:         server.Client(),
		GOOS:           "darwin",
		GOARCH:         "arm64",
	})
	if err == nil || !strings.Contains(err.Error(), "校验") {
		t.Fatalf("缺少校验信息时应拒绝，err=%v", err)
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "bad.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("../evil.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("evil"))
	_ = writer.Close()
	_ = file.Close()
	if err := extractZip(zipPath, filepath.Join(dir, "out")); err == nil {
		t.Fatal("zip slip 应被拒绝")
	}
}

func TestExtractZipAndFindAppBundle(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("plutil 校验仅 macOS")
	}
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "update.zip")
	if err := os.WriteFile(zipPath, buildAppZip(t, "1.2.0"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(dir, "payload")
	if err := extractZip(zipPath, payload); err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	bundle, err := findAppBundle(payload)
	if err != nil {
		t.Fatalf("findAppBundle: %v", err)
	}
	if !strings.HasSuffix(bundle, "雨课助手.app") {
		t.Fatalf("bundle = %s", bundle)
	}
	if err := validateBundle(bundle, "1.2.0"); err != nil {
		t.Fatalf("validateBundle: %v", err)
	}
	if err := validateBundle(bundle, "1.3.0"); err == nil {
		t.Fatal("版本不匹配应报错")
	}
}

func TestExtractTarGz(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "update.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	content := []byte("binary")
	if err := tw.WriteHeader(&tar.Header{Name: "ykt", Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = file.Close()

	dest := filepath.Join(dir, "out")
	if err := extractTarGz(archive, dest); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "ykt"))
	if err != nil || string(data) != "binary" {
		t.Fatalf("解压结果错误: %v", err)
	}
}

func TestVerifySHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("hello"))
	if err := verifySHA256(path, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("校验正确文件失败: %v", err)
	}
	if err := verifySHA256(path, strings.Repeat("0", 64)); err == nil {
		t.Fatal("错误校验值应失败")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("校验失败的文件应被删除")
	}
}

// buildAppZip 构造一个最小可识别的 App 更新包。
func buildAppZip(t *testing.T, version string) []byte {
	t.Helper()
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.wails.ykt</string>
<key>CFBundleShortVersionString</key><string>` + version + `</string>
</dict></plist>`
	buf := &bytes.Buffer{}
	writer := zip.NewWriter(buf)
	files := map[string]string{
		"雨课助手.app/Contents/Info.plist": plist,
		"雨课助手.app/Contents/MacOS/ykt":  "#!/bin/sh\nexit 0\n",
	}
	for name, content := range files {
		w, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCheckSendsToken(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.1.0"})
	}))
	defer server.Close()
	if _, err := Check(context.Background(), Options{
		CurrentVersion: "1.1.0",
		APIURL:         server.URL,
		Client:         server.Client(),
		Token:          "test-token",
	}); err != nil {
		t.Fatalf("Check 失败: %v", err)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}
