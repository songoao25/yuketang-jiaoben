# ykt 内部契约（Go + Wails 版）

本文件是后端（Go）、前端（HTML/CSS/JS）并行开发的唯一契约。改动接口先改这里。

## 目标形态

- 一个代码库、两个构建目标（同一套 `internal/*`）：
  - GUI：`wails build` → macOS `.app`（Windows/Linux 同理），**无参数启动 GUI**；GUI 二进制带参数时也可当 CLI 用。
  - CLI：`go build -tags cli -o bin/ykt .` → 纯 Go、无 cgo / 无 WebView 依赖，可直接交叉编译 macOS / Windows / Linux（`main_cli.go`，`//go:build cli`）。GUI 绑定代码在 `app.go` / `main_gui.go` 中带 `//go:build !cli`。纯 CLI 二进制无参数 = `start`；GUI 二进制无参数 = 打开窗口、带参数 = 执行 CLI 子命令。
- GUI 与 CLI 共享 `internal/*` 包、同一份 `~/.ykt` 配置、同一个本地 HTTP 服务。
- 用户脚本 `yuketang.js` 内置进二进制（`//go:embed yuketang.js`），不依赖仓库路径。
- 本地服务只监听 `127.0.0.1`，端口在 `8899..8910` 内自动选择。
- 网页脚本按 `8899..8910` 顺序探测 `GET /api/config`，实现 AI/功能配置从 App 同步；服务未运行或同步关闭时静默回退 localStorage。

## 目录与写入范围

| 路径 | 负责人 | 说明 |
| --- | --- | --- |
| `main_gui.go`、`app.go`（`!cli`）、`main_cli.go`（`cli`）、`version.go` | Lead | Wails 入口 + 绑定 / 纯 Go CLI 入口；`//go:embed yuketang.js`、`//go:embed all:frontend` |
| `internal/model/model.go` | Lead | 共享数据结构，见文件 |
| `internal/config/` | 后端 | 配置读写、路径、校验 |
| `internal/script/` | 后端 | 脚本头解析、指纹 |
| `internal/server/` | 后端 | 本地 HTTP 服务、健康检查、进程发现/终止、日志 |
| `internal/browser/` | 后端 | 跨平台打开 URL / Finder 定位 |
| `internal/cli/` | 后端 | CLI 子命令实现 |
| `frontend/` | 前端 | 静态资产，`frontend:build` 为空，直接 embed |
| `docs/`、`Makefile`、`README.md` | Lead | 文档与构建 |

## Go 包 API（必须与签名一致）

### internal/config

```go
func Open() (*Store, error)                  // YKT_HOME 环境变量优先，否则 ~/.ykt；目录 0700
func OpenAt(home string) (*Store, error)     // 测试用
type Store struct{ /* 不导出字段 */ }
func (s *Store) HomeDir() string
func (s *Store) ConfigPath() string          // <home>/config.json
func (s *Store) StatePath() string           // <home>/state.json
func (s *Store) ServerPath() string          // <home>/server.json
func (s *Store) LogPath() string             // <home>/server.log
func (s *Store) LoadSettings() model.Settings // 缺省值填充；读失败返回默认值
func (s *Store) SaveSettings(model.Settings) error
func (s *Store) LoadAIConfig() model.AIConfig
func (s *Store) SaveAIConfig(model.AIConfig) error
func (s *Store) LoadFeatures() model.FeatureConfig
func (s *Store) SaveFeatures(model.FeatureConfig) error
func (s *Store) LoadInstallState() model.InstallState
func (s *Store) SaveInstallState(model.InstallState) error
func (s *Store) LoadServerInfo() (model.ServerInfo, bool)
func (s *Store) SaveServerInfo(model.ServerInfo) error
func (s *Store) ClearServerInfo() error
func (s *Store) HasUserConfig() bool         // HasConfigSection("ai") || HasConfigSection("features")
func (s *Store) HasConfigSection(section string) bool // config.json 中是否存在该段（"ai" / "features"）
func ValidateSettings(model.Settings) error  // 端口必须在 [model.MinPort, model.MaxPort]，URL 必须 http(s)
func DefaultSettings() model.Settings
```

- `config.json` 结构：`{"settings":{...},"ai":{...},"features":{...}}`；权限 0600。
- 写入使用「临时文件 + rename」的原子写；`features` 默认 `false/false`；`settings` 默认：
  `port=8899, defaultUrl=https://yuketang.cn/, autoStart=true, syncEnabled=true, appearance="system"`。
