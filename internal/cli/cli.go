// Package cli 实现 ykt 命令行的全部子命令。
//
// 与旧 Node CLI（bin/ykt.js）行为对齐：start/install/open/panel/serve/stop/
// restart/status/version/help，支持 -p/--port、-u/--url、-f/--force、
// --no-open、--daemon、-h/--help、-v/--version。后台服务通过
// `Executable serve --daemon --port N` 派生，输出重定向到 server.log，
// 最多等待约 4 秒 /health 就绪。
//
// start/install/open/panel/stop/restart 都会先获取 <home>/server.lock 跨进程
// 操作锁（QA M1），避免并发 ykt start 泄漏孤儿守护进程。
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/songoao25/yuketang-jiaoben/internal/browser"
	"github.com/songoao25/yuketang-jiaoben/internal/config"
	"github.com/songoao25/yuketang-jiaoben/internal/model"
	"github.com/songoao25/yuketang-jiaoben/internal/script"
	"github.com/songoao25/yuketang-jiaoben/internal/server"
)

const (
	// startPollInterval 是等待就绪 / 停止的轮询间隔。
	startPollInterval = 100 * time.Millisecond
	// stopTimeout 是等待服务优雅退出（SIGTERM）的最长时间。
	stopTimeout = 3 * time.Second
	// forceKillTimeout 是 SIGKILL 兜底后等待端口释放的最长时间。
	forceKillTimeout = 1500 * time.Millisecond
	// scriptFileNote 是状态输出中内置脚本的说明。
	scriptFileNote = "内置（随 App 打包）"
	// guiManagedMessage 是 owner=gui 时 stop/restart 的拒绝提示（QA M5）。
	guiManagedMessage = "本地服务由桌面 App 托管，请在 App 窗口内停止"
	// staleTempFileAge 是清理原子写残留 .tmp-* 文件的年龄阈值（QA L1）。
	staleTempFileAge = 30 * time.Second
)

// 超时与锁参数是变量，便于测试缩短等待（生产值：启动 4s、陈旧锁 20s、
// 重试 50ms、锁等待最多 5s）。
var (
	startTimeout      = 4 * time.Second
	lockStaleAge      = 20 * time.Second
	lockRetryInterval = 50 * time.Millisecond
	lockWaitTimeout   = 5 * time.Second
)

// Env 是 CLI 运行所需的环境。
type Env struct {
	Store        *config.Store
	ScriptSource string
	AppVersion   string
	Stdout       io.Writer
	Stderr       io.Writer
	Executable   string // 可选；派生后台服务用，空则 os.Executable()
}

// options 是解析后的命令行参数。
type options struct {
	command     string
	positionals []string
	port        int
	portSet     bool
	url         string
	urlSet      bool
	force       bool
	noOpen      bool
	daemon      bool
	help        bool
	version     bool
}

// output 封装 stdout/stderr 与颜色开关，统一 ✔/›/!/✖ 输出风格。
type output struct {
	out   io.Writer
	err   io.Writer
	color bool
}

func (o *output) ok(message string)   { fmt.Fprintln(o.out, paint(o.color, "32", "✔")+" "+message) }
func (o *output) info(message string) { fmt.Fprintln(o.out, paint(o.color, "36", "›")+" "+message) }
func (o *output) warn(message string) { fmt.Fprintln(o.out, paint(o.color, "33", "!")+" "+message) }
func (o *output) fail(message string) { fmt.Fprintln(o.err, paint(o.color, "31", "✖")+" "+message) }
func (o *output) plain(message string) {
	fmt.Fprintln(o.out, message)
}

