// Package server 实现 ykt 的本地 HTTP 服务：脚本分发、配置同步、健康检查、
// 进程发现与终止、JSON Lines 日志。
//
// 服务只监听 127.0.0.1，端口限制在 [model.MinPort, model.MaxPort]，供 App、CLI
// 与浏览器里的用户脚本共用。进程信息写入 <home>/server.json，日志写入
// <home>/server.log（JSON Lines）。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/songoao25/yuketang-jiaoben/internal/config"
	"github.com/songoao25/yuketang-jiaoben/internal/model"
	"github.com/songoao25/yuketang-jiaoben/internal/script"
)

const (
	// logLimit 是进程内日志环形缓冲的容量。
	logLimit = 200
	// logDedupeWindow 是相同日志内容的去重窗口。
	logDedupeWindow = 5 * time.Second
	// shutdownTimeout 是优雅关闭的最长等待时间。
	shutdownTimeout = 3 * time.Second
	// scriptFileNote 是 Health.File / UI 中展示的内置脚本说明。
	scriptFileNote = "内置（随 App 打包）"
	// defaultScriptName 是脚本头缺少 @name 时的兜底名称。
	defaultScriptName = "雨课助手"
	// logSeenSweepThreshold 是 logSeen 触发过期清理的条目数阈值。
	logSeenSweepThreshold = 256
)

// Options 是创建 Server 的参数。
type Options struct {
	Store        *config.Store
	ScriptSource string
	AppVersion   string
	Port         int
	// Owner 标记服务归属："gui"（桌面 App 进程内服务）或 "daemon"（CLI 后台服务）。
	// 空值按 "daemon" 处理，并写入 server.json 供 CLI 判断能否直接终止。
	Owner string
}

// 服务归属取值（写入 server.json 的 owner 字段）。
const (
	OwnerGUI    = "gui"
	OwnerDaemon = "daemon"
)

// normalizeOwner 把 Owner 收敛为 gui / daemon，未知值按 daemon 处理。
func normalizeOwner(owner string) string {
	if strings.ToLower(strings.TrimSpace(owner)) == OwnerGUI {
		return OwnerGUI
	}
	return OwnerDaemon
}

// Server 是本地 HTTP 服务实例。
type Server struct {
	opts Options
	meta model.ScriptMeta

	mu        sync.Mutex
	running   bool
	port      int
	startedAt int64
	http      *http.Server
	listener  net.Listener
	logs      []model.LogEntry
	logSeen   map[string]int64 // 相同日志内容的最近记录时间（Unix 毫秒）

	lastConfigSync atomic.Int64
}

// New 创建服务实例（不启动监听）。脚本头解析失败时元信息留空，不影响启动；
// App 与 CLI 在创建前已校验内置脚本。名称兜底在这里完成，请求处理期间不再写
// 共享字段（QA L7）。
func New(opts Options) *Server {
	meta, _ := script.FromSource(opts.ScriptSource)
	if strings.TrimSpace(meta.Name) == "" {
		meta.Name = defaultScriptName
	}
	opts.Owner = normalizeOwner(opts.Owner)
	return &Server{
		opts:    opts,
		meta:    meta,
		logSeen: make(map[string]int64),
	}
}

// Start 在 [MinPort, MaxPort] 内从 opts.Port 起查找空闲端口并开始监听，
// 成功后写入 server.json。返回实际端口。
func (s *Server) Start() (int, error) {
	if s.opts.Store == nil {
		return 0, errors.New("启动本地服务失败：缺少配置存储")
	}

	s.mu.Lock()
	if s.running {
		port := s.port
		s.mu.Unlock()
		return port, nil
	}
	s.mu.Unlock()

	startPort := s.opts.Port
	if startPort < model.MinPort || startPort > model.MaxPort {
		startPort = model.MinPort
	}

	var (
		listener net.Listener
		port     int
		lastErr  error
	)
	for candidate := startPort; candidate <= model.MaxPort; candidate++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", candidate))
		if err != nil {
			lastErr = err
			continue
		}
		listener = ln
		port = candidate
		break
	}
	if listener == nil {
		if lastErr == nil {
			lastErr = errors.New("无可用端口")
		}
		return 0, fmt.Errorf("端口 %d~%d 均被占用：%w", startPort, model.MaxPort, lastErr)
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)
	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	s.mu.Lock()
	s.listener = listener
	s.http = httpServer
	s.port = port
	s.startedAt = time.Now().UnixMilli()
	s.running = true
	s.mu.Unlock()

	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log("error", "本地服务异常退出："+err.Error())
		}
	}()

	info := model.ServerInfo{
		PID:       os.Getpid(),
		Port:      port,
		StartedAt: s.startedAt,
		Version:   s.opts.AppVersion,
		Owner:     s.opts.Owner,
	}
	if err := s.opts.Store.SaveServerInfo(info); err != nil {
		s.log("warn", "写入服务记录失败："+err.Error())
	}
	s.log("info", fmt.Sprintf("本地服务已启动：http://127.0.0.1:%d/（PID %d）", port, os.Getpid()))
	return port, nil
}

