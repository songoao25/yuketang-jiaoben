//go:build !cli

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	goruntime "runtime"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/songoao25/yuketang-jiaoben/internal/batch"
	"github.com/songoao25/yuketang-jiaoben/internal/browser"
	"github.com/songoao25/yuketang-jiaoben/internal/config"
	"github.com/songoao25/yuketang-jiaoben/internal/model"
	"github.com/songoao25/yuketang-jiaoben/internal/script"
	"github.com/songoao25/yuketang-jiaoben/internal/server"
	"github.com/songoao25/yuketang-jiaoben/internal/updater"
)

// App 是 Wails 窗口后端，所有导出方法都会绑定为 window.go.main.App.*。
type App struct {
	ctx          context.Context
	store        *config.Store
	scriptSource string
	appVersion   string
	scriptMeta   model.ScriptMeta

	mu            sync.Mutex
	srv           *server.Server
	externalPID   int
	externalOwner string // gui | daemon | 空
	updateInfo    model.UpdateInfo
	updateChecked bool

	// 多账号批量（见 .private/batch-plan.md）
	batchMu           sync.Mutex
	batchStore        *batch.Store
	batchLogin        *batch.LoginSession
	batchLoginAccount string
	batchQueue        *batch.Queue
	batchLastState    batch.QueueState
	batchNotified     map[string]bool
}

// NewApp 创建窗口后端。scriptSource 是内置用户脚本正文。
func NewApp(store *config.Store, scriptSource, appVersion string) (*App, error) {
	meta, err := script.FromSource(scriptSource)
	if err != nil {
		return nil, fmt.Errorf("解析内置脚本失败: %w", err)
	}
	return &App{
		store:        store,
		scriptSource: scriptSource,
		appVersion:   appVersion,
		scriptMeta:   meta,
	}, nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	startDockIconSync()
	a.initBatch()
	if a.store.LoadSettings().AutoStart {
		if _, err := a.StartService(); err != nil {
			a.emit("自动启动本地服务失败：" + err.Error())
		}
	}
	go a.autoCheckUpdates()
}