// paint 在启用颜色时用 ANSI 前景色包裹文本。
func paint(enabled bool, code, text string) string {
	if !enabled {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// paintBold 在启用颜色时加粗。
func paintBold(enabled bool, text string) string {
	if !enabled {
		return text
	}
	return "\x1b[1m" + text + "\x1b[0m"
}

// paintDim 在启用颜色时置灰。
func paintDim(enabled bool, text string) string {
	if !enabled {
		return text
	}
	return "\x1b[2m" + text + "\x1b[0m"
}

// Run 执行 CLI，返回进程退出码：0 成功、1 运行失败、2 用法错误。
func Run(args []string, env Env) int {
	stdout, stderr := writers(env)
	o := &output{out: stdout, err: stderr, color: colorEnabled(stdout)}

	opts, err := parseArgs(args)
	if err != nil {
		o.fail(err.Error())
		return 2
	}

	if opts.help || opts.command == "help" {
		printHelp(o, env)
		return 0
	}
	if opts.version && opts.command == "" {
		if err := printVersion(o, env); err != nil {
			o.fail(err.Error())
			return 1
		}
		return 0
	}

	command := normalizeCommand(opts.command)
	if command == "" {
		command = "start"
	}
	if env.Store == nil {
		o.fail("缺少配置存储，无法执行命令")
		return 1
	}

	var runErr error
	switch command {
	case "start":
		runErr = withLock(env.Store, func() error {
			return cmdStartLocked(opts, env, o)
		})
	case "install":
		runErr = withLock(env.Store, func() error {
			return cmdInstallLocked(opts, env, o)
		})
	case "open":
		runErr = withLock(env.Store, func() error {
			return cmdOpenLocked(opts, env, o)
		})
	case "panel":
		runErr = withLock(env.Store, func() error {
			return cmdPanelLocked(opts, env, o)
		})
	case "serve":
		runErr = cmdServe(opts, env, o)
	case "stop":
		runErr = withLock(env.Store, func() error {
			return cmdStopLocked(opts, env, o)
		})
	case "restart":
		// QA M1/M5：stop + start 在同一把锁内完成；owner=gui 时 stop 会拒绝。
		runErr = withLock(env.Store, func() error {
			if err := cmdStopLocked(opts, env, o); err != nil {
				return err
			}
			return cmdStartLocked(opts, env, o)
		})
	case "status":
		runErr = cmdStatus(opts, env, o)
	case "version":
		runErr = printVersion(o, env)
	default:
		o.fail(fmt.Sprintf("未知命令：%s（用 ykt help 查看用法）", opts.command))
		return 2
	}
	if runErr != nil {
		o.fail(runErr.Error())
		return 1
	}
	return 0
}

// normalizeCommand 展开旧 CLI 的常用别名。
func normalizeCommand(command string) string {
	switch command {
	case "i":
		return "install"
	case "ui":
		return "panel"
	case "st":
		return "status"
	default:
		return command
	}
}

// parseArgs 解析命令行参数。
func parseArgs(args []string) (*options, error) {
	opts := &options{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-p" || arg == "--port":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("参数 %s 缺少端口值", arg)
			}
			port, err := parsePort(args[i])
			if err != nil {
				return nil, err
			}
			opts.port = port
			opts.portSet = true
		case strings.HasPrefix(arg, "--port="):
			port, err := parsePort(strings.TrimPrefix(arg, "--port="))
			if err != nil {
				return nil, err
			}
			opts.port = port
			opts.portSet = true
		case arg == "-u" || arg == "--url":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("参数 %s 缺少网址", arg)
			}
			opts.url = args[i]
			opts.urlSet = true
		case strings.HasPrefix(arg, "--url="):
			opts.url = strings.TrimPrefix(arg, "--url=")
			opts.urlSet = true
		case arg == "-f" || arg == "--force":
			opts.force = true
		case arg == "--no-open":
			opts.noOpen = true
		case arg == "--daemon":
			opts.daemon = true
		case arg == "-h" || arg == "--help":
			opts.help = true
		case arg == "-v" || arg == "--version":
			opts.version = true
		case strings.HasPrefix(arg, "-") && arg != "-":
			return nil, fmt.Errorf("未知参数：%s（用 ykt help 查看用法）", arg)
		default:
			opts.positionals = append(opts.positionals, arg)
		}
	}
	if len(opts.positionals) > 0 {
		opts.command = opts.positionals[0]
		opts.positionals = opts.positionals[1:]
	}
	return opts, nil
}

// parsePort 解析端口参数。
func parsePort(raw string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || port <= 0 {
		return 0, fmt.Errorf("端口必须是正整数：%s", raw)
	}
	return port, nil
}

// writers 返回实际使用的 stdout/stderr。
func writers(env Env) (io.Writer, io.Writer) {
	stdout := env.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := env.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	return stdout, stderr
}

// colorEnabled 只在输出到终端且未设置 NO_COLOR 时启用颜色。
func colorEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// ---------------------------------------------------------------- 子命令