// Stop 优雅关闭服务并清理本进程写入的 server.json。可重复调用。
func (s *Server) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	httpServer := s.http
	port := s.port
	s.running = false
	s.http = nil
	s.listener = nil
	s.mu.Unlock()

	var err error
	if httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if shutdownErr := httpServer.Shutdown(ctx); shutdownErr != nil {
			err = shutdownErr
			_ = httpServer.Close()
		}
	}
	s.clearServerInfoIfOwned()
	s.log("info", fmt.Sprintf("本地服务已停止：http://127.0.0.1:%d/", port))
	return err
}

// clearServerInfoIfOwned 只清理本进程写入的服务记录，避免误删其他进程的记录。
func (s *Server) clearServerInfoIfOwned() {
	if s.opts.Store == nil {
		return
	}
	info, ok := s.opts.Store.LoadServerInfo()
	if !ok {
		return
	}
	if info.PID != os.Getpid() {
		return
	}
	if err := s.opts.Store.ClearServerInfo(); err != nil {
		s.log("warn", "清理服务记录失败："+err.Error())
	}
}

// Running 报告服务是否在运行。
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Port 返回当前（或最近一次）监听的端口。
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// URL 返回服务根地址，例如 http://127.0.0.1:8899/。
func (s *Server) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/", s.Port())
}

// InstallURL 返回用户脚本安装地址。
func (s *Server) InstallURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/ykt.user.js", s.Port())
}

// StartedAt 返回服务启动时间（Unix 毫秒）。
func (s *Server) StartedAt() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startedAt
}

// LastConfigSync 返回最近一次 /api/config 被成功拉取的时间（Unix 毫秒）。
func (s *Server) LastConfigSync() int64 { return s.lastConfigSync.Load() }

// Logs 返回最近 200 条进程内日志（新→旧顺序中的时间升序）。
func (s *Server) Logs() []model.LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.LogEntry, len(s.logs))
	copy(out, s.logs)
	return out
}

// ClearLogs 清空进程内日志缓冲（日志文件由调用方处理）。
func (s *Server) ClearLogs() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = nil
	s.logSeen = make(map[string]int64)
}

// uptime 返回服务已运行秒数。
func (s *Server) uptime() int64 {
	started := s.StartedAt()
	if started <= 0 {
		return 0
	}
	seconds := (time.Now().UnixMilli() - started) / 1000
	if seconds < 0 {
		return 0
	}
	return seconds
}

// ---------------------------------------------------------------- 日志

// log 记一条日志：写入内存环形缓冲与 server.log（JSON Lines）。
// 相同「级别 + 内容」5 秒内去重，避免用户脚本频繁轮询刷屏（QA L6）。
func (s *Server) log(level, message string) {
	if level == "" {
		level = "info"
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	now := time.Now().UnixMilli()
	key := level + "\x00" + message
	window := logDedupeWindow.Milliseconds()

	s.mu.Lock()
	if last, ok := s.logSeen[key]; ok && now-last < window {
		s.mu.Unlock()
		return
	}
	s.logSeen[key] = now
	if len(s.logSeen) > logSeenSweepThreshold {
		// 清理过期去重记录，避免长时间运行时内存缓慢增长。
		for seenKey, seenAt := range s.logSeen {
			if now-seenAt >= window {
				delete(s.logSeen, seenKey)
			}
		}
	}
	entry := model.LogEntry{Time: now, Level: level, Message: message}
	s.logs = append(s.logs, entry)
	if len(s.logs) > logLimit {
		trimmed := make([]model.LogEntry, logLimit)
		copy(trimmed, s.logs[len(s.logs)-logLimit:])
		s.logs = trimmed
	}
	s.mu.Unlock()

	s.appendLogFile(entry)
}

// appendLogFile 把一条日志追加到 server.log；失败静默（日志不能影响主流程）。
func (s *Server) appendLogFile(entry model.LogEntry) {
	if s.opts.Store == nil {
		return
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	file, err := os.OpenFile(s.opts.Store.LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = file.Write(append(line, '\n'))
}

// ---------------------------------------------------------------- HTTP 路由

// registerRoutes 注册全部 HTTP 路由。
func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/index.html", s.handleIndex)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ykt.user.js", s.handleScript)
	mux.HandleFunc("/yuketang.js", s.handleScript)
	mux.HandleFunc("/script", s.handleScript)
	mux.HandleFunc("/api/config", s.handleAPIConfig)
	mux.HandleFunc("/favicon.ico", s.handleFavicon)
}

// setNoStore 为所有响应设置禁止缓存的通用头。
func setNoStore(w http.ResponseWriter) {
	header := w.Header()
	header.Set("Cache-Control", "no-store, no-cache, must-revalidate")
	header.Set("Pragma", "no-cache")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
}

// requireGet 只允许 GET 请求，其余返回 405。
func requireGet(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET")
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
	return false
}

// writeJSON 输出 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "500 internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

// handleHealth 返回 model.Health。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if !requireGet(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, model.Health{
		OK:      true,
		App:     "ykt",
		PID:     os.Getpid(),
		Port:    s.Port(),
		Version: s.opts.AppVersion,
		Hash:    s.meta.Hash,
		File:    scriptFileNote,
		Uptime:  s.uptime(),
	})
}

