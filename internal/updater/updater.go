// Package updater 基于 GitHub Releases 检查并安装「雨课助手」的新版本。
//
// 检查：GET <APIURL>，取 latest release 的 tag 与平台对应的资产。
// 安装：下载资产 → SHA256 校验 → 解包 → 校验 App 版本/标识 → 由脱离的辅助脚本
// 等本进程退出后替换旧安装并重新启动。校验信息来自 release 的 checksums.txt
// （或 GitHub 资产自带的 sha256 digest），缺失时拒绝自动安装。
package updater

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/songoao25/yuketang-jiaoben/internal/model"
)

const (
	// DefaultAPIURL 是 GitHub Releases latest 接口（origin 仓库）。
	DefaultAPIURL = "https://api.github.com/repos/songoao25/yuketang-jiaoben/releases/latest"
	// DefaultWebBase 是 GitHub 网页版仓库地址；网页 /releases/latest 走普通 CDN，
	// 不占用 API 60 次/小时的限额（默认检查用这条路径）。
	DefaultWebBase = "https://github.com/songoao25/yuketang-jiaoben"
	// checksumsAssetName 是 release 中的校验文件；每行 "sha256  filename"。
	checksumsAssetName = "checksums.txt"
	// maxDownloadBytes 防止异常响应占满磁盘（512 MiB）。
	maxDownloadBytes = 512 << 20
	notesLimit       = 1800
)

const userAgent = "yuketang-jiaoben-updater/1.0"

// Options 控制一次更新检查。
type Options struct {
	CurrentVersion string
	APIURL         string
	WebBase        string
	Client         *http.Client
	GOOS           string
	GOARCH         string
	Token          string // 可选 GitHub token，提高 API 限额
}