// cmdStartLocked 启动：复用/拉起服务 → 按安装记录决定是否打开安装页 → 打开默认网址。
// 调用方必须已持有操作锁。
func cmdStartLocked(opts *options, env Env, o *output) error {
	settings := env.Store.LoadSettings()
	targetURL := settings.DefaultURL
	if opts.urlSet {
		targetURL = strings.TrimSpace(opts.url)
		if targetURL == "" {
			return errors.New("网址不能为空")
		}
		next := settings
		next.DefaultURL = targetURL
		if err := config.ValidateSettings(next); err != nil {
			return err
		}
		if err := env.Store.SaveSettings(next); err != nil {
			return err
		}
		o.ok("已记住默认课程地址：" + targetURL)
	}

	status, reused, err := ensureServiceLocked(opts, env, o)
	if err != nil {
		return err
	}
	base := serviceBase(status)
	if reused {
		o.ok("本地服务已在运行：" + base + "/")
	} else {
		o.ok("本地服务已启动：" + base + "/")
	}

	meta, err := currentMeta(env)
	if err != nil {
		return err
	}
	record := env.Store.LoadInstallState()
	installed := record.Hash != ""
	if opts.force || !installed || record.Hash != meta.Hash {
		if err := openInstallPage(opts, env, o, base, meta); err != nil {
			return err
		}
	} else {
		o.info(fmt.Sprintf("脚本 v%s 已安装过（指纹 %s），需要重装可执行：ykt install", meta.Version, meta.ShortHash))
	}

	if err := openTarget(opts, o, targetURL); err != nil {
		return err
	}

	o.plain("")
	o.plain("  " + paintBold(o.color, "下一步") + "：登录雨课堂 -> 进入「课程目录」页面 -> 点击面板「开始刷课」")
	o.plain("  " + paintDim(o.color, "控制台") + " " + base + "/    " + paintDim(o.color, "停止服务") + " ykt stop")
	o.plain("")
	return nil
}

// cmdInstallLocked 只安装 / 更新用户脚本。调用方必须已持有操作锁。
func cmdInstallLocked(opts *options, env Env, o *output) error {
	meta, err := currentMeta(env)
	if err != nil {
		return err
	}
	status, _, err := ensureServiceLocked(opts, env, o)
	if err != nil {
		return err
	}
	base := serviceBase(status)
	if err := openInstallPage(opts, env, o, base, meta); err != nil {
		return err
	}
	o.ok(fmt.Sprintf("安装地址：%s/ykt.user.js（v%s，指纹 %s）", base, meta.Version, meta.ShortHash))
	o.info("若没有弹出安装窗口，请确认已安装 Tampermonkey 扩展，或手动访问上面的地址。")
	return nil
}

// cmdOpenLocked 打开雨课堂；带位置参数时保存为默认地址。调用方必须已持有操作锁。
func cmdOpenLocked(opts *options, env Env, o *output) error {
	settings := env.Store.LoadSettings()
	targetURL := settings.DefaultURL
	if opts.urlSet {
		targetURL = strings.TrimSpace(opts.url)
	}
	if len(opts.positionals) > 0 {
		targetURL = strings.TrimSpace(opts.positionals[0])
		next := settings
		next.DefaultURL = targetURL
		if err := config.ValidateSettings(next); err != nil {
			return err
		}
		if err := env.Store.SaveSettings(next); err != nil {
			return err
		}
		o.ok("已记住默认课程地址：" + targetURL)
	}
	if targetURL == "" {
		targetURL = model.DefaultURL
	}

	status, _, err := ensureServiceLocked(opts, env, o)
	if err != nil {
		return err
	}
	base := serviceBase(status)
	if err := openTarget(opts, o, targetURL); err != nil {
		return err
	}
	o.ok("已打开：" + targetURL)
	o.info("控制台页面：" + base + "/")
	return nil
}

// cmdPanelLocked 打开本地控制台页面。调用方必须已持有操作锁。
func cmdPanelLocked(opts *options, env Env, o *output) error {
	status, _, err := ensureServiceLocked(opts, env, o)
	if err != nil {
		return err
	}
	base := serviceBase(status)
	if err := openTarget(opts, o, base+"/"); err != nil {
		return err
	}
	o.ok("已打开控制台：" + base + "/")
	return nil
}

