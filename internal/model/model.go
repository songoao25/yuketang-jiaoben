// Package model 定义 App、CLI、本地 HTTP 服务与前端之间共享的数据结构。
// 这些结构体的 JSON 标签同时决定 Wails 绑定与 HTTP 接口的对外格式。
package model

// 本地服务可用端口区间。网页用户脚本会按这个区间逐个探测，以便从本机 App 同步配置。
const (
	MinPort = 8899
	MaxPort = 8910
)

// DefaultURL 是默认打开的雨课堂地址。
const DefaultURL = "https://yuketang.cn/"

// ScriptMeta 描述内置用户脚本的元信息（从脚本头解析）。
type ScriptMeta struct {
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Author    string   `json:"author"`
	Lines     int      `json:"lines"`
	Hash      string   `json:"hash"`
	ShortHash string   `json:"shortHash"`
	Matches   []string `json:"matches"`
}

// InstallState 记录最近一次「在浏览器打开脚本安装地址」的信息。
type InstallState struct {
	Installed bool   `json:"installed"`
	InSync    bool   `json:"inSync"`
	Version   string `json:"version"`
	Hash      string `json:"hash"`
	OpenedAt  int64  `json:"openedAt"` // Unix 毫秒
	Note      string `json:"note"`
}

// ServiceStatus 是本地服务的当前状态。
type ServiceStatus struct {
	Running bool   `json:"running"`
	Port    int    `json:"port"`
	PID     int    `json:"pid"`
	URL     string `json:"url"`
	Uptime  int64  `json:"uptime"` // 秒；外部服务未知时为 0
	Owner   string `json:"owner"`  // gui: 由桌面 App 托管；daemon: CLI 独立进程；空: 未知
	Error   string `json:"error,omitempty"`
}

// Health 是 GET /health 的响应体。
type Health struct {
	OK      bool   `json:"ok"`
	App     string `json:"app"`
	PID     int    `json:"pid"`
	Port    int    `json:"port"`
	Version string `json:"version"`
	Hash    string `json:"hash"`
	File    string `json:"file"`
	Uptime  int64  `json:"uptime"`
}

// ServerInfo 持久化在 ~/.ykt/server.json，用于发现后台服务进程。
type ServerInfo struct {
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	StartedAt int64  `json:"startedAt"` // Unix 毫秒
	Version   string `json:"version"`
	Owner     string `json:"owner"` // gui | daemon
}

// AIConfig 是用户脚本使用的 AI 答题配置。
type AIConfig struct {
	URL        string `json:"url"`
	Key        string `json:"key"`
	Model      string `json:"model"`
	APIFormat  string `json:"apiFormat"`  // openai | anthropic
	AuthMethod string `json:"authMethod"` // bearer | x-api-key
}

// FeatureConfig 是用户脚本的功能开关。
type FeatureConfig struct {
	AutoAI      bool `json:"autoAI"`
	AutoComment bool `json:"autoComment"`
}

// Settings 是 App / CLI 的本机设置。
type Settings struct {
	Port           int    `json:"port"`       // 本地服务端口，限制在 [MinPort, MaxPort]
	DefaultURL     string `json:"defaultUrl"` // 打开雨课堂时使用的默认地址
	AutoStart      bool   `json:"autoStart"`  // App 启动时自动启动本地服务
	SyncEnabled    bool   `json:"syncEnabled"`
	Appearance     string `json:"appearance"`     // system | light | dark
	OnboardingDone bool   `json:"onboardingDone"` // 首次启动的新手引导是否已完成
}

// PortRange 告知前端可选端口区间。
type PortRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// LogEntry 是一条运行日志。
type LogEntry struct {
	Time    int64  `json:"time"` // Unix 毫秒
	Level   string `json:"level"`
	Message string `json:"message"`
}

// State 是前端 GetState 绑定返回的完整快照。
type State struct {
	AppVersion string        `json:"appVersion"`
	Platform   string        `json:"platform"`
	HomeDir    string        `json:"homeDir"`
	ConfigPath string        `json:"configPath"`
	LogPath    string        `json:"logPath"`
	ScriptFile string        `json:"scriptFile"`
	Service    ServiceStatus `json:"service"`
	Script     ScriptMeta    `json:"script"`
	Install    InstallState  `json:"install"`
	AI         AIConfig      `json:"ai"`
	Features   FeatureConfig `json:"features"`
	Settings   Settings      `json:"settings"`
	PortRange  PortRange     `json:"portRange"`
	SyncURL    string        `json:"syncUrl"`    // 脚本同步配置的地址；服务未运行时为空
	LastSyncAt int64         `json:"lastSyncAt"` // 最近一次脚本拉取配置的 Unix 毫秒，0 表示本次会话尚未同步
}

// InstallResult 是 InstallScript 绑定返回的信息。
type InstallResult struct {
	URL     string `json:"url"`
	Version string `json:"version"`
	Hash    string `json:"hash"`
}

// SyncConfig 是 GET /api/config 返回给用户脚本的配置。
type SyncConfig struct {
	OK          bool          `json:"ok"`
	App         string        `json:"app"`
	Version     string        `json:"version"`
	Sync        SyncFlag      `json:"sync"`
	Configured  bool          `json:"configured"`  // 本机是否保存过 AI 或功能配置
	HasAI       bool          `json:"hasAI"`       // config.json 中存在 "ai" 段，脚本才覆盖网页 AI 配置
	HasFeatures bool          `json:"hasFeatures"` // config.json 中存在 "features" 段，脚本才覆盖网页功能开关
	AI          AIConfig      `json:"ai"`
	Features    FeatureConfig `json:"features"`
}

// SyncFlag 表示配置同步是否开启。
type SyncFlag struct {
	Enabled bool `json:"enabled"`
}

// UpdateInfo 是 GitHub Releases 最新版本与当前版本的比较结果。
type UpdateInfo struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	Available      bool   `json:"available"`
	PublishedAt    int64  `json:"publishedAt"` // Unix 毫秒
	Notes          string `json:"notes"`       // Release 说明（截断）
	AssetName      string `json:"assetName"`
	AssetSize      int64  `json:"assetSize"`
	AssetURL       string `json:"assetUrl"`
	Checksum       string `json:"checksum"` // sha256 hex
}
