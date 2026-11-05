package cli

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/songoao25/yuketang-jiaoben/internal/config"
	"github.com/songoao25/yuketang-jiaoben/internal/model"
	"github.com/songoao25/yuketang-jiaoben/internal/script"
	"github.com/songoao25/yuketang-jiaoben/internal/server"
)

const cliTestScript = `// ==UserScript==
// @name         测试脚本
// @version      9.9.9
// @author       测试作者
// @match        *://*.yuketang.cn/*
// @match        *://*.gdufemooc.cn/*
// ==/UserScript==

(() => { console.log('cli test'); })();
`

const cliTestAppVersion = "1.0.0-test"

// cliTestEnv 创建只写内存的 CLI 环境；Executable 故意指向不存在的路径，
// 保证测试绝不会派生真实后台进程。
func cliTestEnv(t *testing.T) (Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	store, err := config.OpenAt(t.TempDir())
	if err != nil {
		t.Fatalf("OpenAt 失败: %v", err)
	}
	var stdout, stderr bytes.Buffer
	return Env{
		Store:        store,
		ScriptSource: cliTestScript,
		AppVersion:   cliTestAppVersion,
		Stdout:       &stdout,
		Stderr:       &stderr,
		Executable:   "/nonexistent/ykt-test-binary",
	}, &stdout, &stderr
}

// startInProcessService 在测试进程内启动本地服务（供复用路径测试）。
func startInProcessService(t *testing.T, store *config.Store) *server.Server {
	t.Helper()
	return startInProcessServiceOwner(t, store, server.OwnerDaemon)
}

// startInProcessServiceOwner 在测试进程内启动指定 owner 的本地服务。
func startInProcessServiceOwner(t *testing.T, store *config.Store, owner string) *server.Server {
	t.Helper()
	port := pickCLITestPort(t)
	srv := server.New(server.Options{
		Store:        store,
		ScriptSource: cliTestScript,
		AppVersion:   cliTestAppVersion,
		Port:         port,
		Owner:        owner,
	})
	if _, err := srv.Start(); err != nil {
		t.Fatalf("启动测试服务失败: %v", err)
	}
	t.Cleanup(func() {
		if err := srv.Stop(); err != nil {
			t.Errorf("停止测试服务失败: %v", err)
		}
	})
	return srv
}

