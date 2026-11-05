package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/songoao25/yuketang-jiaoben/internal/config"
	"github.com/songoao25/yuketang-jiaoben/internal/model"
)

// defaultHealthTimeout 是单次 /health 探测的默认超时。
const defaultHealthTimeout = 800 * time.Millisecond

// Probe 请求指定端口的 /health 并解析响应，但不校验服务身份。
// 仅在需要识别旧版（Node CLI，health 无 app 字段）服务时使用；
// 常规判定请用 CheckHealth / DetectRunning。
func Probe(port int, timeout time.Duration) (model.Health, bool) {
	if port <= 0 {
		return model.Health{}, false
	}
	if timeout <= 0 {
		timeout = defaultHealthTimeout
	}
	// 显式禁用代理：本机请求不应该走系统代理。
	client := &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{Proxy: nil},
	}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		return model.Health{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return model.Health{}, false
	}
	var health model.Health
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return model.Health{}, false
	}
	if !health.OK || health.PID <= 0 {
		return model.Health{}, false
	}
	if health.Port <= 0 {
		health.Port = port
	}
	return health, true
}

// CheckHealth 探测指定端口上的本地服务是否健康。
// 必须校验 health.app == "ykt"（QA M2）：仅返回 {"ok":true} 的其它本地服务
// 不得被误认为 ykt。
func CheckHealth(port int, timeout time.Duration) (model.Health, bool) {
	health, ok := Probe(port, timeout)
	if !ok || health.App != "ykt" {
		return model.Health{}, false
	}
	return health, true
}

// DetectRunning 依据 server.json 记录 + /health 探测判断本地服务是否在运行。
//
// 进程身份以 /health 返回的 PID 为准（QA M2）：记录 PID 与健康响应的 PID 不一致时
// 视为过期记录并清理，绝不按旧 PID 终止任何进程。记录缺失、进程已退出或健康
// 检查失败时返回 false。
func DetectRunning(store *config.Store) (model.ServiceStatus, bool) {
	if store == nil {
		return model.ServiceStatus{}, false
	}
	info, ok := store.LoadServerInfo()
	if !ok {
		return model.ServiceStatus{}, false
	}
	health, ok := CheckHealth(info.Port, defaultHealthTimeout)
	if !ok {
		return model.ServiceStatus{}, false
	}
	// health.PID 是本机 ykt 服务自报的进程；缺失时无法安全判定身份。
	if health.PID <= 0 {
		_ = store.ClearServerInfo()
		return model.ServiceStatus{}, false
	}
	if health.PID != info.PID {
		// server.json 过期：清理记录，但不终止任何进程。
		_ = store.ClearServerInfo()
		return model.ServiceStatus{}, false
	}
	port := health.Port
	if port <= 0 {
		port = info.Port
	}
	return model.ServiceStatus{
		Running: true,
		Port:    port,
		PID:     health.PID,
		URL:     fmt.Sprintf("http://127.0.0.1:%d/", port),
		Uptime:  health.Uptime,
		Owner:   info.Owner,
	}, true
}

// ReadLogFile 读取 server.log 末尾最多 limit 条日志；损坏行会被跳过。
func ReadLogFile(path string, limit int) []model.LogEntry {
	if limit <= 0 {
		limit = logLimit
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	entries := make([]model.LogEntry, 0, limit)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry model.LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	if len(entries) > limit {
		entries = append([]model.LogEntry(nil), entries[len(entries)-limit:]...)
	}
	return entries
}

// KillPID 跨平台终止进程；进程不存在时返回 nil。不会终止当前进程。
func KillPID(pid int) error {
	if pid <= 0 || pid == os.Getpid() {
		return nil
	}
	return terminate(pid)
}

// ForceKillPID 强制终止进程（SIGKILL / TerminateProcess），用于 SIGTERM 超时后的兜底
// （QA L2）。进程不存在时返回 nil；不会终止当前进程。
func ForceKillPID(pid int) error {
	if pid <= 0 || pid == os.Getpid() {
		return nil
	}
	return forceTerminate(pid)
}