- 所有读写不得因单个文件损坏而 crash；解析失败返回默认值并在日志中记录（可选）。
- **兼容旧 Node CLI 数据（QA M4）**：`LoadSettings` 读到旧扁平结构 `{"url": "...", "port": 8900}`（无 `settings` 段）时，把 `url`/`port` 作为迁移来源；`LoadInstallState` 读到旧字段 `openedHash` 时映射为 `hash`（`installed=true`）。迁移只在读取时发生，下一次保存写新结构。
- `HasConfigSection` 应基于原始 JSON 键判断：文件损坏或键不存在都返回 false。

### internal/script

```go
func FromSource(source string) (model.ScriptMeta, error) // 解析 ==UserScript== 头，sha256，行数
func ShortHash(hash string) string                       // 前 8 位
```

要求：解析 `@name`、`@version`、`@author`、`@match`（多个）；缺少 `==UserScript==` 头或 `@version` 返回 error。

### internal/server

```go
type Options struct {
    Store        *config.Store
    ScriptSource string
    AppVersion   string
    Port         int
    Owner        string // "gui"（桌面 App 进程内服务）或 "daemon"（CLI 后台服务，默认）
}
func New(opts Options) *Server
func (s *Server) Start() (port int, err error) // 从 opts.Port 起在 [MinPort,MaxPort] 内找空闲端口，写 server.json
func (s *Server) Stop() error                  // 优雅关闭，清理 server.json
func (s *Server) Running() bool
func (s *Server) Port() int
func (s *Server) URL() string                  // http://127.0.0.1:<port>/
func (s *Server) InstallURL() string           // http://127.0.0.1:<port>/ykt.user.js
func (s *Server) StartedAt() int64             // Unix 毫秒
func (s *Server) LastConfigSync() int64        // 最近一次 /api/config 被拉取的时间（Unix 毫秒，进程内）
func (s *Server) Logs() []model.LogEntry       // 最近 200 条（进程内环形缓冲）
func (s *Server) ClearLogs()

func DetectRunning(store *config.Store) (model.ServiceStatus, bool) // server.json + /health 校验
func CheckHealth(port int, timeout time.Duration) (model.Health, bool) // 必须校验 health.App == "ykt"
func KillPID(pid int) error                    // 跨平台终止进程；不存在时返回 nil
func ReadLogFile(path string, limit int) []model.LogEntry // JSON Lines，读最后 limit 条
```

HTTP 路由（全部返回 `Cache-Control: no-store`）：

| 路由 | 说明 |
| --- | --- |
| `GET /health` | `model.Health` JSON；`app` 字段固定 `"ykt"` |
| `GET /ykt.user.js`、`/yuketang.js`、`/script` | 返回脚本源码，`Content-Type: application/javascript; charset=utf-8`；每次成功返回时用当前脚本 meta 更新 `state.json` 安装记录 |
| `GET /` | 简易 HTML 控制台（看版本/指纹、手动安装、打开雨课堂），风格简洁即可 |
| `GET /api/config` | 用户脚本配置同步，见下 |
| `GET /favicon.ico` | 204 |

`/api/config` 安全规则：

- 必须带请求头 `X-YKT-Client: userscript`，否则 403 JSON `{"ok":false,"error":"forbidden"}`。
- CORS：仅当 `Origin` 为空或匹配 `https://*.yuketang.cn` / `https://*.gdufemooc.cn` / `http://127.0.0.1:*` / `http://localhost:*` 时回写 `Access-Control-Allow-Origin: <origin>`；其余不回写。
- 成功响应：`model.SyncConfig`，`sync.enabled` 取自 `settings.SyncEnabled`；未开启时 `ai` 与 `features` 返回零值（不要泄露 Key）。
- `configured` 语义：`~/.ykt/config.json` 中已存在 `ai` 或 `features` 字段时为 true（新增方法 `func (s *Store) HasUserConfig() bool`）；为 false 时网页脚本不得覆盖自己在网页面板里保存的配置。注意识别「文件存在但只有 settings」的情况。
- 每次成功拉取更新 `LastConfigSync`，并记一条 info 日志（例如 `配置已同步到网页脚本`，相同内容 5 秒内去重）。

日志：`server.log` 使用 JSON Lines（`{"time":1700000000000,"level":"info","message":"..."}`），进程内环形缓冲 200 条。记录生命周期（启动/停止/端口）、脚本安装拉取、配置同步、错误；不要记录 AI Key 或完整请求体。

