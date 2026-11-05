//go:build !cli

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	goruntime "runtime"

	"github.com/songoao25/yuketang-jiaoben/internal/config"
	"github.com/songoao25/yuketang-jiaoben/internal/model"
)

// newTestApp 用临时 YKT_HOME 构造 App，绝不接触真实 ~/.ykt。
func newTestApp(t *testing.T) (*App, *config.Store) {
	t.Helper()
	source, err := os.ReadFile("yuketang.js")
	if err != nil {
		t.Fatalf("读取 yuketang.js 失败: %v", err)
	}
	store, err := config.OpenAt(t.TempDir())
	if err != nil {
		t.Fatalf("打开临时配置目录失败: %v", err)
	}
	app, err := NewApp(store, string(source), "1.0.0-test")
	if err != nil {
		t.Fatalf("NewApp 失败: %v", err)
	}
	t.Cleanup(func() {
		_, _ = app.StopService()
	})
	return app, store
}

// freePortInRange 返回端口区间内的一个空闲端口。
func freePortInRange(t *testing.T) int {
	t.Helper()
	for port := model.MinPort; port <= model.MaxPort; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return port
	}
	t.Skip("8899-8910 端口全部被占用，跳过")
	return 0
}

// requestHealth 请求本地 /health。
func requestHealth(t *testing.T, port int) model.Health {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		t.Fatalf("请求 /health 失败: %v", err)
	}
	defer resp.Body.Close()
	var health model.Health
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("解析 /health 失败: %v", err)
	}
	return health
}

func TestAppDefaultState(t *testing.T) {
	app, store := newTestApp(t)

	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState 失败: %v", err)
	}
	if state.Script.Version != "3.0.8" {
		t.Errorf("内置脚本版本 = %q，期望 3.0.8", state.Script.Version)
	}
	if state.Script.Hash == "" || state.Script.ShortHash == "" {
		t.Error("脚本指纹为空")
	}
	if state.Service.Running {
		t.Error("初始状态不应有运行中的服务")
	}
	if state.PortRange.Min != model.MinPort || state.PortRange.Max != model.MaxPort {
		t.Errorf("端口区间 = %+v", state.PortRange)
	}
	if state.Settings.Port < model.MinPort || state.Settings.Port > model.MaxPort {
		t.Errorf("默认端口越界: %d", state.Settings.Port)
	}
	if state.Settings.DefaultURL == "" || !state.Settings.SyncEnabled || !state.Settings.AutoStart {
		t.Errorf("默认设置不符合预期: %+v", state.Settings)
	}
	if state.Install.Installed {
		t.Error("首次不应有安装记录")
	}
	if state.HomeDir != store.HomeDir() {
		t.Errorf("HomeDir = %q，期望 %q", state.HomeDir, store.HomeDir())
	}
}

func TestAppServiceLifecycleAndConfig(t *testing.T) {
	app, store := newTestApp(t)

	state, err := app.StartService()
	if err != nil {
		t.Fatalf("StartService 失败: %v", err)
	}
	if !state.Service.Running {
		t.Fatal("StartService 后 Service.Running 应为 true")
	}
	port := state.Service.Port
	if port < model.MinPort || port > model.MaxPort {
		t.Errorf("服务端口 %d 不在 8899-8910 内", port)
	}
	health := requestHealth(t, port)
	if !health.OK || health.App != "ykt" {
		t.Errorf("health = %+v", health)
	}
	if health.Hash != state.Script.Hash {
		t.Errorf("health 指纹 %q != 脚本指纹 %q", health.Hash, state.Script.Hash)
	}
	if state.SyncURL == "" {
		t.Error("服务运行时 SyncURL 不应为空")
	}

	// 保存 AI 配置：应从内存状态可读、落盘 0600、HasUserConfig 为 true
	ai := model.AIConfig{
		URL:        "https://api.deepseek.com/chat/completions",
		Key:        "sk-test-secret",
		Model:      "deepseek-chat",
		APIFormat:  "openai",
		AuthMethod: "bearer",
	}
	state, err = app.SaveAIConfig(ai)
	if err != nil {
		t.Fatalf("SaveAIConfig 失败: %v", err)
	}
	if state.AI.Key != ai.Key || state.AI.Model != ai.Model {
		t.Errorf("AI 配置未生效: %+v", state.AI)
	}
	info, err := os.Stat(store.ConfigPath())
	if err != nil {
		t.Fatalf("config.json 不存在: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config.json 权限 = %v，期望 0600", info.Mode().Perm())
	}
	if !store.HasUserConfig() {
		t.Error("保存 AI 配置后 HasUserConfig 应为 true")
	}

	// 功能开关
	state, err = app.SaveFeatures(model.FeatureConfig{AutoAI: true, AutoComment: true})
	if err != nil {
		t.Fatalf("SaveFeatures 失败: %v", err)
	}
	if !state.Features.AutoAI || !state.Features.AutoComment {
		t.Errorf("功能开关未生效: %+v", state.Features)
	}

	// 日志
	logs, err := app.GetLogs(50)
	if err != nil {
		t.Fatalf("GetLogs 失败: %v", err)
	}
	if len(logs) == 0 {
		t.Error("服务启动后应有日志")
	}

	// 停止服务
	state, err = app.StopService()
	if err != nil {
		t.Fatalf("StopService 失败: %v", err)
	}
	if state.Service.Running {
		t.Error("StopService 后 Service.Running 应为 false")
	}
	if _, err := os.Stat(store.ServerPath()); !os.IsNotExist(err) {
		t.Errorf("停止后 server.json 应被清理，err=%v", err)
	}
}

