package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/songoao25/yuketang-jiaoben/internal/config"
	"github.com/songoao25/yuketang-jiaoben/internal/model"
	"github.com/songoao25/yuketang-jiaoben/internal/script"
)

const serverTestScript = `// ==UserScript==
// @name         测试脚本
// @version      9.9.9
// @author       测试作者
// @match        *://*.yuketang.cn/*
// @match        *://*.gdufemooc.cn/*
// ==/UserScript==

(() => { console.log('server test'); })();
`

const testAppVersion = "1.0.0-test"

// testHTTPClient 是本机测试用的 HTTP 客户端（显式禁用代理）。
var testHTTPClient = &http.Client{
	Timeout:   5 * time.Second,
	Transport: &http.Transport{Proxy: nil},
}

// newTempStore 创建临时配置目录。
func newTempStore(t *testing.T) *config.Store {
	t.Helper()
	store, err := config.OpenAt(t.TempDir())
	if err != nil {
		t.Fatalf("OpenAt 失败: %v", err)
	}
	return store
}

// portAvailable 探测 127.0.0.1 上的端口是否空闲。
func portAvailable(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// pickFreePorts 返回 [MinPort, MaxPort] 内连续 count 个空闲端口；不够时跳过测试。
func pickFreePorts(t *testing.T, count int) []int {
	t.Helper()
	run := make([]int, 0, count)
	for port := model.MinPort; port <= model.MaxPort; port++ {
		if portAvailable(port) {
			run = append(run, port)
			if len(run) == count {
				return run
			}
			continue
		}
		run = run[:0]
	}
	t.Skipf("端口 %d~%d 内没有连续 %d 个空闲端口，跳过测试", model.MinPort, model.MaxPort, count)
	return nil
}

// startTestServer 启动测试服务，并在测试结束时停止。
func startTestServer(t *testing.T) (*config.Store, *Server) {
	t.Helper()
	store := newTempStore(t)
	ports := pickFreePorts(t, 1)
	srv := New(Options{
		Store:        store,
		ScriptSource: serverTestScript,
		AppVersion:   testAppVersion,
		Port:         ports[0],
	})
	port, err := srv.Start()
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if port < model.MinPort || port > model.MaxPort {
		t.Fatalf("端口 %d 超出约定区间", port)
	}
	t.Cleanup(func() {
		if err := srv.Stop(); err != nil {
			t.Errorf("Stop 失败: %v", err)
		}
	})
	return store, srv
}

// httpGet 发起 GET 请求并读取完整响应体。
func httpGet(t *testing.T, rawURL string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := testHTTPClient.Do(request)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", rawURL, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}
	return response, body
}

func mustMeta(t *testing.T) model.ScriptMeta {
	t.Helper()
	meta, err := script.FromSource(serverTestScript)
	if err != nil {
		t.Fatalf("解析测试脚本失败: %v", err)
	}
	return meta
}

func TestStartStopLifecycle(t *testing.T) {
	store, srv := startTestServer(t)
	port := srv.Port()

	if !srv.Running() {
		t.Fatal("Start 后 Running 应为 true")
	}
	if got, want := srv.URL(), fmt.Sprintf("http://127.0.0.1:%d/", port); got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if got, want := srv.InstallURL(), fmt.Sprintf("http://127.0.0.1:%d/ykt.user.js", port); got != want {
		t.Fatalf("InstallURL = %q, want %q", got, want)
	}
	if srv.StartedAt() <= 0 {
		t.Fatal("StartedAt 应大于 0")
	}

	info, ok := store.LoadServerInfo()
	if !ok {
		t.Fatal("Start 后应写入 server.json")
	}
	if info.PID != os.Getpid() || info.Port != port || info.Version != testAppVersion {
		t.Fatalf("server.json 内容异常: %+v", info)
	}

	status, ok := DetectRunning(store)
	if !ok || !status.Running {
		t.Fatalf("DetectRunning = %+v/%v, want running", status, ok)
	}
	if status.Port != port || status.PID != os.Getpid() {
		t.Fatalf("DetectRunning 状态异常: %+v", status)
	}

	health, ok := CheckHealth(port, time.Second)
	if !ok {
		t.Fatal("CheckHealth 应成功")
	}
	meta := mustMeta(t)
	if health.App != "ykt" || health.Port != port || health.Version != testAppVersion || health.Hash != meta.Hash {
		t.Fatalf("health 内容异常: %+v", health)
	}

	// 重复 Start 幂等。
	if again, err := srv.Start(); err != nil || again != port {
		t.Fatalf("重复 Start = %d/%v, want %d/nil", again, err, port)
	}

	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	if srv.Running() {
		t.Fatal("Stop 后 Running 应为 false")
	}
	if _, ok := store.LoadServerInfo(); ok {
		t.Fatal("Stop 后应清理 server.json")
	}
	if _, ok := CheckHealth(port, 300*time.Millisecond); ok {
		t.Fatal("Stop 后健康检查应失败")
	}
	if _, ok := DetectRunning(store); ok {
		t.Fatal("Stop 后 DetectRunning 应为 false")
	}
	if err := srv.Stop(); err != nil {
		t.Fatalf("重复 Stop 不应报错: %v", err)
	}

	logs := srv.Logs()
	if len(logs) == 0 {
		t.Fatal("生命周期应产生日志")
	}
	found := false
	for _, entry := range logs {
		if strings.Contains(entry.Message, "本地服务已停止") {
			found = true
		}
	}
	if !found {
		t.Fatalf("日志缺少停止记录: %+v", logs)
	}
}

func TestPortFallbackWhenPreferredBusy(t *testing.T) {
	ports := pickFreePorts(t, 2)
	store := newTempStore(t)

	blocker, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", ports[0]))
	if err != nil {
		t.Fatalf("占用端口失败: %v", err)
	}
	defer func() { _ = blocker.Close() }()

	srv := New(Options{
		Store:        store,
		ScriptSource: serverTestScript,
		AppVersion:   testAppVersion,
		Port:         ports[0],
	})
	actual, err := srv.Start()
	if err != nil {
		t.Fatalf("Start 应顺延到空闲端口: %v", err)
	}
	defer func() { _ = srv.Stop() }()
	if actual != ports[1] {
		t.Fatalf("端口顺延结果 = %d, want %d", actual, ports[1])
	}
}

func TestStartWithoutStore(t *testing.T) {
	srv := New(Options{ScriptSource: serverTestScript, Port: model.MinPort})
	if _, err := srv.Start(); err == nil {
		t.Fatal("缺少 Store 时 Start 应报错")
	}
}

func TestHTTPRoutes(t *testing.T) {
	store, srv := startTestServer(t)
	base := strings.TrimSuffix(srv.URL(), "/")
	meta := mustMeta(t)

	t.Run("health", func(t *testing.T) {
		response, body := httpGet(t, base+"/health", nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("状态码 = %d, want 200", response.StatusCode)
		}
		assertNoStore(t, response)
		if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("Content-Type = %q", got)
		}
		var health model.Health
		if err := json.Unmarshal(body, &health); err != nil {
			t.Fatalf("health JSON 解析失败: %v", err)
		}
		if !health.OK || health.App != "ykt" || health.Port != srv.Port() {
			t.Fatalf("health 内容异常: %+v", health)
		}
		if health.Hash != meta.Hash {
			t.Fatalf("health.Hash = %q, want %q", health.Hash, meta.Hash)
		}
		if health.File == "" {
			t.Fatal("health.File 不应为空")
		}
	})

	t.Run("script", func(t *testing.T) {
		for _, path := range []string{"/ykt.user.js", "/yuketang.js", "/script"} {
			response, body := httpGet(t, base+path, nil)
			if response.StatusCode != http.StatusOK {
				t.Fatalf("%s 状态码 = %d, want 200", path, response.StatusCode)
			}
			assertNoStore(t, response)
			if got := response.Header.Get("Content-Type"); got != "application/javascript; charset=utf-8" {
				t.Fatalf("%s Content-Type = %q", path, got)
			}
			if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("%s 缺少 nosniff 安全头: %q", path, got)
			}
			if string(body) != serverTestScript {
				t.Fatalf("%s 返回内容与脚本不一致", path)
			}
		}

		state := store.LoadInstallState()
		if !state.Installed || state.Hash != meta.Hash || state.Version != meta.Version {
			t.Fatalf("脚本 GET 后安装记录异常: %+v", state)
		}
		if state.OpenedAt <= 0 {
			t.Fatalf("安装记录缺少时间: %+v", state)
		}
	})

	t.Run("index", func(t *testing.T) {
		response, body := httpGet(t, base+"/", nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("状态码 = %d, want 200", response.StatusCode)
		}
		assertNoStore(t, response)
		if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Fatalf("Content-Type = %q", got)
		}
		for _, want := range []string{meta.Name, meta.Version, meta.ShortHash, "/ykt.user.js"} {
			if !strings.Contains(string(body), want) {
				t.Fatalf("首页缺少 %q", want)
			}
		}
		// /index.html 同样返回首页。
		indexResponse, _ := httpGet(t, base+"/index.html", nil)
		if indexResponse.StatusCode != http.StatusOK {
			t.Fatalf("/index.html 状态码 = %d", indexResponse.StatusCode)
		}
	})

	t.Run("favicon", func(t *testing.T) {
		response, _ := httpGet(t, base+"/favicon.ico", nil)
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("状态码 = %d, want 204", response.StatusCode)
		}
		assertNoStore(t, response)
	})

	t.Run("notFound", func(t *testing.T) {
		response, _ := httpGet(t, base+"/not-exist", nil)
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("状态码 = %d, want 404", response.StatusCode)
		}
	})
}