端口占用：`Start` 在 `[MinPort, MaxPort]` 内尝试 `opts.Port` 起依次 +1，全占用报错。已在 `server.json` 中记录且健康的本进程无需处理（由调用方 `DetectRunning` 判断复用）。

进程安全（QA M2/M5）：

- `CheckHealth`/`DetectRunning` 必须校验 `/health` 的 `app == "ykt"`；仅 `ok:true` 的其它本地服务不得视为 ykt。
- `DetectRunning` 以 `/health` 返回的 `PID` 为准；若 `server.json` 记录的 PID 与 `health.PID` 不一致，视为过期记录并清理，不按旧 PID 终止任何进程。
- `server.json` 写入 `owner` 字段（`gui` / `daemon`）。CLI 的 `stop` / `restart` 遇到 `owner == "gui"` 时必须拒绝直接杀进程，提示「本地服务由桌面 App 托管，请在 App 窗口内停止」，退出码 1。
- CLI 的 `ensureService` 与 `stop` 必须用 `~/.ykt/server.lock` 做跨进程互斥（O_CREATE|O_EXCL + 陈旧锁超时清理），锁内二次 `DetectRunning`，避免并发 `ykt start` 泄漏孤儿守护进程（QA M1）。
- `start` 派生守护进程失败/超时时应终止子进程；`stop` 在 SIGTERM 超时后应有 SIGKILL 兜底并如实报错。

`/api/config` 响应新增 `hasAI` / `hasFeatures`：分别取 `Store.HasConfigSection("ai")` / `HasConfigSection("features")`；`configured = hasAI || hasFeatures`。

### internal/browser

```go
func OpenURL(url string) error   // darwin: open；windows: rundll32 url.dll,FileProtocolHandler 或 cmd start；linux: xdg-open
func Reveal(path string) error   // darwin: open -R；windows: explorer /select,；linux: xdg-open 父目录
```

### internal/cli

```go
type Env struct {
    Store        *config.Store
    ScriptSource string
    AppVersion   string
    Stdout       io.Writer
    Stderr       io.Writer
    Executable   string // 可选；守护进程派生子进程用，空则 os.Executable()
}
func Run(args []string, env Env) int // 返回进程退出码
```

子命令（与旧 Node CLI 行为对齐）：

| 命令 | 行为 |
| --- | --- |
| `start`（默认） | 复用/拉起后台服务 → 判断安装记录决定是否打开安装页 → 打开默认网址 → 打印下一步 |
| `install` | 拉起服务 → 打开安装地址 → 写安装记录（打开即记录） |
| `open [网址]` | 有参数则保存为默认地址；拉起服务；打开网址 |
| `panel` | 拉起服务；打开 `http://127.0.0.1:<port>/` |
| `serve` | 前台运行服务（`--daemon` 内部使用：脱离子进程，日志写 server.log） |
| `stop` | 停止 `server.json` 记录且健康的服务 |
| `restart` | stop + start |
| `status` | 打印脚本版本/指纹、服务状态、安装记录、默认地址、配置路径 |
| `version` | 打印 `App 版本 + 脚本版本 + 指纹短码` |
| `help` | 帮助 |

参数：`-p/--port`、`-u/--url`、`-f/--force`、`--no-open`、`--daemon`、`-h/--help`、`-v/--version`。
后台守护：`Executable serve --daemon --port N`，输出重定向到 `server.log`；等待 `/health` 最多约 4 秒。
跨平台进程分离用 build tag 文件（`proc_unix.go` / `proc_windows.go`）实现 `detach(cmd *exec.Cmd)`。
`--no-open` 时只准备服务、不调用浏览器。
所有错误输出以 `✖` 开头并返回非零退出码；成功以 `✔` 开头。

## Wails 绑定（`window.go.main.App.*`）