// NewOptions 返回默认检查参数；可用 YKT_UPDATE_API_URL 覆盖接口地址（测试/镜像）。
func NewOptions(currentVersion string) Options {
	apiURL := strings.TrimSpace(os.Getenv("YKT_UPDATE_API_URL"))
	webBase := strings.TrimSpace(os.Getenv("YKT_UPDATE_WEB_BASE"))
	token := strings.TrimSpace(os.Getenv("YKT_UPDATE_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	return Options{
		CurrentVersion: currentVersion,
		APIURL:         apiURL,
		WebBase:        webBase,
		Token:          token,
	}
}

func (o Options) normalized() Options {
	if strings.TrimSpace(o.APIURL) == "" && strings.TrimSpace(o.WebBase) == "" {
		o.WebBase = DefaultWebBase
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 20 * time.Second}
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	if strings.TrimSpace(o.CurrentVersion) == "" {
		o.CurrentVersion = "0.0.0"
	}
	return o
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	PublishedAt time.Time     `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

// Check 比较当前版本与 GitHub 最新 release。已是最新时 Available=false。
func Check(ctx context.Context, opts Options) (model.UpdateInfo, error) {
	opts = opts.normalized()
	var release *githubRelease
	var err error
	if opts.APIURL != "" {
		release, err = fetchLatest(ctx, opts)
	} else {
		release, err = fetchLatestWeb(ctx, opts)
	}
	if err != nil {
		return model.UpdateInfo{CurrentVersion: opts.CurrentVersion}, err
	}
	latest := normalizeVersion(release.TagName)
	info := model.UpdateInfo{
		CurrentVersion: opts.CurrentVersion,
		LatestVersion:  latest,
		PublishedAt:    release.PublishedAt.UnixMilli(),
		Notes:          truncate(strings.TrimSpace(release.Body), notesLimit),
	}
	if !versionGreater(latest, opts.CurrentVersion) {
		return info, nil
	}

	asset, err := pickAsset(release, opts.GOOS, opts.GOARCH)
	if err != nil {
		return info, err
	}
	sum, err := checksumFor(ctx, opts, release, asset)
	if err != nil {
		return info, err
	}
	info.Available = true
	info.AssetName = asset.Name
	info.AssetURL = asset.BrowserDownloadURL
	info.AssetSize = asset.Size
	info.Checksum = sum
	return info, nil
}

func fetchLatest(ctx context.Context, opts Options) (*githubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.APIURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造更新请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)
	if opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.Token)
	}
	resp, err := opts.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("检查更新失败（网络不可用或代理异常）: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, errors.New("暂时没有可用的发布版本")
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, errors.New("GitHub 接口请求受限（可能是速率限制）；可设置 GITHUB_TOKEN 后重试，或稍后再试")
	default:
		return nil, fmt.Errorf("检查更新失败：GitHub 返回 HTTP %d", resp.StatusCode)
	}
	var release githubRelease
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 4<<20))
	if err := decoder.Decode(&release); err != nil {
		return nil, fmt.Errorf("解析更新信息失败: %w", err)
	}
	if release.TagName == "" {
		return nil, errors.New("更新信息缺少版本号")
	}
	return &release, nil
}

// fetchLatestWeb 用网页版 /releases/latest 的 302 Location 取最新 tag，
// 再按固定资产名拼出下载地址；不访问 GitHub API，避免未登录限流。
func fetchLatestWeb(ctx context.Context, opts Options) (*githubRelease, error) {
	base := strings.TrimRight(opts.WebBase, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/releases/latest", nil)
	if err != nil {
		return nil, fmt.Errorf("构造更新请求失败: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	client := *opts.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("检查更新失败（网络不可用或代理异常）: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var location string
	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		location = resp.Header.Get("Location")
	case http.StatusOK:
		if resp.Request != nil && resp.Request.URL != nil {
			location = resp.Request.URL.String()
		}
	case http.StatusNotFound:
		return nil, errors.New("暂时没有可用的发布版本")
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, errors.New("GitHub 网页请求受限，请稍后重试")
	default:
		return nil, fmt.Errorf("检查更新失败：GitHub 返回 HTTP %d", resp.StatusCode)
	}
	const marker = "/releases/tag/"
	idx := strings.LastIndex(location, marker)
	if idx < 0 {
		return nil, errors.New("更新信息缺少版本号")
	}
	tag := location[idx+len(marker):]
	if cut := strings.IndexAny(tag, "?#"); cut >= 0 {
		tag = tag[:cut]
	}
	if tag == "" {
		return nil, errors.New("更新信息缺少版本号")
	}
	name := assetName(opts.GOOS, opts.GOARCH)
	assetURL := fmt.Sprintf("%s/releases/download/%s/%s", base, tag, name)
	checksumsURL := fmt.Sprintf("%s/releases/download/%s/%s", base, tag, checksumsAssetName)
	release := &githubRelease{TagName: tag, Assets: []githubAsset{
		{Name: name, BrowserDownloadURL: assetURL, Size: headContentLength(ctx, opts.Client, assetURL)},
		{Name: checksumsAssetName, BrowserDownloadURL: checksumsURL},
	}}
	return release, nil
}

func headContentLength(ctx context.Context, client *http.Client, url string) int64 {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	return resp.ContentLength
}

// assetName 返回当前平台约定的更新包名。
func assetName(goos, goarch string) string {
	if goos == "linux" {
		return fmt.Sprintf("ykt-%s-%s.tar.gz", goos, goarch)
	}
	return fmt.Sprintf("ykt-%s-%s.zip", goos, goarch)
}

func pickAsset(release *githubRelease, goos, goarch string) (githubAsset, error) {
	want := assetName(goos, goarch)
	for _, asset := range release.Assets {
		if asset.Name == want {
			if asset.BrowserDownloadURL == "" {
				return githubAsset{}, fmt.Errorf("更新包 %s 缺少下载地址", want)
			}
			return asset, nil
		}
	}
	return githubAsset{}, fmt.Errorf("该平台暂无更新包（%s/%s，期望 %s）", goos, goarch, want)
}

func checksumFor(ctx context.Context, opts Options, release *githubRelease, asset githubAsset) (string, error) {
	for _, candidate := range release.Assets {
		if candidate.Name != checksumsAssetName {
			continue
		}
		token := opts.Token
		if opts.APIURL == "" {
			token = "" // 网页 CDN 不发送 token
		}
		body, err := fetchText(ctx, opts.Client, candidate.BrowserDownloadURL, token, 1<<20)
		if err != nil {
			return "", fmt.Errorf("下载校验文件失败: %w", err)
		}
		if sum := checksumFromText(body, asset.Name); sum != "" {
			return sum, nil
		}
		return "", fmt.Errorf("%s 中缺少 %s 的校验值", checksumsAssetName, asset.Name)
	}
	if digest := strings.TrimSpace(asset.Digest); strings.HasPrefix(digest, "sha256:") {
		sum := strings.TrimSpace(strings.TrimPrefix(digest, "sha256:"))
		if isHexSHA256(sum) {
			return sum, nil
		}
	}
	return "", errors.New("发布资产缺少 SHA256 校验信息，已拒绝自动更新")
}

func checksumFromText(text, name string) string {
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sum := strings.ToLower(fields[0])
		file := strings.TrimPrefix(fields[len(fields)-1], "*")
		if file == name && isHexSHA256(sum) {
			return sum
		}
	}
	return ""
}

func fetchText(ctx context.Context, client *http.Client, url, token string, limit int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Apply 下载并校验更新包，然后安排辅助脚本在 App 退出后替换安装。
// 返回 nil 表示替换已排入队列，调用方应尽快退出 App。
func Apply(ctx context.Context, info model.UpdateInfo, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if !info.Available {
		return errors.New("当前没有可安装的更新")
	}
	if info.AssetURL == "" || !isHexSHA256(info.Checksum) {
		return errors.New("更新信息不完整，已拒绝安装")
	}
	switch runtime.GOOS {
	case "darwin":
		return applyDarwin(ctx, info, logf)
	case "windows":
		return applyWindows(ctx, info, logf)
	default:
		return applyLinux(ctx, info, logf)
	}
}

// CurrentBundlePath 返回当前运行 App 的 .app 路径；非 .app 运行时报错。
func CurrentBundlePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("无法定位当前可执行文件: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("解析可执行文件路径失败: %w", err)
	}
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if !strings.HasSuffix(bundle, ".app") {
		return "", errors.New("当前不是从 .app 运行，无法自动更新")
	}
	return bundle, nil
}

func applyDarwin(ctx context.Context, info model.UpdateInfo, logf func(string, ...any)) error {
	bundle, err := CurrentBundlePath()
	if err != nil {
		return err
	}
	parent := filepath.Dir(bundle)
	if !isWritableInstallDir(parent) {
		return fmt.Errorf("当前安装位置不支持自动更新（%s）；请把 App 放到 /Applications 或 ~/Applications", bundle)
	}
	stage, err := os.MkdirTemp(parent, ".ykt-update-")
	if err != nil {
		return fmt.Errorf("创建更新临时目录失败: %w", err)
	}
	zipPath := filepath.Join(stage, "update.zip")
	logf("开始下载 %s（%d 字节）", info.AssetName, info.AssetSize)
	if err := download(ctx, info.AssetURL, zipPath); err != nil {
		return err
	}
	if err := verifySHA256(zipPath, info.Checksum); err != nil {
		return err
	}
	payload := filepath.Join(stage, "payload")
	if err := extractZip(zipPath, payload); err != nil {
		return err
	}
	newBundle, err := findAppBundle(payload)
	if err != nil {
		return err
	}
	if err := validateBundle(newBundle, info.LatestVersion); err != nil {
		return err
	}
	logPath := helperLogPath()
	script := filepath.Join(stage, "apply.sh")
	if err := os.WriteFile(script, []byte(darwinHelperScript), 0o700); err != nil {
		return fmt.Errorf("写入更新脚本失败: %w", err)
	}
	logf("开始替换 App：%s", bundle)
	cmd := exec.Command("/bin/sh", script, bundle, newBundle, strconv.Itoa(os.Getpid()), logPath, stage)
	if err := startDetached(cmd); err != nil {
		return fmt.Errorf("启动更新脚本失败: %w", err)
	}
	return nil
}

const darwinHelperScript = `#!/bin/sh
set -u
target="$1"
staged="$2"
pid="$3"
log="$4"
stage="$5"
i=0
while kill -0 "$pid" 2>/dev/null; do
  i=$((i + 1))
  [ "$i" -gt 600 ] && break
  sleep 0.1
done
sleep 0.4
backup="${target}.bak"
rm -rf "$backup"
if mv "$target" "$backup" 2>>"$log" && mv "$staged" "$target" 2>>"$log"; then
  rm -rf "$backup"
  xattr -dr com.apple.quarantine "$target" >/dev/null 2>&1 || true
  open "$target" >>"$log" 2>&1
else
  rm -rf "$target" 2>/dev/null
  [ -e "$backup" ] && mv "$backup" "$target" 2>>"$log"
  echo "更新替换失败，已回滚" >>"$log"
fi
rm -rf "$stage" 2>/dev/null
`

func applyWindows(ctx context.Context, info model.UpdateInfo, logf func(string, ...any)) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法定位当前可执行文件: %w", err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(exe), ".ykt-update-")
	if err != nil {
		return fmt.Errorf("创建更新临时目录失败: %w", err)
	}
	zipPath := filepath.Join(stage, "update.zip")
	logf("开始下载 %s（%d 字节）", info.AssetName, info.AssetSize)
	if err := download(ctx, info.AssetURL, zipPath); err != nil {
		return err
	}
	if err := verifySHA256(zipPath, info.Checksum); err != nil {
		return err
	}
	payload := filepath.Join(stage, "payload")
	if err := extractZip(zipPath, payload); err != nil {
		return err
	}
	staged := filepath.Join(payload, "ykt.exe")
	if _, err := os.Stat(staged); err != nil {
		return errors.New("更新包中未找到 ykt.exe")
	}
	script := filepath.Join(stage, "apply.ps1")
	if err := os.WriteFile(script, []byte(windowsHelperScript), 0o600); err != nil {
		return fmt.Errorf("写入更新脚本失败: %w", err)
	}
	logPath := helperLogPath()
	logf("开始替换程序：%s", exe)
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script, exe, staged, strconv.Itoa(os.Getpid()), logPath, stage)
	if err := startDetached(cmd); err != nil {
		return fmt.Errorf("启动更新脚本失败: %w", err)
	}
	return nil
}

const windowsHelperScript = `param([string]$target, [string]$staged, [int]$pid, [string]$log, [string]$stage)
for ($i = 0; $i -lt 600; $i++) {
  if (-not (Get-Process -Id $pid -ErrorAction SilentlyContinue)) { break }
  Start-Sleep -Milliseconds 100
}
Start-Sleep -Milliseconds 500
try {
  Copy-Item -Force $staged $target -ErrorAction Stop
  Start-Process $target
  Add-Content -Path $log -Value "更新完成"
} catch {
  Add-Content -Path $log -Value ("更新失败: " + $_)
}
Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue
`

func applyLinux(ctx context.Context, info model.UpdateInfo, logf func(string, ...any)) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法定位当前可执行文件: %w", err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(exe), ".ykt-update-")
	if err != nil {
		return fmt.Errorf("创建更新临时目录失败: %w", err)
	}
	archive := filepath.Join(stage, "update.tar.gz")
	logf("开始下载 %s（%d 字节）", info.AssetName, info.AssetSize)
	if err := download(ctx, info.AssetURL, archive); err != nil {
		return err
	}
	if err := verifySHA256(archive, info.Checksum); err != nil {
		return err
	}
	payload := filepath.Join(stage, "payload")
	if err := extractTarGz(archive, payload); err != nil {
		return err
	}
	staged := filepath.Join(payload, "ykt")
	if _, err := os.Stat(staged); err != nil {
		return errors.New("更新包中未找到 ykt 可执行文件")
	}
	script := filepath.Join(stage, "apply.sh")
	if err := os.WriteFile(script, []byte(linuxHelperScript), 0o700); err != nil {
		return fmt.Errorf("写入更新脚本失败: %w", err)
	}
	logPath := helperLogPath()
	logf("开始替换程序：%s", exe)
	cmd := exec.Command("/bin/sh", script, exe, staged, strconv.Itoa(os.Getpid()), logPath, stage)
	if err := startDetached(cmd); err != nil {
		return fmt.Errorf("启动更新脚本失败: %w", err)
	}
	return nil
}

const linuxHelperScript = `#!/bin/sh
set -u
target="$1"
staged="$2"
pid="$3"
log="$4"
stage="$5"
i=0
while kill -0 "$pid" 2>/dev/null; do
  i=$((i + 1))
  [ "$i" -gt 600 ] && break
  sleep 0.1
done
sleep 0.4
if cp "$staged" "$target" 2>>"$log" && chmod +x "$target" 2>>"$log"; then
  "$target" >>"$log" 2>&1 &
  echo "更新完成" >>"$log"
else
  echo "更新替换失败" >>"$log"
fi
rm -rf "$stage" 2>/dev/null
`

// ---------------------------------------------------------------- 工具函数

func helperLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "ykt-update.log")
	}
	dir := filepath.Join(home, ".ykt")
	_ = os.MkdirAll(dir, 0o700)
	return filepath.Join(dir, "update.log")
}

func isWritableInstallDir(dir string) bool {
	clean := filepath.Clean(dir)
	home, _ := os.UserHomeDir()
	allowed := []string{"/Applications", filepath.Join(home, "Applications")}
	for _, candidate := range allowed {
		if clean == candidate {
			return true
		}
	}
	return false
}

func download(ctx context.Context, url, dest string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("构造下载请求失败: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载更新包失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载更新包失败：HTTP %d", resp.StatusCode)
	}
	file, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("创建更新包文件失败: %w", err)
	}
	defer func() { _ = file.Close() }()
	written, err := io.Copy(file, io.LimitReader(resp.Body, maxDownloadBytes))
	if err != nil {
		return fmt.Errorf("写入更新包失败: %w", err)
	}
	if written == 0 {
		return errors.New("下载到的更新包为空")
	}
	return nil
}

func verifySHA256(path, want string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("打开更新包失败: %w", err)
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return fmt.Errorf("计算校验值失败: %w", err)
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, want) {
		_ = os.Remove(path)
		return errors.New("更新包校验失败，已删除下载文件")
	}
	return nil
}

func extractZip(src, dest string) error {
	reader, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("打开更新包失败: %w", err)
	}
	defer func() { _ = reader.Close() }()
	dest = filepath.Clean(dest)
	for _, entry := range reader.File {
		name := filepath.Clean(strings.ReplaceAll(entry.Name, "\\", "/"))
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("更新包包含非法路径：%s", entry.Name)
		}
		target := filepath.Join(dest, name)
		if !strings.HasPrefix(target, dest+string(os.PathSeparator)) && target != dest {
			return fmt.Errorf("更新包路径越界：%s", entry.Name)
		}
		mode := entry.Mode()
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if !mode.IsRegular() {
			return fmt.Errorf("更新包包含不支持的文件类型：%s", entry.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		perm := os.FileMode(0o644)
		if mode&0o111 != 0 {
			perm = 0o755
		}
		if err := extractZipFile(entry, target, perm); err != nil {
			return err
		}
	}
	return nil
}

func extractZipFile(entry *zip.File, target string, perm os.FileMode) error {
	source, err := entry.Open()
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, source); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func extractTarGz(src, dest string) error {
	file, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("打开更新包失败: %w", err)
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("解压更新包失败: %w", err)
	}
	defer func() { _ = gz.Close() }()
	dest = filepath.Clean(dest)
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("读取更新包失败: %w", err)
		}
		name := filepath.Clean(header.Name)
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("更新包包含非法路径：%s", header.Name)
		}
		target := filepath.Join(dest, name)
		if !strings.HasPrefix(target, dest+string(os.PathSeparator)) && target != dest {
			return fmt.Errorf("更新包路径越界：%s", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			perm := os.FileMode(0o644)
			if header.Mode&0o111 != 0 {
				perm = 0o755
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, io.LimitReader(reader, maxDownloadBytes)); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("更新包包含不支持的文件类型：%s", header.Name)
		}
	}
	return nil
}

func findAppBundle(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".app") {
			return filepath.Join(root, entry.Name()), nil
		}
	}
	// 允许 zip 内再套一层目录
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		nested, err := os.ReadDir(filepath.Join(root, entry.Name()))
		if err != nil {
			continue
		}
		for _, child := range nested {
			if child.IsDir() && strings.HasSuffix(child.Name(), ".app") {
				return filepath.Join(root, entry.Name(), child.Name()), nil
			}
		}
	}
	return "", errors.New("更新包中未找到 .app")
}

func validateBundle(bundle, wantVersion string) error {
	plist := filepath.Join(bundle, "Contents", "Info.plist")
	if _, err := os.Stat(plist); err != nil {
		return errors.New("更新包不是有效的 App（缺少 Info.plist）")
	}
	executable := filepath.Join(bundle, "Contents", "MacOS", "ykt")
	if info, err := os.Stat(executable); err != nil || info.IsDir() {
		return errors.New("更新包缺少可执行文件")
	}
	gotVersion, err := plistValue(plist, "CFBundleShortVersionString")
	if err != nil {
		return err
	}
	if normalizeVersion(gotVersion) != normalizeVersion(wantVersion) {
		return fmt.Errorf("更新包版本不匹配：期望 %s，实际 %s", normalizeVersion(wantVersion), normalizeVersion(gotVersion))
	}
	identifier, err := plistValue(plist, "CFBundleIdentifier")
	if err != nil {
		return err
	}
	if identifier != "com.wails.ykt" {
		return fmt.Errorf("更新包标识不匹配：%s", identifier)
	}
	return nil
}

func plistValue(plist, key string) (string, error) {
	out, err := exec.Command("plutil", "-extract", key, "raw", "-o", "-", plist).Output()
	if err != nil {
		return "", fmt.Errorf("读取 Info.plist 失败（%s）", key)
	}
	return strings.TrimSpace(string(out)), nil
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

func isHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// normalizeVersion 去掉前缀 v 与预发布后缀。
func normalizeVersion(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "v"))
	if idx := strings.IndexAny(value, "-+"); idx >= 0 {
		value = value[:idx]
	}
	return value
}

// versionGreater 报告 a 是否严格大于 b（按数字段比较）。
func versionGreater(a, b string) bool {
	return compareVersions(a, b) > 0
}

func compareVersions(a, b string) int {
	left := strings.Split(normalizeVersion(a), ".")
	right := strings.Split(normalizeVersion(b), ".")
	length := len(left)
	if len(right) > length {
		length = len(right)
	}
	for i := 0; i < length; i++ {
		lv, rv := 0, 0
		if i < len(left) {
			lv, _ = strconv.Atoi(strings.TrimSpace(left[i]))
		}
		if i < len(right) {
			rv, _ = strconv.Atoi(strings.TrimSpace(right[i]))
		}
		if lv > rv {
			return 1
		}
		if lv < rv {
			return -1
		}
	}
	return 0
}