// pickCLITestPort 在 [MinPort, MaxPort] 内找一个空闲端口；全占用时跳过测试。
func pickCLITestPort(t *testing.T) int {
	t.Helper()
	for port := model.MinPort; port <= model.MaxPort; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		_ = listener.Close()
		return port
	}
	t.Skipf("端口 %d~%d 全被占用，跳过测试", model.MinPort, model.MaxPort)
	return 0
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		check  func(*testing.T, *options)
		hasErr bool
	}{
		{
			name: "无参数",
			args: nil,
			check: func(t *testing.T, opts *options) {
				if opts.command != "" {
					t.Fatalf("command = %q, want 空", opts.command)
				}
			},
		},
		{
			name: "子命令",
			args: []string{"status"},
			check: func(t *testing.T, opts *options) {
				if opts.command != "status" {
					t.Fatalf("command = %q", opts.command)
				}
			},
		},
		{
			name: "端口短参数",
			args: []string{"-p", "8900", "start"},
			check: func(t *testing.T, opts *options) {
				if !opts.portSet || opts.port != 8900 || opts.command != "start" {
					t.Fatalf("解析结果异常: %+v", opts)
				}
			},
		},
		{
			name: "端口等号参数",
			args: []string{"--port=8901", "serve"},
			check: func(t *testing.T, opts *options) {
				if !opts.portSet || opts.port != 8901 || opts.command != "serve" {
					t.Fatalf("解析结果异常: %+v", opts)
				}
			},
		},
		{
			name: "网址参数",
			args: []string{"-u", "https://changjiang.yuketang.cn/"},
			check: func(t *testing.T, opts *options) {
				if !opts.urlSet || opts.url != "https://changjiang.yuketang.cn/" {
					t.Fatalf("解析结果异常: %+v", opts)
				}
			},
		},
		{
			name: "网址等号参数",
			args: []string{"--url=https://example.com/"},
			check: func(t *testing.T, opts *options) {
				if !opts.urlSet || opts.url != "https://example.com/" {
					t.Fatalf("解析结果异常: %+v", opts)
				}
			},
		},
		{
			name: "布尔参数",
			args: []string{"-f", "--no-open", "--daemon", "install"},
			check: func(t *testing.T, opts *options) {
				if !opts.force || !opts.noOpen || !opts.daemon || opts.command != "install" {
					t.Fatalf("解析结果异常: %+v", opts)
				}
			},
		},
		{
			name: "帮助与版本",
			args: []string{"-h"},
			check: func(t *testing.T, opts *options) {
				if !opts.help {
					t.Fatal("help 未置位")
				}
			},
		},
		{
			name: "版本短参数",
			args: []string{"-v"},
			check: func(t *testing.T, opts *options) {
				if !opts.version {
					t.Fatal("version 未置位")
				}
			},
		},
		{
			name: "open 位置参数",
			args: []string{"open", "https://changjiang.yuketang.cn/"},
			check: func(t *testing.T, opts *options) {
				if opts.command != "open" || len(opts.positionals) != 1 || opts.positionals[0] != "https://changjiang.yuketang.cn/" {
					t.Fatalf("解析结果异常: %+v", opts)
				}
			},
		},
		{name: "端口缺值", args: []string{"-p"}, hasErr: true},
		{name: "端口非数字", args: []string{"-p", "abc"}, hasErr: true},
		{name: "端口为空", args: []string{"--port="}, hasErr: true},
		{name: "网址缺值", args: []string{"--url"}, hasErr: true},
		{name: "未知参数", args: []string{"--bogus"}, hasErr: true},
		{name: "未知短参数", args: []string{"-x"}, hasErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := parseArgs(tt.args)
			if tt.hasErr {
				if err == nil {
					t.Fatalf("parseArgs(%v) 应返回错误", tt.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseArgs(%v) 失败: %v", tt.args, err)
			}
			tt.check(t, opts)
		})
	}
}

func TestRunHelp(t *testing.T) {
	env, stdout, _ := cliTestEnv(t)
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		stdout.Reset()
		if code := Run(args, env); code != 0 {
			t.Fatalf("Run(%v) = %d, want 0", args, code)
		}
		output := stdout.String()
		for _, want := range []string{"用法", "ykt install", "ykt serve", "ykt status", "--no-open"} {
			if !strings.Contains(output, want) {
				t.Fatalf("帮助输出缺少 %q:\n%s", want, output)
			}
		}
		if strings.Contains(output, "\x1b[") {
			t.Fatalf("bytes.Buffer 输出不应包含 ANSI 颜色: %q", output)
		}
	}
}

func TestRunVersion(t *testing.T) {
	env, stdout, _ := cliTestEnv(t)
	meta, err := script.FromSource(cliTestScript)
	if err != nil {
		t.Fatalf("解析测试脚本失败: %v", err)
	}
	for _, args := range [][]string{{"version"}, {"-v"}, {"--version"}} {
		stdout.Reset()
		if code := Run(args, env); code != 0 {
			t.Fatalf("Run(%v) = %d, want 0", args, code)
		}
		output := stdout.String()
		for _, want := range []string{
			"App v" + cliTestAppVersion,
			"脚本 v" + meta.Version,
			meta.ShortHash,
		} {
			if !strings.Contains(output, want) {
				t.Fatalf("版本输出缺少 %q:\n%s", want, output)
			}
		}
	}
}

