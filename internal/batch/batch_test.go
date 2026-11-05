package batch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPage(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><title>batch-spike</title></head><body><h1 id="v">ready</h1></body></html>`)
	}))
	t.Cleanup(server.Close)
	return server
}

func launchForTest(t *testing.T, profileDir string) *cdpConn {
	t.Helper()
	if testing.Short() {
		t.Skip("short 模式跳过真实浏览器测试")
	}
	if _, err := ChromePath(); err != nil {
		t.Skipf("本机没有 Chrome/Chromium：%v", err)
	}
	conn, err := startCDP(profileDir, false)
	if err != nil {
		t.Fatalf("启动 headless Chrome 失败：%v", err)
	}
	t.Cleanup(conn.Close)
	return conn
}

func openSession(t *testing.T, conn *cdpConn, url string) (context.Context, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	target, err := conn.CreateTarget(ctx, "about:blank")
	if err != nil {
		t.Fatalf("CreateTarget 失败：%v", err)
	}
	session, err := conn.Attach(ctx, target)
	if err != nil {
		t.Fatalf("Attach 失败：%v", err)
	}
	if err := conn.EnablePage(ctx, session); err != nil {
		t.Fatalf("EnablePage 失败：%v", err)
	}
	if err := conn.Navigate(ctx, session, url); err != nil {
		t.Fatalf("Navigate 失败：%v", err)
	}
	return ctx, session
}

func TestBrowserArgsHeadlessAndPipe(t *testing.T) {
	args := strings.Join(browserArgs("/tmp/profile", false), " ")
	for _, want := range []string{"--remote-debugging-pipe", "--headless=new", "--user-data-dir=/tmp/profile"} {
		if !strings.Contains(args, want) {
			t.Fatalf("无头参数缺少 %q：%s", want, args)
		}
	}
	visible := strings.Join(browserArgs("/tmp/profile", true), " ")
	if strings.Contains(visible, "--headless") {
		t.Fatalf("可见登录模式不应带 --headless：%s", visible)
	}
}

func TestHeadlessNavigateAndEvaluate(t *testing.T) {
	server := testPage(t)
	conn := launchForTest(t, filepath.Join(t.TempDir(), "profile"))
	ctx, session := openSession(t, conn, server.URL)

	title, err := conn.EvaluateString(ctx, session, "document.title")
	if err != nil {
		t.Fatalf("读取 title 失败：%v", err)
	}
	if title != "batch-spike" {
		t.Fatalf("title = %q，期望 batch-spike", title)
	}
	value, err := conn.EvaluateString(ctx, session, "1 + 1")
	if err != nil {
		t.Fatalf("执行 JS 失败：%v", err)
	}
	if value != "2" {
		t.Fatalf("1+1 = %q，期望 2", value)
	}
	text, err := conn.EvaluateString(ctx, session, "document.querySelector('#v').textContent")
	if err != nil || text != "ready" {
		t.Fatalf("读取 DOM 失败：%q %v", text, err)
	}
}

func TestInjectOnNewDocument(t *testing.T) {
	server := testPage(t)
	conn := launchForTest(t, filepath.Join(t.TempDir(), "profile"))
	ctx, session := openSession(t, conn, server.URL)

	if err := conn.InjectOnNewDocument(ctx, session, "window.__batchInjected = 'yes';"); err != nil {
		t.Fatalf("注入脚本失败：%v", err)
	}
	if err := conn.Navigate(ctx, session, server.URL); err != nil {
		t.Fatalf("二次导航失败：%v", err)
	}
	value, err := conn.EvaluateString(ctx, session, "window.__batchInjected || ''")
	if err != nil {
		t.Fatalf("读取注入结果失败：%v", err)
	}
	if value != "yes" {
		t.Fatalf("注入未生效：%q", value)
	}
}

func TestProfilesAreIsolatedAndPrivate(t *testing.T) {
	server := testPage(t)
	root := t.TempDir()
	profileA := filepath.Join(root, "a")
	profileB := filepath.Join(root, "b")
	connA := launchForTest(t, profileA)
	connB := launchForTest(t, profileB)

	ctxA, sessionA := openSession(t, connA, server.URL)
	if _, err := connA.EvaluateString(ctxA, sessionA, "localStorage.setItem('batch-profile', 'A')"); err != nil {
		t.Fatalf("写入 A profile 失败：%v", err)
	}
	ctxB, sessionB := openSession(t, connB, server.URL)
	value, err := connB.EvaluateString(ctxB, sessionB, "localStorage.getItem('batch-profile') || ''")
	if err != nil {
		t.Fatalf("读取 B profile 失败：%v", err)
	}
	if value != "" {
		t.Fatalf("两个账号 profile 串号了：B 读到了 %q", value)
	}
	info, err := os.Stat(profileA)
	if err != nil {
		t.Fatalf("profile 目录不存在：%v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("profile 目录权限过宽：%o", info.Mode().Perm())
	}
}