// cmdServe 前台运行本地服务；--daemon 时由父进程派生，日志写 server.log。
// QA L1：信号处理在 Start 之前注册，避免 /health 已可达但还没装好处理器时收到
// SIGTERM 直接退出并留下 .tmp-* 残留；启动前顺手清理陈旧临时文件。
func cmdServe(opts *options, env Env, o *output) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, terminationSignals()...)
	defer signal.Stop(signals)

	cleanupStaleTempFiles(env.Store)

	port, err := resolvePort(opts, env)
	if err != nil {
		return err
	}
	meta, err := currentMeta(env)
	if err != nil {
		return err
	}
	srv := server.New(server.Options{
		Store:        env.Store,
		ScriptSource: env.ScriptSource,
		AppVersion:   env.AppVersion,
		Port:         port,
	})
	actual, err := srv.Start()
	if err != nil {
		return err
	}
	if !opts.daemon {
		o.ok(fmt.Sprintf("本地服务已启动：http://127.0.0.1:%d/", actual))
		o.info(fmt.Sprintf("脚本安装地址：http://127.0.0.1:%d/ykt.user.js", actual))
		o.info(fmt.Sprintf("脚本 %s", script.Describe(meta)))
		o.info("按 Ctrl+C 停止服务")
	}
	<-signals
	return srv.Stop()
}

// cmdStopLocked 停止 server.json 记录且健康的本地服务。调用方必须已持有操作锁。
// QA M2：只按 /health 自报的 PID 终止；QA M5：owner=gui 时拒绝直接杀进程；
// QA L2：SIGTERM 超时后 SIGKILL 兜底，仍失败如实报错。
func cmdStopLocked(opts *options, env Env, o *output) error {
	info, ok := env.Store.LoadServerInfo()
	if !ok {
		o.info("本地服务未运行。")
		return nil
	}
	status, running := server.DetectRunning(env.Store)
	if !running {
		// R1：兼容旧版 Node CLI 守护进程（其 /health 没有 app 字段）。
		// 仅当记录端口上 /health 自报的 PID 与记录完全一致时才终止，避免误杀。
		if legacy, ok := legacyService(env.Store); ok {
			if err := server.KillPID(legacy.PID); err != nil {
				return fmt.Errorf("停止旧版 ykt 服务失败：%w", err)
			}
			if !waitServiceStopped(legacy.Port, stopTimeout) {
				_ = server.ForceKillPID(legacy.PID)
				_ = waitServiceStopped(legacy.Port, forceKillTimeout)
			}
			if err := env.Store.ClearServerInfo(); err != nil {
				return err
			}
			o.ok(fmt.Sprintf("已停止旧版 ykt 服务（PID %d）。", legacy.PID))
			return nil
		}
		if err := env.Store.ClearServerInfo(); err != nil {
			return err
		}
		o.info("本地服务未运行（已清理过期记录）。")
		return nil
	}

	// M5：GUI 进程内的服务不能由 CLI 直接终止，否则整个桌面 App 会被杀掉。
	if status.Owner == server.OwnerGUI {
		return errors.New(guiManagedMessage)
	}

	pid := status.PID
	if pid <= 0 {
		pid = info.PID
	}
	if pid == os.Getpid() {
		if err := env.Store.ClearServerInfo(); err != nil {
			return err
		}
		o.info("本地服务由当前进程管理，未执行终止。")
		return nil
	}
	if err := server.KillPID(pid); err != nil {
		return fmt.Errorf("停止本地服务失败：%w", err)
	}
	// L3：等待健康检查时使用 /health 回显的实际端口，而不是 server.json 的端口。
	port := status.Port
	if port <= 0 {
		port = info.Port
	}
	if !waitServiceStopped(port, stopTimeout) {
		// L2：SIGTERM 超时后用 SIGKILL 兜底；仍无法停止则保留记录并如实报错。
		if forceErr := server.ForceKillPID(pid); forceErr != nil {
			return fmt.Errorf("停止本地服务失败（PID %d 未响应 SIGTERM，SIGKILL 失败：%v）", pid, forceErr)
		}
		if !waitServiceStopped(port, forceKillTimeout) {
			return fmt.Errorf("停止本地服务失败（PID %d 在 SIGKILL 后仍未退出）", pid)
		}
	}
	if err := env.Store.ClearServerInfo(); err != nil {
		return err
	}
	o.ok(fmt.Sprintf("本地服务已停止（PID %d）。", pid))
	return nil
}