// handleScript 返回内置用户脚本，并更新安装记录。
func (s *Server) handleScript(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if !requireGet(w, r) {
		return
	}
	if strings.TrimSpace(s.opts.ScriptSource) == "" {
		s.log("error", "内置用户脚本为空，无法下发")
		http.Error(w, "内置用户脚本为空", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, s.opts.ScriptSource)

	s.recordInstall()
	s.log("info", fmt.Sprintf("脚本安装页已下发：%s", script.Describe(s.meta)))
}

// recordInstall 记录「脚本已被浏览器拉取安装」。
func (s *Server) recordInstall() {
	if s.opts.Store == nil || s.meta.Hash == "" {
		return
	}
	state := model.InstallState{
		Installed: true,
		InSync:    true,
		Version:   s.meta.Version,
		Hash:      s.meta.Hash,
		OpenedAt:  time.Now().UnixMilli(),
		Note:      "已通过本地服务安装",
	}
	if err := s.opts.Store.SaveInstallState(state); err != nil {
		s.log("warn", "写入安装记录失败："+err.Error())
	}
}

// handleFavicon 返回 204，避免浏览器反复请求。
func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if !requireGet(w, r) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAPIConfig 处理用户脚本的配置同步请求，带来源校验与 CORS 白名单。
// 支持白名单 Origin 的 OPTIONS 预检（QA L8）；POST 等其它方法仍返回 405。
func (s *Server) handleAPIConfig(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)

	origin := r.Header.Get("Origin")
	if origin != "" {
		w.Header().Add("Vary", "Origin")
	}

	// 预检：不需要 X-YKT-Client 头，只对白名单 Origin 放行。
	if r.Method == http.MethodOptions {
		if origin == "" {
			w.Header().Set("Allow", "GET, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !isAllowedOrigin(origin) {
			writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "forbidden"})
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "X-YKT-Client")
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if !requireGet(w, r) {
		return
	}
	if origin != "" && isAllowedOrigin(origin) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}

	if r.Header.Get("X-YKT-Client") != "userscript" {
		s.log("warn", "拒绝配置同步请求：缺少 X-YKT-Client: userscript 头")
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "forbidden"})
		return
	}
	if s.opts.Store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "store unavailable"})
		return
	}

	settings := s.opts.Store.LoadSettings()
	hasAI := s.opts.Store.HasConfigSection("ai")
	hasFeatures := s.opts.Store.HasConfigSection("features")
	response := model.SyncConfig{
		OK:          true,
		App:         "ykt",
		Version:     s.opts.AppVersion,
		Sync:        model.SyncFlag{Enabled: settings.SyncEnabled},
		Configured:  hasAI || hasFeatures, // QA M3：按段判断，configured 只是兼容旧脚本的整体标记
		HasAI:       hasAI,
		HasFeatures: hasFeatures,
	}
	if settings.SyncEnabled {
		response.AI = s.opts.Store.LoadAIConfig()
		response.Features = s.opts.Store.LoadFeatures()
	}

	s.lastConfigSync.Store(time.Now().UnixMilli())
	s.log("info", "配置已同步到网页脚本")
	writeJSON(w, http.StatusOK, response)
}