func TestAppSaveSettingsRestartsService(t *testing.T) {
	app, _ := newTestApp(t)

	state, err := app.StartService()
	if err != nil {
		t.Fatalf("StartService 失败: %v", err)
	}
	oldPort := state.Service.Port

	// 找一个与当前不同的空闲端口
	var newPort int
	for port := model.MinPort; port <= model.MaxPort; port++ {
		if port == oldPort {
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		_ = ln.Close()
		newPort = port
		break
	}
	if newPort == 0 {
		t.Skip("区间内没有其它空闲端口，跳过")
	}

	settings := state.Settings
	settings.Port = newPort
	state, err = app.SaveSettings(settings)
	if err != nil {
		t.Fatalf("SaveSettings 失败: %v", err)
	}
	if !state.Service.Running {
		t.Fatal("修改端口后服务应保持运行")
	}
	if state.Service.Port != newPort {
		t.Errorf("修改端口后服务端口 = %d，期望 %d", state.Service.Port, newPort)
	}
	if got := requestHealth(t, newPort); !got.OK {
		t.Errorf("新端口 health 失败: %+v", got)
	}

	// 外观设置保存
	settings.Appearance = "dark"
	state, err = app.SaveSettings(settings)
	if err != nil {
		t.Fatalf("SaveSettings(appearance) 失败: %v", err)
	}
	if state.Settings.Appearance != "dark" {
		t.Errorf("外观设置未生效: %q", state.Settings.Appearance)
	}
}

func TestAppValidation(t *testing.T) {
	app, _ := newTestApp(t)

	if _, err := app.SaveAIConfig(model.AIConfig{URL: "ftp://bad.example"}); err == nil {
		t.Error("非 http(s) 的 AI 地址应被拒绝")
	}
	if _, err := app.SaveAIConfig(model.AIConfig{APIFormat: "gemini"}); err == nil {
		t.Error("未知 API 格式应被拒绝")
	}
	settings := model.Settings{Port: 80, DefaultURL: model.DefaultURL, Appearance: "system"}
	if _, err := app.SaveSettings(settings); err == nil {
		t.Error("越界端口应被拒绝")
	}
	if _, err := app.SaveSettings(model.Settings{Port: model.MinPort, DefaultURL: "file:///etc/passwd", Appearance: "system"}); err == nil {
		t.Error("非 http(s) 默认网址应被拒绝")
	}
}

// TestAppConfigSyncedToServer 验证网页脚本通过本地服务拿到的配置与 App 保存的一致。
func TestAppConfigSyncedToServer(t *testing.T) {
	app, _ := newTestApp(t)
	state, err := app.StartService()
	if err != nil {
		t.Fatalf("StartService 失败: %v", err)
	}
	if _, err := app.SaveAIConfig(model.AIConfig{
		URL:        "https://api.deepseek.com/chat/completions",
		Key:        "sk-sync",
		Model:      "deepseek-chat",
		APIFormat:  "openai",
		AuthMethod: "bearer",
	}); err != nil {
		t.Fatalf("SaveAIConfig 失败: %v", err)
	}
	if _, err := app.SaveFeatures(model.FeatureConfig{AutoAI: true}); err != nil {
		t.Fatalf("SaveFeatures 失败: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/api/config", state.Service.Port)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-YKT-Client", "userscript")
	req.Header.Set("Origin", "https://www.yuketang.cn")
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("拉取 /api/config 失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/config 状态码 = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://www.yuketang.cn" {
		t.Errorf("CORS 头 = %q", got)
	}
	var sync model.SyncConfig
	if err := json.NewDecoder(resp.Body).Decode(&sync); err != nil {
		t.Fatalf("解析 SyncConfig 失败: %v", err)
	}
	if !sync.OK || sync.App != "ykt" || !sync.Sync.Enabled || !sync.Configured {
		t.Errorf("SyncConfig 基本字段不符: %+v", sync)
	}
	if sync.AI.Key != "sk-sync" || !sync.Features.AutoAI {
		t.Errorf("同步配置内容不符: %+v", sync)
	}
	if at := state.LastSyncAt; at != 0 {
		t.Logf("提示：LastSyncAt 在拉取后应由下一步状态刷新（当前快照 %d）", at)
	}
	state, _ = app.GetState()
	if state.LastSyncAt == 0 {
		t.Error("拉取 /api/config 后 LastSyncAt 应更新")
	}

	// 未带客户端头的请求必须被拒绝
	resp2, err := client.Get(url)
	if err != nil {
		t.Fatalf("无头请求失败: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("无 X-YKT-Client 头的状态码 = %d，期望 403", resp2.StatusCode)
	}

	// 清理测试产生的日志文件
	_ = os.WriteFile(filepath.Join(app.store.HomeDir(), "server.log"), nil, 0o600)
}

// TestAppUpdateCheck 验证更新检查绑定与缓存；不执行真实下载/替换。
func TestAppUpdateCheck(t *testing.T) {
	if goruntime.GOOS != "darwin" {
		t.Skip("更新资产命名按 darwin 测试")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name":     "v9.9.9",
				"body":         "测试版本",
				"published_at": "2026-11-05T00:00:00Z",
				"assets": []map[string]any{
					{"name": "ykt-darwin-arm64.zip", "browser_download_url": "http://" + r.Host + "/download", "size": 100},
					{"name": "checksums.txt", "browser_download_url": "http://" + r.Host + "/checksums.txt"},
				},
			})
		case "/checksums.txt":
			_, _ = w.Write([]byte(strings.Repeat("a", 64) + "  ykt-darwin-arm64.zip\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("YKT_UPDATE_API_URL", server.URL+"/releases/latest")
	t.Setenv("YKT_UPDATE_DISABLE", "")

	app, _ := newTestApp(t)
	info, err := app.CheckForUpdates()
	if err != nil {
		t.Fatalf("CheckForUpdates 失败: %v", err)
	}
	if !info.Available || info.LatestVersion != "9.9.9" || info.Checksum == "" {
		t.Fatalf("UpdateInfo = %+v", info)
	}
	cached, err := app.GetUpdateInfo()
	if err != nil || cached.LatestVersion != "9.9.9" {
		t.Fatalf("GetUpdateInfo = %+v, %v", cached, err)
	}

	t.Setenv("YKT_UPDATE_DISABLE", "1")
	if _, err := app.CheckForUpdates(); err == nil {
		t.Fatal("YKT_UPDATE_DISABLE=1 时应拒绝检查")
	}
}

// TestAppFinishOnboarding 验证新手引导完成状态会被持久化。
func TestAppFinishOnboarding(t *testing.T) {
	app, store := newTestApp(t)
	state, err := app.FinishOnboarding()
	if err != nil {
		t.Fatalf("FinishOnboarding 失败: %v", err)
	}
	if !state.Settings.OnboardingDone {
		t.Fatal("返回状态应标记 onboardingDone")
	}
	if !store.LoadSettings().OnboardingDone {
		t.Fatal("onboardingDone 未持久化")
	}
	app2, err := NewApp(store, mustReadScript(t), "1.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	state2, err := app2.GetState()
	if err != nil || !state2.Settings.OnboardingDone {
		t.Fatalf("重新加载后 onboardingDone 丢失: %+v", state2.Settings)
	}
}

func mustReadScript(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("yuketang.js")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