// legacyService 识别旧版 Node CLI 留下的守护进程记录（R1 升级兼容）。
//
// 旧版 /health 没有 app 字段，M2 收紧后不会再被 DetectRunning 认可。
// 仅当记录端口上的 /health 自报的 PID 与 server.json 记录完全一致时才认定，
// 避免把其它本地服务误判为 ykt、进而误杀进程。
func legacyService(store *config.Store) (model.ServerInfo, bool) {
	info, ok := store.LoadServerInfo()
	if !ok {
		return model.ServerInfo{}, false
	}
	health, ok := server.Probe(info.Port, 800*time.Millisecond)
	if !ok || health.App != "" || health.PID != info.PID {
		return model.ServerInfo{}, false
	}
	return info, true
}

// cmdStatus 输出脚本、服务、安装记录与配置路径。
func cmdStatus(opts *options, env Env, o *output) error {
	meta, err := currentMeta(env)
	if err != nil {
		return err
	}
	settings := env.Store.LoadSettings()
	record := env.Store.LoadInstallState()

	o.plain("")
	o.plain("  " + paintBold(o.color, "雨课助手 · ykt"))
	o.plain("")

	installNote := "暂无（执行 ykt 时自动安装）"
	if record.Hash != "" {
		syncNote := "脚本已更新，请执行 ykt install"
		if record.Hash == meta.Hash {
			syncNote = "与当前脚本一致"
		}
		installNote = fmt.Sprintf("%s · v%s · %s · %s",
			formatTime(record.OpenedAt), fallback(record.Version, "未知"), script.ShortHash(record.Hash), syncNote)
	}

	serviceNote := "未运行（执行 ykt 即会启动）"
	if status, ok := server.DetectRunning(env.Store); ok {
		serviceNote = fmt.Sprintf("运行中 · %s · PID %d", status.URL, status.PID)
	} else if legacy, ok := legacyService(env.Store); ok {
		serviceNote = fmt.Sprintf("旧版 ykt 服务运行中 · 端口 %d · 执行 ykt stop 可停止并升级", legacy.Port)
	}

	rows := [][2]string{
		{"App 版本", fallback(env.AppVersion, "未知")},
		{"脚本文件", fmt.Sprintf("%s · v%s · %d 行", scriptFileNote, meta.Version, meta.Lines)},
		{"脚本指纹", fmt.Sprintf("%s  (%s…)", meta.ShortHash, firstN(meta.Hash, 16))},
		{"匹配网址", fallback(strings.Join(meta.Matches, "  "), "未声明")},
		{"本地服务", serviceNote},
		{"安装记录", installNote},
		{"默认地址", settings.DefaultURL},
		{"配置文件", env.Store.ConfigPath()},
		{"日志文件", env.Store.LogPath()},
	}
	for _, row := range rows {
		o.plain("  " + paintDim(o.color, row[0]+"　") + " " + row[1])
	}
	o.plain("")
	return nil
}

// printVersion 打印 App 版本 + 脚本版本 + 指纹短码。
func printVersion(o *output, env Env) error {
	meta, err := currentMeta(env)
	if err != nil {
		return err
	}
	o.plain(fmt.Sprintf("App v%s · 脚本 v%s · 指纹 %s",
		fallback(env.AppVersion, "未知"), meta.Version, meta.ShortHash))
	return nil
}

// printHelp 打印帮助。
func printHelp(o *output, env Env) {
	name := "雨课助手"
	if meta, err := currentMeta(env); err == nil {
		name = meta.Name
	}
	lines := []string{
		"",
		paintBold(o.color, "ykt") + " —— " + name,
		"",
		paintBold(o.color, "用法"),
		"  ykt [start]          启动本地服务并打开雨课堂（必要时先安装/更新脚本）",
		"  ykt install          只安装 / 更新用户脚本（在 Tampermonkey 中确认）",
		"  ykt open [网址]      打开雨课堂；传入网址则记为默认地址",
		"  ykt panel            打开本地控制台页面（可在浏览器里手动安装）",
		"  ykt serve            前台运行本地服务，便于调试或查看日志",
		"  ykt stop             停止后台本地服务",
		"  ykt restart          重启本地服务",
		"  ykt status           查看脚本版本、指纹、服务与安装记录",
		"  ykt version          打印 App 版本、脚本版本与指纹",
		"  ykt help             显示本帮助",
		"",
		paintBold(o.color, "参数"),
		fmt.Sprintf("  -p, --port <端口>    指定本地服务端口（%d~%d，默认 %d，被占用时自动顺延）", model.MinPort, model.MaxPort, model.MinPort),
		"  -u, --url <网址>     指定要打开的网址（start 时会保存为默认地址）",
		"  -f, --force          忽略安装记录，强制重新打开安装页",
		"      --no-open        只准备服务，不调用浏览器（CI / 远程环境）",
		"      --daemon         后台守护模式（内部使用：serve --daemon）",
		"  -h, --help           显示本帮助",
		"  -v, --version        打印版本信息",
		"",
		paintBold(o.color, "示例"),
		"  ykt                                       # 启动",
		"  ykt open https://changjiang.yuketang.cn/  # 把长江雨课堂设为默认地址",
		"  ykt --port 8900 --force                   # 换端口并强制重装脚本",
		"",
	}
	if env.Store != nil {
		lines = append(lines,
			paintDim(o.color, "配置文件 "+env.Store.ConfigPath()),
			paintDim(o.color, "本地日志 "+env.Store.LogPath()),
			"",
		)
	}
	o.plain(strings.Join(lines, "\n"))
}