// isAllowedOrigin 判断 Origin 是否在 CORS 白名单内：
// https://*.yuketang.cn、https://*.gdufemooc.cn、http://127.0.0.1:*、http://localhost:*。
func isAllowedOrigin(origin string) bool {
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || parsed.Host == "" {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return false
	}
	switch {
	case scheme == "https" && (host == "yuketang.cn" || strings.HasSuffix(host, ".yuketang.cn")):
		return true
	case scheme == "https" && (host == "gdufemooc.cn" || strings.HasSuffix(host, ".gdufemooc.cn")):
		return true
	case scheme == "http" && (host == "127.0.0.1" || host == "localhost"):
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------- 控制台首页

// pageData 是首页模板的数据。
type pageData struct {
	Name       string
	Version    string
	ShortHash  string
	Hash       string
	Lines      int
	Matches    string
	AppVersion string
	BaseURL    string
	InstallURL string
	DefaultURL string
	ConfigPath string
	LogPath    string
}

// indexTemplate 是极简本地控制台页面。
var indexTemplate = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Name}} · 本地控制台</title>
<style>
:root{color-scheme:light dark}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0e1116;color:#e6edf3;
font:15px/1.6 -apple-system,BlinkMacSystemFont,"PingFang SC","Microsoft YaHei",sans-serif}
.card{width:min(640px,92vw);padding:34px 34px 26px;border:1px solid #232a33;border-radius:18px;background:#151a21;
box-shadow:0 24px 60px rgba(0,0,0,.45)}
h1{margin:0 0 6px;font-size:22px}
.sub{margin:0 0 24px;color:#8b949e;font-size:13px}
.actions{display:flex;gap:12px;flex-wrap:wrap;margin-bottom:26px}
a.btn{flex:1 1 240px;text-align:center;padding:14px 18px;border-radius:12px;border:1px solid #2d3846;
background:#1b222b;color:#e6edf3;text-decoration:none;font-weight:600}
a.btn:hover{border-color:#3d4b5c}
a.btn.primary{background:linear-gradient(135deg,#2f81f7,#1f6feb);border-color:#2f81f7;color:#fff}
ol{margin:0 0 18px;padding-left:20px;color:#adbac7;font-size:14px}
ol li{margin:4px 0}
.meta{display:grid;grid-template-columns:auto 1fr;gap:6px 16px;font-size:12.5px;color:#8b949e;
border-top:1px solid #232a33;padding-top:16px;margin-top:6px}
code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;color:#79c0ff;font-size:12px;word-break:break-all}
.tip{margin:18px 0 0;font-size:12.5px;color:#6e7681}
</style>
</head>
<body>
<main class="card">
  <h1>{{.Name}}</h1>
  <p class="sub">本地控制台 · App v{{.AppVersion}} · 脚本 v{{.Version}} · 指纹 {{.ShortHash}}</p>
  <div class="actions">
    <a class="btn primary" href="/ykt.user.js">① 安装 / 更新用户脚本</a>
    <a class="btn" href="{{.DefaultURL}}" target="_blank" rel="noreferrer">② 打开雨课堂</a>
  </div>
  <ol>
    <li>点击上方按钮，在 Tampermonkey 弹窗中确认「安装 / 重新安装」。</li>
    <li>打开雨课堂并登录，进入<b>课程目录</b>页面（不是视频页面）。</li>
    <li>点击左下角面板的「开始刷课」，脚本会自动接管播放。</li>
  </ol>
  <div class="meta">
    <span>服务地址</span><code>{{.BaseURL}}</code>
    <span>安装地址</span><code>{{.InstallURL}}</code>
    <span>匹配网址</span><code>{{.Matches}}</code>
    <span>脚本行数</span><code>{{.Lines}} 行</code>
    <span>配置文件</span><code>{{.ConfigPath}}</code>
    <span>日志文件</span><code>{{.LogPath}}</code>
  </div>
  <p class="tip">停止本地服务：<code>ykt stop</code> · 仅供学习交流，请遵守平台使用条款。</p>
</main>
</body>
</html>
`))

// handleIndex 渲染本地控制台首页；其他未知路径返回 404。
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if !requireGet(w, r) {
		return
	}
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}

	defaultURL := model.DefaultURL
	configPath, logPath := "", ""
	if s.opts.Store != nil {
		settings := s.opts.Store.LoadSettings()
		if strings.TrimSpace(settings.DefaultURL) != "" {
			defaultURL = settings.DefaultURL
		}
		configPath = s.opts.Store.ConfigPath()
		logPath = s.opts.Store.LogPath()
	}
	data := pageData{
		Name:       s.meta.Name,
		Version:    s.meta.Version,
		ShortHash:  s.meta.ShortHash,
		Hash:       s.meta.Hash,
		Lines:      s.meta.Lines,
		Matches:    strings.Join(s.meta.Matches, "  "),
		AppVersion: s.opts.AppVersion,
		BaseURL:    s.URL(),
		InstallURL: s.InstallURL(),
		DefaultURL: defaultURL,
		ConfigPath: configPath,
		LogPath:    logPath,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := indexTemplate.Execute(w, data); err != nil {
		s.log("warn", "渲染控制台页面失败："+err.Error())
	}
}