func TestAPIConfigSecurity(t *testing.T) {
	store, srv := startTestServer(t)
	url := strings.TrimSuffix(srv.URL(), "/") + "/api/config"
	meta := mustMeta(t)

	t.Run("missingClientHeader", func(t *testing.T) {
		response, body := httpGet(t, url, nil)
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("状态码 = %d, want 403", response.StatusCode)
		}
		assertNoStore(t, response)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("403 响应不是 JSON: %v (%s)", err, body)
		}
		if payload["ok"] != false || payload["error"] != "forbidden" {
			t.Fatalf("403 响应内容异常: %v", payload)
		}
	})

	clientHeader := map[string]string{"X-YKT-Client": "userscript"}

	t.Run("defaultResponse", func(t *testing.T) {
		response, body := httpGet(t, url, clientHeader)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("状态码 = %d, want 200", response.StatusCode)
		}
		assertNoStore(t, response)
		var payload model.SyncConfig
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("SyncConfig 解析失败: %v", err)
		}
		if !payload.OK || payload.App != "ykt" || payload.Version != testAppVersion {
			t.Fatalf("响应头信息异常: %+v", payload)
		}
		if !payload.Sync.Enabled {
			t.Fatal("默认应开启同步")
		}
		if payload.Configured {
			t.Fatal("尚未保存 AI/功能配置时 Configured 应为 false")
		}
		if payload.AI != (model.AIConfig{}) || payload.Features != (model.FeatureConfig{}) {
			t.Fatalf("未保存配置时应返回零值: %+v", payload)
		}
		if srv.LastConfigSync() <= 0 {
			t.Fatal("成功拉取后 LastConfigSync 应更新")
		}
	})

	t.Run("configuredConfig", func(t *testing.T) {
		ai := model.AIConfig{
			URL:        "https://api.example.com/v1",
			Key:        "sk-test-key",
			Model:      "test-model",
			APIFormat:  "openai",
			AuthMethod: "bearer",
		}
		features := model.FeatureConfig{AutoAI: true, AutoComment: true}
		if err := store.SaveAIConfig(ai); err != nil {
			t.Fatalf("保存 AI 配置失败: %v", err)
		}
		if err := store.SaveFeatures(features); err != nil {
			t.Fatalf("保存功能开关失败: %v", err)
		}
		_, body := httpGet(t, url, clientHeader)
		var payload model.SyncConfig
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("SyncConfig 解析失败: %v", err)
		}
		if !payload.Configured {
			t.Fatal("保存配置后 Configured 应为 true")
		}
		if payload.AI != ai || payload.Features != features {
			t.Fatalf("同步内容异常: %+v", payload)
		}
	})

	t.Run("syncDisabledHidesKey", func(t *testing.T) {
		settings := store.LoadSettings()
		settings.SyncEnabled = false
		if err := store.SaveSettings(settings); err != nil {
			t.Fatalf("保存设置失败: %v", err)
		}
		_, body := httpGet(t, url, clientHeader)
		var payload model.SyncConfig
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("SyncConfig 解析失败: %v", err)
		}
		if payload.Sync.Enabled {
			t.Fatal("关闭同步后 sync.enabled 应为 false")
		}
		if payload.AI.Key != "" || payload.AI.URL != "" {
			t.Fatalf("关闭同步后不得返回 AI 配置: %+v", payload.AI)
		}
		if payload.Features.AutoAI || payload.Features.AutoComment {
			t.Fatalf("关闭同步后不得返回功能开关: %+v", payload.Features)
		}
		if !payload.Configured {
			t.Fatal("关闭同步不应改变 Configured")
		}
	})

	t.Run("cors", func(t *testing.T) {
		allowed := []string{
			"https://yuketang.cn",
			"https://www.yuketang.cn",
			"https://changjiang.yuketang.cn",
			"https://www.gdufemooc.cn",
			"http://127.0.0.1:8899",
			"http://localhost:3000",
		}
		for _, origin := range allowed {
			headers := map[string]string{"X-YKT-Client": "userscript", "Origin": origin}
			response, _ := httpGet(t, url, headers)
			if got := response.Header.Get("Access-Control-Allow-Origin"); got != origin {
				t.Fatalf("Origin %q 的 ACAO = %q, want 原样回写", origin, got)
			}
			if response.Header.Get("Vary") == "" {
				t.Fatalf("Origin %q 缺少 Vary: Origin", origin)
			}
		}
		denied := []string{
			"https://evil.example.com",
			"http://www.yuketang.cn",
			"https://yuketang.cn.evil.com",
			"https://evil-gdufemooc.cn",
			"null",
		}
		for _, origin := range denied {
			headers := map[string]string{"X-YKT-Client": "userscript", "Origin": origin}
			response, _ := httpGet(t, url, headers)
			if response.StatusCode != http.StatusOK {
				t.Fatalf("Origin %q 请求状态码 = %d", origin, response.StatusCode)
			}
			if got := response.Header.Get("Access-Control-Allow-Origin"); got != "" {
				t.Fatalf("Origin %q 不应回写 ACAO，实际 %q", origin, got)
			}
		}
	})

	if meta.Hash == "" {
		t.Fatal("测试脚本指纹为空")
	}
}