// ---------------------------------------------------------------- 内部实现

// currentMeta 解析内置脚本元信息。
func currentMeta(env Env) (model.ScriptMeta, error) {
	if strings.TrimSpace(env.ScriptSource) == "" {
		return model.ScriptMeta{}, errors.New("内置用户脚本为空")
	}
	meta, err := script.FromSource(env.ScriptSource)
	if err != nil {
		return model.ScriptMeta{}, fmt.Errorf("解析内置脚本失败：%w", err)
	}
	return meta, nil
}

// resolvePort 计算本次服务应使用的端口。
func resolvePort(opts *options, env Env) (int, error) {
	port := env.Store.LoadSettings().Port
	if opts.portSet {
		port = opts.port
	}
	if port < model.MinPort || port > model.MaxPort {
		return 0, fmt.Errorf("端口必须在 %d~%d 之间，当前为 %d", model.MinPort, model.MaxPort, port)
	}
	return port, nil
}

// ensureServiceLocked 复用已有服务，否则派生后台服务并等待就绪。
// 调用方必须已持有操作锁（QA M1）；返回服务状态以及是否为复用。
func ensureServiceLocked(opts *options, env Env, o *output) (model.ServiceStatus, bool, error) {
	if status, ok := server.DetectRunning(env.Store); ok {
		return status, true, nil
	}
	// R1：M2 收紧后旧 Node CLI 的守护进程不再被识别；升级场景下先停掉它，再由新版接管。
	if info, ok := legacyService(env.Store); ok {
		if err := server.KillPID(info.PID); err == nil {
			_ = waitServiceStopped(info.Port, stopTimeout)
			_ = env.Store.ClearServerInfo()
			if o != nil {
				o.warn(fmt.Sprintf("检测到旧版 ykt 服务（PID %d），已停止并由新版接管", info.PID))
			}
		}
	}
	port, err := resolvePort(opts, env)
	if err != nil {
		return model.ServiceStatus{}, false, err
	}
	executable := strings.TrimSpace(env.Executable)
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return model.ServiceStatus{}, false, fmt.Errorf("无法定位 ykt 可执行文件：%w", err)
		}
	}

	logFile, err := os.OpenFile(env.Store.LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return model.ServiceStatus{}, false, fmt.Errorf("打开日志文件失败：%w", err)
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.Command(executable, "serve", "--daemon", "--port", strconv.Itoa(port))
	detach(cmd)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return model.ServiceStatus{}, false, fmt.Errorf("启动后台服务失败：%w", err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	deadline := time.Now().Add(startTimeout)
	for {
		select {
		case waitErr := <-waitCh:
			return model.ServiceStatus{}, false, fmt.Errorf("后台服务启动失败（日志：%s）：%v", env.Store.LogPath(), waitErr)
		default:
		}
		if !time.Now().Before(deadline) {
			// QA L5：超时未就绪时终止子进程，避免「先报错、后可用」的孤儿。
			_ = cmd.Process.Kill()
			select {
			case <-waitCh:
			case <-time.After(2 * time.Second):
			}
			return model.ServiceStatus{}, false, fmt.Errorf("本地服务启动超时，已终止子进程（日志：%s）", env.Store.LogPath())
		}
		time.Sleep(startPollInterval)
		if status, ok := server.DetectRunning(env.Store); ok {
			return status, false, nil
		}
	}
}

// ---------------------------------------------------------------- 跨进程操作锁

// lockPath 返回 CLI 跨进程操作锁路径：<home>/server.lock（QA M1）。
func lockPath(store *config.Store) string {
	return filepath.Join(store.HomeDir(), "server.lock")
}

// withLock 在操作锁内执行 fn，保证同一时间只有一个 CLI 进程管理本地服务。
func withLock(store *config.Store, fn func() error) error {
	release, err := acquireLock(store)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

// acquireLock 用 O_CREATE|O_EXCL 创建锁文件；陈旧锁（mtime 超过 lockStaleAge）
// 清理后重试；最多等待 lockWaitTimeout。
func acquireLock(store *config.Store) (func(), error) {
	path := lockPath(store)
	deadline := time.Now().Add(lockWaitTimeout)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
			_ = file.Close()
			return func() { releaseLock(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("创建操作锁失败：%w", err)
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > lockStaleAge {
			_ = os.Remove(path)
			continue
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("另一个 ykt 进程正在操作本地服务，请稍后重试（锁文件：%s）", path)
		}
		time.Sleep(lockRetryInterval)
	}
}

// releaseLock 只在锁文件仍归当前进程时删除，避免误删他人新建的锁。
func releaseLock(path string) {
	content, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if strings.TrimSpace(string(content)) != strconv.Itoa(os.Getpid()) {
		return
	}
	_ = os.Remove(path)
}

// cleanupStaleTempFiles 清理配置目录里陈旧的原子写临时文件（QA L1）。
func cleanupStaleTempFiles(store *config.Store) {
	if store == nil {
		return
	}
	entries, err := os.ReadDir(store.HomeDir())
	if err != nil {
		return
	}
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), ".tmp-") {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) < staleTempFileAge {
			continue
		}
		_ = os.Remove(filepath.Join(store.HomeDir(), entry.Name()))
	}
}