func (a *App) shutdown(ctx context.Context) {
	a.closeBatchLogin()
	if a.batchQueue != nil {
		a.batchQueue.Stop()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srv != nil {
		_ = a.srv.Stop()
		a.srv = nil
	}
}

// emit 发一条前端可见的轻量通知（用于错误提示，不阻塞主流程）。
func (a *App) emit(message string) {
	if a.ctx == nil || message == "" {
		return
	}
	wruntime.EventsEmit(a.ctx, "app:notice", message)
}

// notify 通知前端刷新状态。
func (a *App) notify() {
	if a.ctx == nil {
		return
	}
	wruntime.EventsEmit(a.ctx, "state:changed")
}

// ---------------------------------------------------------------- 状态与绑定

// GetState 返回 App 的完整状态快照。
func (a *App) GetState() (model.State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stateLocked(), nil
}

// StartService 启动（或复用）本地配置服务。
func (a *App) StartService() (model.State, error) {
	a.mu.Lock()
	if _, err := a.ensureServerLocked(); err != nil {
		state := a.stateLocked()
		a.mu.Unlock()
		return state, err
	}
	state := a.stateLocked()
	a.mu.Unlock()
	a.notify()
	return state, nil
}

// StopService 停止本地服务（包括由 CLI 启动的后台进程）。
func (a *App) StopService() (model.State, error) {
	a.mu.Lock()
	if a.srv != nil {
		_ = a.srv.Stop()
		a.srv = nil
	}
	if a.externalPID > 0 {
		pid := a.externalPID
		if a.externalOwner == "gui" && pid != os.Getpid() {
			// 另一个 App 实例托管的服务：不杀别人的进程，提示用户
			a.emit("本地服务由另一个「雨课助手」实例托管，请在那个窗口内停止")
		} else if pid > 0 && pid != os.Getpid() {
			_ = server.KillPID(pid)
			a.externalPID = 0
			a.externalOwner = ""
			_ = a.store.ClearServerInfo()
		}
	}
	state := a.stateLocked()
	a.mu.Unlock()
	a.notify()
	return state, nil
}

// RestartService 重启本地服务。
func (a *App) RestartService() (model.State, error) {
	_, _ = a.StopService()
	return a.StartService()
}

// InstallScript 在默认浏览器中打开用户脚本安装地址，并记录安装信息。
func (a *App) InstallScript() (model.InstallResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	port, err := a.ensureServerLocked()
	if err != nil {
		return model.InstallResult{}, err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/ykt.user.js", port)
	if err := browser.OpenURL(url); err != nil {
		return model.InstallResult{}, err
	}
	_ = a.store.SaveInstallState(model.InstallState{
		Hash:     a.scriptMeta.Hash,
		Version:  a.scriptMeta.Version,
		OpenedAt: time.Now().UnixMilli(),
	})
	go a.notify()
	return model.InstallResult{
		URL:     url,
		Version: a.scriptMeta.Version,
		Hash:    a.scriptMeta.Hash,
	}, nil
}

// OpenYuketang 打开雨课堂（默认地址可在设置中修改）。
func (a *App) OpenYuketang() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.ensureServerLocked(); err != nil {
		return err
	}
	target := a.store.LoadSettings().DefaultURL
	if strings.TrimSpace(target) == "" {
		target = model.DefaultURL
	}
	return browser.OpenURL(target)
}

// SaveAIConfig 保存 AI 答题配置（写入 ~/.ykt/config.json）。
func (a *App) SaveAIConfig(cfg model.AIConfig) (model.State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.Key = strings.TrimSpace(cfg.Key)
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.URL != "" && !isHTTPURL(cfg.URL) {
		return a.stateLocked(), errors.New("API 地址必须以 http:// 或 https:// 开头")
	}
	switch cfg.APIFormat {
	case "":
		cfg.APIFormat = "openai"
	case "openai", "anthropic":
	default:
		return a.stateLocked(), errors.New("API 格式必须是 openai 或 anthropic")
	}
	switch cfg.AuthMethod {
	case "":
		cfg.AuthMethod = "bearer"
	case "bearer", "x-api-key":
	default:
		return a.stateLocked(), errors.New("认证方式必须是 bearer 或 x-api-key")
	}
	if err := a.store.SaveAIConfig(cfg); err != nil {
		return a.stateLocked(), err
	}
	state := a.stateLocked()
	go a.notify()
	return state, nil
}

// SaveFeatures 保存脚本功能开关。
func (a *App) SaveFeatures(cfg model.FeatureConfig) (model.State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.store.SaveFeatures(cfg); err != nil {
		return a.stateLocked(), err
	}
	state := a.stateLocked()
	go a.notify()
	return state, nil
}

// SaveSettings 保存本机设置；端口变化且服务在运行时自动重启服务。
func (a *App) SaveSettings(next model.Settings) (model.State, error) {
	a.mu.Lock()

	prev := a.store.LoadSettings()
	if err := config.ValidateSettings(next); err != nil {
		state := a.stateLocked()
		a.mu.Unlock()
		return state, err
	}
	if err := a.store.SaveSettings(next); err != nil {
		state := a.stateLocked()
		a.mu.Unlock()
		return state, err
	}

	if prev.Port != next.Port {
		wasRunning := (a.srv != nil && a.srv.Running()) || a.externalPID > 0
		if wasRunning {
			if a.srv != nil {
				_ = a.srv.Stop()
				a.srv = nil
			}
			if a.externalPID > 0 && a.externalOwner != "gui" {
				_ = server.KillPID(a.externalPID)
				a.externalPID = 0
				a.externalOwner = ""
				_ = a.store.ClearServerInfo()
			}
			if _, err := a.ensureServerLocked(); err != nil {
				state := a.stateLocked()
				a.mu.Unlock()
				a.notify()
				return state, err
			}
		}
	}
	state := a.stateLocked()
	a.mu.Unlock()
	a.notify()
	return state, nil
}

// GetLogs 返回最近日志。服务在跑时取内存日志，否则读日志文件尾部。
func (a *App) GetLogs(limit int) ([]model.LogEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if a.srv != nil && a.srv.Running() {
		logs := a.srv.Logs()
		if len(logs) > limit {
			logs = logs[len(logs)-limit:]
		}
		return logs, nil
	}
	return server.ReadLogFile(a.store.LogPath(), limit), nil
}

// ClearLogs 清空内存日志与日志文件。
func (a *App) ClearLogs() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srv != nil {
		a.srv.ClearLogs()
	}
	return os.WriteFile(a.store.LogPath(), nil, 0o600)
}