func TestRunUnknownCommand(t *testing.T) {
	env, _, stderr := cliTestEnv(t)
	if code := Run([]string{"bogus"}, env); code != 2 {
		t.Fatalf("Run(未知命令) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "✖") || !strings.Contains(stderr.String(), "未知命令") {
		t.Fatalf("错误输出不符合风格:\n%s", stderr.String())
	}
}

func TestRunUnknownFlag(t *testing.T) {
	env, _, stderr := cliTestEnv(t)
	if code := Run([]string{"--bogus"}, env); code != 2 {
		t.Fatalf("Run(未知参数) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "未知参数") {
		t.Fatalf("错误输出异常:\n%s", stderr.String())
	}
}

func TestRunStatusWithoutService(t *testing.T) {
	env, stdout, _ := cliTestEnv(t)
	meta, err := script.FromSource(cliTestScript)
	if err != nil {
		t.Fatalf("解析测试脚本失败: %v", err)
	}
	if code := Run([]string{"status"}, env); code != 0 {
		t.Fatalf("Run(status) = %d, want 0", code)
	}
	output := stdout.String()
	for _, want := range []string{
		meta.Version,
		meta.ShortHash,
		"未运行",
		"暂无",
		env.Store.ConfigPath(),
		model.DefaultURL,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("status 输出缺少 %q:\n%s", want, output)
		}
	}
}

func TestRunStopWithoutService(t *testing.T) {
	env, stdout, _ := cliTestEnv(t)
	if code := Run([]string{"stop"}, env); code != 0 {
		t.Fatalf("Run(stop) = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "未运行") {
		t.Fatalf("stop 输出异常:\n%s", stdout.String())
	}
}

func TestRunStopDoesNotKillSelf(t *testing.T) {
	env, stdout, _ := cliTestEnv(t)
	startInProcessService(t, env.Store)

	if code := Run([]string{"stop"}, env); code != 0 {
		t.Fatalf("Run(stop) = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "当前进程管理") {
		t.Fatalf("应识别出服务由当前进程管理:\n%s", stdout.String())
	}
	if _, ok := env.Store.LoadServerInfo(); ok {
		t.Fatal("pid 为当前进程时应清理记录")
	}
	// 当前测试进程仍然存活。
	if os.Getpid() <= 0 {
		t.Fatal("unreachable")
	}
}

func TestRunPanelReusesService(t *testing.T) {
	env, stdout, _ := cliTestEnv(t)
	srv := startInProcessService(t, env.Store)

	if code := Run([]string{"panel", "--no-open"}, env); code != 0 {
		t.Fatalf("Run(panel) = %d, want 0", code)
	}
	output := stdout.String()
	if !strings.Contains(output, "[no-open]") {
		t.Fatalf("--no-open 应只打印地址:\n%s", output)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d/", srv.Port())
	if !strings.Contains(output, base) {
		t.Fatalf("输出缺少服务地址 %s:\n%s", base, output)
	}
}

func TestRunInstallNoOpen(t *testing.T) {
	env, stdout, _ := cliTestEnv(t)
	startInProcessService(t, env.Store)

	if code := Run([]string{"install", "--no-open"}, env); code != 0 {
		t.Fatalf("Run(install) = %d, want 0", code)
	}
	output := stdout.String()
	if !strings.Contains(output, "[no-open]") || !strings.Contains(output, "/ykt.user.js") {
		t.Fatalf("install 输出异常:\n%s", output)
	}
	// --no-open 时不应写入安装记录。
	if state := env.Store.LoadInstallState(); state.Hash != "" {
		t.Fatalf("--no-open 不应写安装记录: %+v", state)
	}
}

func TestRunStartNoOpen(t *testing.T) {
	env, stdout, _ := cliTestEnv(t)
	startInProcessService(t, env.Store)

	if code := Run([]string{"start", "--no-open"}, env); code != 0 {
		t.Fatalf("Run(start) = %d, want 0", code)
	}
	output := stdout.String()
	for _, want := range []string{"本地服务已在运行", "[no-open]", "下一步"} {
		if !strings.Contains(output, want) {
			t.Fatalf("start 输出缺少 %q:\n%s", want, output)
		}
	}
}

func TestRunStartSkipsInstallWhenInSync(t *testing.T) {
	env, stdout, _ := cliTestEnv(t)
	startInProcessService(t, env.Store)
	meta, err := script.FromSource(cliTestScript)
	if err != nil {
		t.Fatalf("解析测试脚本失败: %v", err)
	}
	if err := env.Store.SaveInstallState(model.InstallState{
		Installed: true,
		InSync:    true,
		Version:   meta.Version,
		Hash:      meta.Hash,
		OpenedAt:  time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("写入安装记录失败: %v", err)
	}

	if code := Run([]string{"start", "--no-open"}, env); code != 0 {
		t.Fatalf("Run(start) = %d, want 0", code)
	}
	output := stdout.String()
	if !strings.Contains(output, "已安装过") {
		t.Fatalf("已安装且指纹一致时应提示跳过安装:\n%s", output)
	}
	if strings.Contains(output, "[no-open] http") && strings.Contains(output, "/ykt.user.js") {
		t.Fatalf("已同步时不应再准备安装页:\n%s", output)
	}
}

func TestRunOpenSavesDefaultURL(t *testing.T) {
	env, stdout, _ := cliTestEnv(t)
	startInProcessService(t, env.Store)

	target := "https://changjiang.yuketang.cn/"
	if code := Run([]string{"open", target, "--no-open"}, env); code != 0 {
		t.Fatalf("Run(open) = %d, want 0", code)
	}
	output := stdout.String()
	for _, want := range []string{"已记住默认课程地址", target, "已打开"} {
		if !strings.Contains(output, want) {
			t.Fatalf("open 输出缺少 %q:\n%s", want, output)
		}
	}
	if got := env.Store.LoadSettings().DefaultURL; got != target {
		t.Fatalf("默认地址 = %q, want %q", got, target)
	}
}

func TestRunOpenRejectsNonHTTPURL(t *testing.T) {
	env, _, stderr := cliTestEnv(t)
	if code := Run([]string{"open", "file:///etc/passwd", "--no-open"}, env); code != 1 {
		t.Fatalf("Run(open 非法网址) = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "http://") && !strings.Contains(stderr.String(), "https://") {
		t.Fatalf("错误信息应说明仅支持 http(s):\n%s", stderr.String())
	}
}

func TestRunRejectsOutOfRangePort(t *testing.T) {
	env, _, stderr := cliTestEnv(t)
	if code := Run([]string{"start", "-p", "9000", "--no-open"}, env); code != 1 {
		t.Fatalf("Run(越界端口) = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "端口必须在") {
		t.Fatalf("错误信息异常:\n%s", stderr.String())
	}
}

func TestRunStatusWithBrokenScript(t *testing.T) {
	env, _, stderr := cliTestEnv(t)
	env.ScriptSource = "console.log('no header')"
	if code := Run([]string{"status"}, env); code != 1 {
		t.Fatalf("Run(status 损坏脚本) = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "✖") {
		t.Fatalf("错误输出缺少 ✖:\n%s", stderr.String())
	}
}

func TestNormalizeCommand(t *testing.T) {
	cases := map[string]string{
		"":        "",
		"start":   "start",
		"i":       "install",
		"ui":      "panel",
		"st":      "status",
		"bogus":   "bogus",
		"install": "install",
	}
	for input, want := range cases {
		if got := normalizeCommand(input); got != want {
			t.Fatalf("normalizeCommand(%q) = %q, want %q", input, got, want)
		}
	}
}

// ---------------------------------------------------------------- QA M1/M2/M4/M5/L1/L5 回归

func TestLockAcquireAndRelease(t *testing.T) {
	env, _, _ := cliTestEnv(t)
	path := lockPath(env.Store)

	release, err := acquireLock(env.Store)
	if err != nil {
		t.Fatalf("获取锁失败: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("锁文件不存在: %v", err)
	}
	if !strings.Contains(string(content), strconv.Itoa(os.Getpid())) {
		t.Fatalf("锁文件内容应记录持有者 PID: %q", content)
	}

	// 缩短等待时间，验证锁被占用时第二次获取会超时而不是直接成功。
	oldWait, oldInterval := lockWaitTimeout, lockRetryInterval
	lockWaitTimeout, lockRetryInterval = 150*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() {
		lockWaitTimeout, lockRetryInterval = oldWait, oldInterval
	})
	if _, err := acquireLock(env.Store); err == nil {
		t.Fatal("锁被占用时第二次获取应超时")
	}

	release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("释放后锁文件应被删除")
	}
	release2, err := acquireLock(env.Store)
	if err != nil {
		t.Fatalf("释放后应能重新获取锁: %v", err)
	}
	release2()
}

func TestLockCleansStaleFile(t *testing.T) {
	env, _, _ := cliTestEnv(t)
	path := lockPath(env.Store)
	if err := os.WriteFile(path, []byte("999999\n"), 0o600); err != nil {
		t.Fatalf("写入陈旧锁失败: %v", err)
	}
	old := time.Now().Add(-2 * lockStaleAge)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("设置锁文件 mtime 失败: %v", err)
	}

	release, err := acquireLock(env.Store)
	if err != nil {
		t.Fatalf("陈旧锁应被清理后可获取: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("锁文件不存在: %v", err)
	}
	if !strings.Contains(string(content), strconv.Itoa(os.Getpid())) {
		t.Fatalf("陈旧锁未被替换为新持有者: %q", content)
	}
	release()
}

func TestCLIOperationsLeaveNoLockFile(t *testing.T) {
	env, _, _ := cliTestEnv(t)
	startInProcessService(t, env.Store)
	if code := Run([]string{"panel", "--no-open"}, env); code != 0 {
		t.Fatalf("Run(panel) = %d, want 0", code)
	}
	if _, err := os.Stat(lockPath(env.Store)); !os.IsNotExist(err) {
		t.Fatal("命令结束后不应残留 server.lock")
	}
}

func TestRunStopRefusesGUIManagedService(t *testing.T) {
	env, stdout, stderr := cliTestEnv(t)
	srv := startInProcessServiceOwner(t, env.Store, server.OwnerGUI)

	code := Run([]string{"stop"}, env)
	if code != 1 {
		t.Fatalf("Run(stop owner=gui) = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), guiManagedMessage) {
		t.Fatalf("错误输出缺少 GUI 托管提示:\nstdout=%s\nstderr=%s", stdout.String(), stderr.String())
	}
	if !srv.Running() {
		t.Fatal("owner=gui 时不得终止服务")
	}
	if _, ok := env.Store.LoadServerInfo(); !ok {
		t.Fatal("拒绝停止时应保留有效 server.json")
	}
}

func TestRunRestartRefusesGUIManagedService(t *testing.T) {
	env, stdout, stderr := cliTestEnv(t)
	srv := startInProcessServiceOwner(t, env.Store, server.OwnerGUI)

	code := Run([]string{"restart", "--no-open"}, env)
	if code != 1 {
		t.Fatalf("Run(restart owner=gui) = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), guiManagedMessage) {
		t.Fatalf("restart 错误输出缺少 GUI 托管提示:\n%s", stderr.String())
	}
	if !srv.Running() {
		t.Fatal("owner=gui 时 restart 不得终止或替换服务")
	}
	if strings.Contains(stdout.String(), "本地服务已启动") {
		t.Fatalf("owner=gui 时 restart 不应启动新服务:\n%s", stdout.String())
	}
}

func TestRunStatusMigratesLegacyData(t *testing.T) {
	env, stdout, _ := cliTestEnv(t)
	legacyConfig := `{"url":"https://changjiang.yuketang.cn/","port":8900}`
	if err := os.WriteFile(env.Store.ConfigPath(), []byte(legacyConfig), 0o600); err != nil {
		t.Fatalf("写入旧配置失败: %v", err)
	}
	hash := strings.Repeat("ab", 32)
	legacyState := fmt.Sprintf(`{"openedHash":%q,"version":"3.0.7","openedAt":1700000000000}`, hash)
	if err := os.WriteFile(env.Store.StatePath(), []byte(legacyState), 0o600); err != nil {
		t.Fatalf("写入旧安装记录失败: %v", err)
	}

	if code := Run([]string{"status"}, env); code != 0 {
		t.Fatalf("Run(status) = %d, want 0", code)
	}
	output := stdout.String()
	if !strings.Contains(output, "https://changjiang.yuketang.cn/") {
		t.Fatalf("status 未显示迁移后的默认地址:\n%s", output)
	}
	if !strings.Contains(output, "abababab") {
		t.Fatalf("status 未显示迁移后的安装记录指纹:\n%s", output)
	}
	if !strings.Contains(output, "与当前脚本一致") && !strings.Contains(output, "脚本已更新") {
		t.Fatalf("status 未识别迁移后的安装记录:\n%s", output)
	}
}

func TestEnsureServiceSpawnFailure(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh 不可用，跳过")
	}
	env, _, stderr := cliTestEnv(t)
	// /bin/sh 收到 "serve --daemon ..." 会立即报错退出，用于验证 QA L5 的失败检测。
	env.Executable = "/bin/sh"
	started := time.Now()
	code := Run([]string{"start", "--no-open"}, env)
	elapsed := time.Since(started)
	if code != 1 {
		t.Fatalf("Run(start 子进程失败) = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "后台服务启动失败") {
		t.Fatalf("错误输出未报告子进程失败:\n%s", stderr.String())
	}
	if elapsed > 3*time.Second {
		t.Fatalf("子进程已退出时应快速失败，实际耗时 %v", elapsed)
	}
	if _, err := os.Stat(lockPath(env.Store)); !os.IsNotExist(err) {
		t.Fatal("启动失败后应释放操作锁")
	}
}

func TestEnsureServiceTimeoutKillsChild(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh 不可用，跳过")
	}
	env, _, stderr := cliTestEnv(t)

	// 子进程只 sleep，不监听端口：等待超时后应被终止（QA L5）。
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	script := filepath.Join(t.TempDir(), "slow-ykt.sh")
	content := "#!/bin/sh\n echo $$ > \"$YKT_TEST_CHILD_PIDFILE\"\n exec sleep 30\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("写入测试脚本失败: %v", err)
	}
	t.Setenv("YKT_TEST_CHILD_PIDFILE", pidFile)
	env.Executable = script

	oldTimeout := startTimeout
	startTimeout = 1 * time.Second
	t.Cleanup(func() {
		startTimeout = oldTimeout
	})

	started := time.Now()
	code := Run([]string{"start", "--no-open"}, env)
	elapsed := time.Since(started)
	if code != 1 {
		t.Fatalf("Run(start 超时) = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "启动超时") {
		t.Fatalf("错误输出未报告启动超时:\n%s", stderr.String())
	}
	if elapsed > 3*time.Second {
		t.Fatalf("超时用例不应等满 4s，实际 %v", elapsed)
	}
	if _, err := os.Stat(lockPath(env.Store)); !os.IsNotExist(err) {
		t.Fatal("超时失败后应释放操作锁")
	}

	// 子进程应已被终止：等待 PID 文件出现后确认进程不存在。
	var raw []byte
	var err error
	deadlineFile := time.Now().Add(2 * time.Second)
	for {
		raw, err = os.ReadFile(pidFile)
		if err == nil {
			break
		}
		if !time.Now().Before(deadlineFile) {
			t.Fatalf("子进程未写入 PID 文件: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		t.Fatalf("PID 文件内容异常: %q", raw)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		process, findErr := os.FindProcess(pid)
		if findErr != nil {
			break
		}
		if signalErr := process.Signal(syscall.Signal(0)); signalErr != nil {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("超时后子进程 PID %d 仍存活", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