| 方法 | 返回 | 说明 |
| --- | --- | --- |
| `GetState()` | `model.State` | 完整快照 |
| `StartService()` | `model.State` | 启动/复用本地服务 |
| `StopService()` | `model.State` | 停止本 App 启动的服务或外部守护进程 |
| `RestartService()` | `model.State` | 重启 |
| `InstallScript()` | `model.InstallResult` | 确保服务 → 用默认浏览器打开安装地址 → 记录安装 |
| `OpenYuketang()` | `error` | 确保服务 → 打开默认网址 |
| `SaveAIConfig(model.AIConfig)` | `model.State` | 校验并保存；URL 非空时必须 http(s)，apiFormat/authMethod 白名单 |
| `SaveFeatures(model.FeatureConfig)` | `model.State` | 保存功能开关 |
| `SaveSettings(model.Settings)` | `model.State` | 校验保存；端口变化且服务在运行时自动重启服务 |
| `GetLogs(limit int)` | `[]model.LogEntry` | 服务运行时取内存日志，否则读日志文件尾部 |
| `GetUpdateInfo()` | `model.UpdateInfo` | 最近一次检查的缓存结果（未检查时只含当前版本） |
| `CheckForUpdates()` | `model.UpdateInfo` | 访问 GitHub Releases latest 接口检查新版本 |
| `InstallUpdate()` | `string` | 下载、校验并安排替换安装；成功后 App 退出重启 |

自动更新资产约定（与 `.github/workflows/release.yml`、`tools/package-release.sh` 保持一致）：

- `ykt-darwin-arm64.zip` / `ykt-darwin-amd64.zip`：内含 `雨课助手.app`
- `ykt-windows-amd64.zip`：内含 `ykt.exe`
- `ykt-linux-amd64.tar.gz`：内含 `ykt`
- `checksums.txt`：每行 `sha256  <asset>`；缺失校验信息时 App 拒绝安装
- 版本以 release tag（`v1.1.0`）为准；下载后校验 SHA256、`CFBundleShortVersionString` 与 `CFBundleIdentifier`（`com.wails.ykt`）
- 仅允许从 `/Applications`、`~/Applications` 下的正式 App 自替换；开发构建会返回明确错误
- 测试可用 `YKT_UPDATE_API_URL` 覆盖 latest 接口，`YKT_UPDATE_DISABLE=1` 关闭检查/安装

| `ClearLogs()` | `error` | 清空内存与文件日志 |
| `RevealPath(which string)` | `error` | `home`/`config`/`log` 三类路径，用系统文件管理器定位 |
| `SetClipboard(text string)` | `error` | 写系统剪贴板 |
| `OpenExternal(url string)` | `error` | 仅允许 http(s)，用默认浏览器打开 |
| `Quit()` | — | 退出 App |

后端在服务状态变化后通过 `runtime.EventsEmit(ctx, "state:changed")` 通知前端；前端同时保留 3 秒轮询兜底。

## 前端契约

- 技术：纯原生 HTML + CSS + JS（无框架、无 npm、无 CDN、离线可用）。
- 文件：`frontend/index.html`、`frontend/app.css`、`frontend/app.js`（可增加 `frontend/assets/`）。
- 入口：`index.html` 直接引用 `app.css`/`app.js`（不要 ES module 依赖构建，`<script src="./app.js"></script>` 即可）。
- 调用：`await window.go.main.App.GetState()`；事件 `window.runtime.EventsOn("state:changed", refresh)`（需判空）。
- 开发预览：`index.html?mock=1` 时不得访问绑定，改用内置 mock 数据渲染（便于用 Chrome 截图自测）。
- 适配 macOS 隐藏标题栏：顶部 52px 区域留出红绿灯空间，拖动区域使用 `--wails-draggable: drag`，按钮 `--wails-draggable: no-drag`。
- 支持浅色/深色（`prefers-color-scheme`），`Settings.Appearance` 为 `system|light|dark` 时切换根元素 class；尊重 `prefers-reduced-motion`。
- 平台差异：`state.Platform` 为 `darwin` 时使用透明/毛玻璃侧栏，其他平台使用实色背景。
- 键盘：Cmd/Ctrl+1..5 切换页面；Esc 关闭弹层；焦点可见；所有按钮有 aria-label。

## 验证命令

```bash
go vet ./internal/...
go vet -tags cli .
go vet -tags desktop,production .
go test ./internal/...
CGO_LDFLAGS="-framework UniformTypeIdentifiers" go test -tags desktop,production .  # macOS 链接 Wails 需要
go build -tags cli -o bin/ykt .            # 纯 Go CLI（三平台可交叉编译）
CGO_LDFLAGS="-framework UniformTypeIdentifiers" go build -tags desktop,production -o bin/ykt-gui .  # macOS
wails build
./bin/ykt status
curl -s http://127.0.0.1:8899/health
node --check yuketang.js
```

Go 测试必须使用临时目录（`config.OpenAt(t.TempDir())` 或 `YKT_HOME`），不得读写真实 `~/.ykt`；不得依赖固定端口（测试用 8899..8910 内空闲口或先探测）。