func TestConfigSyncLogDeduped(t *testing.T) {
	_, srv := startTestServer(t)
	url := strings.TrimSuffix(srv.URL(), "/") + "/api/config"
	headers := map[string]string{"X-YKT-Client": "userscript"}
	for i := 0; i < 3; i++ {
		httpGet(t, url, headers)
	}
	count := 0
	for _, entry := range srv.Logs() {
		if strings.Contains(entry.Message, "配置已同步") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("5 秒内相同日志应去重，实际出现 %d 次", count)
	}
}

func TestLogsRingAndClear(t *testing.T) {
	store, srv := startTestServer(t)
	for i := 0; i < 260; i++ {
		srv.log("info", fmt.Sprintf("测试日志 %d", i))
	}
	logs := srv.Logs()
	if len(logs) != logLimit {
		t.Fatalf("环形缓冲长度 = %d, want %d", len(logs), logLimit)
	}
	if logs[len(logs)-1].Message != "测试日志 259" {
		t.Fatalf("环形缓冲最后一条 = %q", logs[len(logs)-1].Message)
	}

	fileEntries := ReadLogFile(store.LogPath(), 5)
	if len(fileEntries) != 5 {
		t.Fatalf("ReadLogFile 条数 = %d, want 5", len(fileEntries))
	}
	if fileEntries[4].Message != "测试日志 259" {
		t.Fatalf("ReadLogFile 最后一条 = %q", fileEntries[4].Message)
	}

	info, err := os.Stat(store.LogPath())
	if err != nil {
		t.Fatalf("server.log 不存在: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("server.log 权限 = %#o, want 0600", got)
	}

	srv.ClearLogs()
	if len(srv.Logs()) != 0 {
		t.Fatal("ClearLogs 后内存日志应为空")
	}
}

func TestReadLogFileSkipsBadLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	content := `{"time":1,"level":"info","message":"a"}
not-json
{"time":2,"level":"warn","message":"b"}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入测试日志失败: %v", err)
	}
	entries := ReadLogFile(path, 10)
	if len(entries) != 2 {
		t.Fatalf("ReadLogFile = %+v, want 2 条", entries)
	}
	if entries[0].Message != "a" || entries[1].Message != "b" {
		t.Fatalf("日志内容异常: %+v", entries)
	}
	if got := ReadLogFile(filepath.Join(t.TempDir(), "missing.log"), 10); got != nil {
		t.Fatalf("缺失日志文件应返回 nil, got %+v", got)
	}
}

func TestCheckHealthUnreachable(t *testing.T) {
	ports := pickFreePorts(t, 1)
	if _, ok := CheckHealth(ports[0], 200*time.Millisecond); ok {
		t.Fatalf("端口 %d 未监听时 CheckHealth 应为 false", ports[0])
	}
	if _, ok := CheckHealth(0, time.Second); ok {
		t.Fatal("端口 0 应返回 false")
	}
}

func TestDetectRunningWithoutRecord(t *testing.T) {
	store := newTempStore(t)
	if _, ok := DetectRunning(store); ok {
		t.Fatal("没有 server.json 时 DetectRunning 应为 false")
	}
	if _, ok := DetectRunning(nil); ok {
		t.Fatal("nil store 时 DetectRunning 应为 false")
	}

	// 过期记录（进程已不存在）应判定为未运行。
	ports := pickFreePorts(t, 1)
	info := model.ServerInfo{PID: 1 << 30, Port: ports[0], StartedAt: time.Now().UnixMilli()}
	if err := store.SaveServerInfo(info); err != nil {
		t.Fatalf("写入过期记录失败: %v", err)
	}
	if _, ok := DetectRunning(store); ok {
		t.Fatal("过期记录时 DetectRunning 应为 false")
	}
}

func TestKillPIDNonexistent(t *testing.T) {
	if err := KillPID(1 << 30); err != nil {
		t.Fatalf("终止不存在的进程应返回 nil: %v", err)
	}
	if err := KillPID(0); err != nil {
		t.Fatalf("PID 0 应返回 nil: %v", err)
	}
	if err := KillPID(os.Getpid()); err != nil {
		t.Fatalf("当前进程 PID 应返回 nil: %v", err)
	}
}

func TestIsAllowedOrigin(t *testing.T) {
	tests := []struct {
		origin string
		want   bool
	}{
		{"", false},
		{"https://yuketang.cn", true},
		{"https://www.yuketang.cn", true},
		{"https://changjiang.yuketang.cn", true},
		{"https://www.gdufemooc.cn", true},
		{"http://127.0.0.1:8899", true},
		{"http://localhost:3000", true},
		{"http://localhost", true}, // 默认端口 80，仍属本机白名单
		{"http://www.yuketang.cn", false},
		{"https://yuketang.cn.evil.com", false},
		{"https://evil.com", false},
		{"https://gdufemooc.cn.evil.com", false},
		{"ftp://yuketang.cn", false},
		{"not a url", false},
	}
	for _, tt := range tests {
		if got := isAllowedOrigin(tt.origin); got != tt.want {
			t.Fatalf("isAllowedOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
		}
	}
}

// assertNoStore 校验 Cache-Control 与基础安全头。
func assertNoStore(t *testing.T, response *http.Response) {
	t.Helper()
	if got := response.Header.Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("Cache-Control = %q, want 含 no-store", got)
	}
}

// ---------------------------------------------------------------- QA M2/M3/M5/L6/L7/L8 回归

func TestOwnerRecordedAndReported(t *testing.T) {
	t.Run("默认记录为 daemon", func(t *testing.T) {
		store, _ := startTestServer(t)
		info, ok := store.LoadServerInfo()
		if !ok {
			t.Fatal("server.json 不存在")
		}
		if info.Owner != OwnerDaemon {
			t.Fatalf("默认 owner = %q, want %q", info.Owner, OwnerDaemon)
		}
		status, ok := DetectRunning(store)
		if !ok || status.Owner != OwnerDaemon {
			t.Fatalf("DetectRunning = %+v/%v, want owner=daemon", status, ok)
		}
	})

	t.Run("GUI 服务记录为 gui", func(t *testing.T) {
		store := newTempStore(t)
		ports := pickFreePorts(t, 1)
		srv := New(Options{
			Store:        store,
			ScriptSource: serverTestScript,
			AppVersion:   testAppVersion,
			Port:         ports[0],
			Owner:        "gui",
		})
		if _, err := srv.Start(); err != nil {
			t.Fatalf("Start 失败: %v", err)
		}
		defer func() { _ = srv.Stop() }()

		info, ok := store.LoadServerInfo()
		if !ok || info.Owner != OwnerGUI {
			t.Fatalf("server.json owner = %q, want gui", info.Owner)
		}
		status, ok := DetectRunning(store)
		if !ok || status.Owner != OwnerGUI {
			t.Fatalf("DetectRunning = %+v/%v, want owner=gui", status, ok)
		}
	})

	t.Run("未知 owner 归一为 daemon", func(t *testing.T) {
		srv := New(Options{Owner: "unknown"})
		if srv.opts.Owner != OwnerDaemon {
			t.Fatalf("未知 owner 未归一: %q", srv.opts.Owner)
		}
	})
}

// startFakeHealth 启动一个返回指定 JSON 的假 /health 服务，返回端口。
func startFakeHealth(t *testing.T, payload any) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)
	addr, ok := srv.Listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("无法获取假服务端口: %v", srv.Listener.Addr())
	}
	return addr.Port
}

func TestCheckHealthValidatesApp(t *testing.T) {
	cases := []struct {
		name    string
		payload any
		want    bool
	}{
		{"非 ykt 服务", map[string]any{"ok": true, "app": "other"}, false},
		{"缺少 app 字段", map[string]any{"ok": true}, false},
		{"ok=false", map[string]any{"ok": false, "app": "ykt"}, false},
		{"真正的 ykt", map[string]any{"ok": true, "app": "ykt", "pid": 12345, "port": 8899}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			port := startFakeHealth(t, tt.payload)
			if _, ok := CheckHealth(port, time.Second); ok != tt.want {
				t.Fatalf("CheckHealth = %v, want %v", ok, tt.want)
			}
		})
	}
}

func TestDetectRunningPIDMismatchClearsStaleRecord(t *testing.T) {
	store, srv := startTestServer(t)
	info, ok := store.LoadServerInfo()
	if !ok {
		t.Fatal("server.json 不存在")
	}
	// 记录里的 PID 与 /health 自报的不一致：视为过期记录。
	stale := info
	stale.PID = info.PID + 1<<20
	if err := store.SaveServerInfo(stale); err != nil {
		t.Fatalf("写入过期记录失败: %v", err)
	}
	if status, running := DetectRunning(store); running {
		t.Fatalf("PID 不一致的记录不应视为运行中: %+v", status)
	}
	if _, ok := store.LoadServerInfo(); ok {
		t.Fatal("PID 不一致的过期记录应被清理")
	}
	if !srv.Running() {
		t.Fatal("清理过期记录不应影响真实服务")
	}
}

func TestAPIConfigSectionFlags(t *testing.T) {
	store, srv := startTestServer(t)
	url := strings.TrimSuffix(srv.URL(), "/") + "/api/config"
	fetch := func() model.SyncConfig {
		t.Helper()
		_, body := httpGet(t, url, map[string]string{"X-YKT-Client": "userscript"})
		var payload model.SyncConfig
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("SyncConfig 解析失败: %v", err)
		}
		return payload
	}

	payload := fetch()
	if payload.HasAI || payload.HasFeatures || payload.Configured {
		t.Fatalf("初始不应有任何配置段: %+v", payload)
	}

	ai := model.AIConfig{URL: "https://api.example.com/v1", Key: "sk-section-key", Model: "m", APIFormat: "openai", AuthMethod: "bearer"}
	if err := store.SaveAIConfig(ai); err != nil {
		t.Fatalf("SaveAIConfig 失败: %v", err)
	}
	payload = fetch()
	if !payload.HasAI {
		t.Fatal("只保存 AI 时 hasAI 应为 true")
	}
	if payload.HasFeatures {
		t.Fatal("只保存 AI 时 hasFeatures 应为 false（M3 分段覆盖）")
	}
	if !payload.Configured {
		t.Fatal("保存 AI 后 configured 应为 true")
	}
	if payload.AI != ai {
		t.Fatalf("AI 配置未下发: %+v", payload.AI)
	}

	features := model.FeatureConfig{AutoAI: true, AutoComment: true}
	if err := store.SaveFeatures(features); err != nil {
		t.Fatalf("SaveFeatures 失败: %v", err)
	}
	payload = fetch()
	if !payload.HasAI || !payload.HasFeatures || !payload.Configured {
		t.Fatalf("保存两段后标志位不符: %+v", payload)
	}
	if payload.Features != features {
		t.Fatalf("功能开关未下发: %+v", payload.Features)
	}

	// 同步关闭：hasAI/hasFeatures/configured 仍反映配置存在，但不泄露内容。
	settings := store.LoadSettings()
	settings.SyncEnabled = false
	if err := store.SaveSettings(settings); err != nil {
		t.Fatalf("SaveSettings 失败: %v", err)
	}
	payload = fetch()
	if payload.Sync.Enabled {
		t.Fatal("同步关闭后 sync.enabled 应为 false")
	}
	if !payload.HasAI || !payload.HasFeatures || !payload.Configured {
		t.Fatalf("同步关闭不应改变 hasAI/hasFeatures/configured: %+v", payload)
	}
	if payload.AI.Key != "" || payload.AI.URL != "" || payload.Features.AutoAI {
		t.Fatalf("同步关闭时不得泄露配置内容: %+v", payload)
	}
}

func TestAPIConfigOptionsPreflight(t *testing.T) {
	_, srv := startTestServer(t)
	url := strings.TrimSuffix(srv.URL(), "/") + "/api/config"
	do := func(method, origin string, headers map[string]string) *http.Response {
		t.Helper()
		request, err := http.NewRequest(method, url, nil)
		if err != nil {
			t.Fatalf("构造请求失败: %v", err)
		}
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, err := testHTTPClient.Do(request)
		if err != nil {
			t.Fatalf("%s %s 请求失败: %v", method, origin, err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return response
	}

	response := do(http.MethodOptions, "https://www.yuketang.cn", nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("OPTIONS 状态码 = %d, want 204", response.StatusCode)
	}
	assertNoStore(t, response)
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "https://www.yuketang.cn" {
		t.Fatalf("预检 ACAO = %q", got)
	}
	methods := response.Header.Get("Access-Control-Allow-Methods")
	if !strings.Contains(methods, "GET") || !strings.Contains(methods, "OPTIONS") {
		t.Fatalf("Allow-Methods = %q", methods)
	}
	if got := response.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-YKT-Client") {
		t.Fatalf("Allow-Headers = %q, want 含 X-YKT-Client", got)
	}

	response = do(http.MethodOptions, "https://evil.example.com", nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("非白名单 Origin 预检状态码 = %d, want 403", response.StatusCode)
	}
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("非白名单 Origin 预检不应回写 ACAO: %q", got)
	}

	response = do(http.MethodOptions, "", nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("无 Origin 的 OPTIONS 状态码 = %d, want 204", response.StatusCode)
	}

	// 非 GET/OPTIONS 仍返回 405。
	response = do(http.MethodPost, "", map[string]string{"X-YKT-Client": "userscript"})
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST 状态码 = %d, want 405", response.StatusCode)
	}

	// 预检支持不影响普通 GET。
	response = do(http.MethodGet, "https://www.yuketang.cn", map[string]string{"X-YKT-Client": "userscript"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET 状态码 = %d, want 200", response.StatusCode)
	}
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "https://www.yuketang.cn" {
		t.Fatalf("GET ACAO = %q", got)
	}
}

func TestLogDedupeUsesLevelAndMessage(t *testing.T) {
	_, srv := startTestServer(t)
	const message = "QA-L6 相同文本"
	countMessage := func() int {
		count := 0
		for _, entry := range srv.Logs() {
			if entry.Message == message {
				count++
			}
		}
		return count
	}
	srv.log("info", message)
	srv.log("info", message)
	if got := countMessage(); got != 1 {
		t.Fatalf("同级别同文本应去重，实际 %d 条", got)
	}
	srv.log("warn", message)
	if got := countMessage(); got != 2 {
		t.Fatalf("不同级别同文本不应被吞，实际 %d 条", got)
	}
}

func TestNewFallsBackScriptNameWithoutHandlerMutation(t *testing.T) {
	store := newTempStore(t)
	ports := pickFreePorts(t, 1)
	srv := New(Options{
		Store:        store,
		ScriptSource: "console.log('not a userscript');\n",
		AppVersion:   testAppVersion,
		Port:         ports[0],
	})
	if srv.meta.Name != defaultScriptName {
		t.Fatalf("解析失败时初始化应兜底脚本名，实际 %q", srv.meta.Name)
	}
	if _, err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	base := strings.TrimSuffix(srv.URL(), "/")
	if response, _ := httpGet(t, base+"/", nil); response.StatusCode != http.StatusOK {
		t.Fatalf("首页状态码 = %d", response.StatusCode)
	}
	if srv.meta.Name != defaultScriptName {
		t.Fatalf("请求处理不应修改共享 meta: %q", srv.meta.Name)
	}
}

func TestForceKillPIDNonexistent(t *testing.T) {
	if err := ForceKillPID(1 << 30); err != nil {
		t.Fatalf("强制终止不存在的进程应返回 nil: %v", err)
	}
	if err := ForceKillPID(0); err != nil {
		t.Fatalf("PID 0 应返回 nil: %v", err)
	}
	if err := ForceKillPID(os.Getpid()); err != nil {
		t.Fatalf("当前进程 PID 应返回 nil: %v", err)
	}
}