// RevealPath 在系统文件管理器中定位 home/config/log 路径。
func (a *App) RevealPath(which string) error {
	switch which {
	case "home":
		return browser.Reveal(a.store.HomeDir())
	case "config":
		return browser.Reveal(a.store.ConfigPath())
	case "log":
		return browser.Reveal(a.store.LogPath())
	default:
		return fmt.Errorf("未知路径类型: %s", which)
	}
}

// SetClipboard 把文本写入系统剪贴板。
func (a *App) SetClipboard(text string) error {
	if a.ctx == nil {
		return errors.New("应用尚未就绪")
	}
	return wruntime.ClipboardSetText(a.ctx, text)
}

// OpenExternal 仅允许打开 http(s) 链接（用于仓库、文档等）。
func (a *App) OpenExternal(url string) error {
	if !isHTTPURL(url) {
		return errors.New("仅支持打开 http/https 链接")
	}
	return browser.OpenURL(url)
}

// ---------------------------------------------------------------- 自动更新

// GetUpdateInfo 返回最近一次检查的缓存结果（未检查时只有当前版本）。
func (a *App) GetUpdateInfo() (model.UpdateInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	info := a.updateInfo
	info.CurrentVersion = a.appVersion
	return info, nil
}

// CheckForUpdates 访问 GitHub Releases 检查新版本。
func (a *App) CheckForUpdates() (model.UpdateInfo, error) {
	if os.Getenv("YKT_UPDATE_DISABLE") == "1" {
		return a.cachedUpdateInfo(), errors.New("自动更新已禁用（YKT_UPDATE_DISABLE=1）")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	info, err := updater.Check(ctx, updater.NewOptions(a.appVersion))
	if err != nil {
		a.updateLog("检查更新失败: %v", err)
		return a.cachedUpdateInfo(), err
	}
	info.CurrentVersion = a.appVersion
	a.mu.Lock()
	a.updateInfo = info
	a.updateChecked = true
	a.mu.Unlock()
	if info.Available {
		a.updateLog("发现新版本 v%s（当前 v%s）", info.LatestVersion, a.appVersion)
	}
	return info, nil
}

// FinishOnboarding 记录新手引导已完成，之后不再自动弹出。
func (a *App) FinishOnboarding() (model.State, error) {
	a.mu.Lock()
	settings := a.store.LoadSettings()
	settings.OnboardingDone = true
	if err := a.store.SaveSettings(settings); err != nil {
		state := a.stateLocked()
		a.mu.Unlock()
		return state, err
	}
	state := a.stateLocked()
	a.mu.Unlock()
	a.notify()
	return state, nil
}

// InstallUpdate 下载并安排安装更新。成功后 App 会自动退出并重启。
func (a *App) InstallUpdate() (string, error) {
	if os.Getenv("YKT_UPDATE_DISABLE") == "1" {
		return "", errors.New("自动更新已禁用（YKT_UPDATE_DISABLE=1）")
	}
	info := a.cachedUpdateInfo()
	if !info.Available {
		checked, err := a.CheckForUpdates()
		if err != nil {
			return "", err
		}
		info = checked
	}
	if !info.Available {
		return "", errors.New("当前已是最新版本")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := updater.Apply(ctx, info, a.updateLog); err != nil {
		a.updateLog("安装更新失败: %v", err)
		return "", err
	}
	go func() {
		time.Sleep(1200 * time.Millisecond)
		if a.ctx != nil {
			wruntime.Quit(a.ctx)
		}
	}()
	return "正在安装更新，App 将退出并自动重启", nil
}

func (a *App) cachedUpdateInfo() model.UpdateInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	info := a.updateInfo
	info.CurrentVersion = a.appVersion
	return info
}

// autoCheckUpdates 启动后静默检查一次；发现新版本时通过 app:notice 提示。
func (a *App) autoCheckUpdates() {
	if os.Getenv("YKT_UPDATE_DISABLE") == "1" {
		return
	}
	time.Sleep(3 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	info, err := updater.Check(ctx, updater.NewOptions(a.appVersion))
	if err != nil {
		a.updateLog("自动检查更新失败: %v", err)
		return
	}
	info.CurrentVersion = a.appVersion
	a.mu.Lock()
	a.updateInfo = info
	a.updateChecked = true
	a.mu.Unlock()
	if info.Available {
		a.emit("发现新版本 v" + info.LatestVersion + "，可在「设置 → 更新」中安装")
	}
}

// updateLog 把更新过程写入 ~/.ykt/update.log（不含敏感信息）。
func (a *App) updateLog(format string, args ...any) {
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
	file, err := os.OpenFile(filepath.Join(a.store.HomeDir(), "update.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = file.WriteString(line)
	_ = file.Close()
}

// Quit 退出 App。
func (a *App) Quit() {
	if a.ctx != nil {
		wruntime.Quit(a.ctx)
	}
}

// ---------------------------------------------------------------- 内部实现

// ensureServerLocked 确保有一个可用的本地服务，返回其端口。
func (a *App) ensureServerLocked() (int, error) {
	if a.srv != nil && a.srv.Running() {
		return a.srv.Port(), nil
	}
	if status, ok := server.DetectRunning(a.store); ok {
		a.externalPID = status.PID
		a.externalOwner = status.Owner
		return status.Port, nil
	}
	settings := a.store.LoadSettings()
	srv := server.New(server.Options{
		Store:        a.store,
		ScriptSource: a.scriptSource,
		AppVersion:   a.appVersion,
		Port:         settings.Port,
		Owner:        "gui",
	})
	port, err := srv.Start()
	if err != nil {
		return 0, fmt.Errorf("启动本地服务失败: %w", err)
	}
	a.srv = srv
	return port, nil
}

func (a *App) stateLocked() model.State {
	settings := a.store.LoadSettings()
	state := model.State{
		AppVersion: a.appVersion,
		Platform:   goruntime.GOOS,
		HomeDir:    a.store.HomeDir(),
		ConfigPath: a.store.ConfigPath(),
		LogPath:    a.store.LogPath(),
		ScriptFile: "内置（随 App 打包）",
		Script:     a.scriptMeta,
		AI:         a.store.LoadAIConfig(),
		Features:   a.store.LoadFeatures(),
		Settings:   settings,
		PortRange:  model.PortRange{Min: model.MinPort, Max: model.MaxPort},
		Install:    a.installStateLocked(),
		Service:    a.serviceStatusLocked(),
	}
	if state.Service.Running {
		state.SyncURL = fmt.Sprintf("http://127.0.0.1:%d/api/config", state.Service.Port)
	}
	if a.srv != nil {
		state.LastSyncAt = a.srv.LastConfigSync()
	}
	return state
}

func (a *App) serviceStatusLocked() model.ServiceStatus {
	if a.srv != nil && a.srv.Running() {
		return model.ServiceStatus{
			Running: true,
			Port:    a.srv.Port(),
			PID:     os.Getpid(),
			URL:     a.srv.URL(),
			Uptime:  (time.Now().UnixMilli() - a.srv.StartedAt()) / 1000,
			Owner:   "gui",
		}
	}
	if status, ok := server.DetectRunning(a.store); ok {
		if status.PID != os.Getpid() {
			a.externalPID = status.PID
			a.externalOwner = status.Owner
		}
		return status
	}
	return model.ServiceStatus{}
}

func (a *App) installStateLocked() model.InstallState {
	record := a.store.LoadInstallState()
	installed := record.Hash != ""
	inSync := installed && record.Hash == a.scriptMeta.Hash
	state := model.InstallState{
		Installed: installed,
		InSync:    inSync,
		Version:   record.Version,
		Hash:      record.Hash,
		OpenedAt:  record.OpenedAt,
	}
	switch {
	case !installed:
		state.Note = "尚未在浏览器中安装脚本"
	case !inSync:
		state.Note = "脚本已更新，需要在浏览器中重新安装"
	default:
		state.Note = "已安装，与当前内置脚本一致"
	}
	return state
}

func isHTTPURL(raw string) bool {
	return strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://")
}

// showMainWindow 从菜单栏/二次启动唤起主窗口。
func (a *App) showMainWindow() {
	if a.ctx == nil {
		return
	}
	go wruntime.WindowShow(a.ctx)
}