// waitServiceStopped 轮询 /health 直到服务停止（或超时）。
func waitServiceStopped(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if _, running := server.CheckHealth(port, 250*time.Millisecond); !running {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(startPollInterval)
	}
}

// serviceBase 返回服务根地址（不含末尾斜杠）。
func serviceBase(status model.ServiceStatus) string {
	if status.URL != "" {
		return strings.TrimSuffix(status.URL, "/")
	}
	return fmt.Sprintf("http://127.0.0.1:%d", status.Port)
}

// openInstallPage 打开脚本安装地址，并在真正打开后记录安装信息。
func openInstallPage(opts *options, env Env, o *output, base string, meta model.ScriptMeta) error {
	installURL := base + "/ykt.user.js"
	o.info(fmt.Sprintf("准备安装 / 更新脚本 v%s（指纹 %s）", meta.Version, meta.ShortHash))
	if opts.noOpen {
		o.info("[no-open] " + installURL)
		return nil
	}
	if err := browser.OpenURL(installURL); err != nil {
		o.warn("无法自动打开浏览器，请手动访问：" + installURL)
		return nil
	}
	state := model.InstallState{
		Installed: true,
		InSync:    true,
		Version:   meta.Version,
		Hash:      meta.Hash,
		OpenedAt:  time.Now().UnixMilli(),
		Note:      "已在浏览器打开安装页",
	}
	if err := env.Store.SaveInstallState(state); err != nil {
		o.warn("写入安装记录失败：" + err.Error())
	}
	return nil
}

// openTarget 用默认浏览器打开目标网址；--no-open 时只输出地址。
func openTarget(opts *options, o *output, target string) error {
	if strings.TrimSpace(target) == "" {
		return errors.New("要打开的网址为空")
	}
	if opts.noOpen {
		o.info("[no-open] " + target)
		return nil
	}
	if err := browser.OpenURL(target); err != nil {
		o.warn("无法自动打开浏览器，请手动访问：" + target)
	}
	return nil
}

// formatTime 把 Unix 毫秒格式化为本地时间。
func formatTime(ms int64) string {
	if ms <= 0 {
		return "—"
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04:05")
}

// fallback 在字符串为空时返回替代值。
func fallback(value, replacement string) string {
	if strings.TrimSpace(value) == "" {
		return replacement
	}
	return value
}

// firstN 返回字符串前 n 个字符（按字节，用于十六进制指纹）。
func firstN(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n]
}
