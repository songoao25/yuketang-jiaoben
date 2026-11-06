package batch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoginSessionCapturesCookies(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过真实浏览器测试")
	}
	if _, err := ChromePath(); err != nil {
		t.Skipf("本机没有 Chrome/Chromium：%v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>首页</title><div>欢迎</div>`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	session, err := newLoginSession(ctx, "acc_cookie", filepath.Join(t.TempDir(), "profile"), server.URL+"/", false)
	if err != nil {
		t.Fatalf("启动登录会话失败：%v", err)
	}
	if _, err := session.conn.Evaluate(ctx, session.session, `document.cookie = "sessionid=persisted; path=/"; true`); err != nil {
		t.Fatalf("写入 Cookie 失败：%v", err)
	}
	records, err := session.CookieRecords(ctx)
	if err != nil {
		t.Fatalf("读取 Cookie 失败：%v", err)
	}
	if !HasAuthCookie(records) {
		t.Fatalf("应识别到登录态 Cookie：%+v", records)
	}
	session.Close() // 优雅关闭，不应 panic/阻塞
}

func TestRunnerInjectsSavedCookies(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过真实浏览器测试")
	}
	if _, err := ChromePath(); err != nil {
		t.Skipf("本机没有 Chrome/Chromium：%v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只有注入了 sessionid Cookie，页面才给脚本面板。
		cookie, _ := r.Cookie("sessionid")
		panel := ""
		if cookie != nil && cookie.Value == "ok" {
			panel = `<div id="btn-start">开始刷课</div><script>document.getElementById('btn-start').addEventListener('click',function(){window.__yktBatchDone=true;});</script>`
		}
		fmt.Fprintf(w, `<!doctype html><title>课程目录</title>%s`, panel)
	}))
	defer server.Close()
	runner := &YKTRunner{
		ScriptSource: "",
		ProfileDir: func(Account) (string, error) {
			return filepath.Join(t.TempDir(), "profile"), nil
		},
		Cookies: func(Account) ([]CookieRecord, error) {
			return []CookieRecord{{Name: "sessionid", Value: "ok", Domain: "127.0.0.1", Path: "/"}}, nil
		},
		Headless: true, StartTimeout: 8 * time.Second, RunTimeout: 8 * time.Second, PollInterval: 100 * time.Millisecond,
	}
	outcome := runner.Run(context.Background(), Account{ID: "cookie-acc", Name: "Cookie", Site: server.URL + "/"}, nil)
	if outcome.Err != nil || outcome.NeedsLogin {
		t.Fatalf("注入 Cookie 后应能运行：%+v", outcome)
	}
	// 对比：不注入 Cookie 时应等不到面板。
	runner.Cookies = nil
	runner.StartTimeout = 500 * time.Millisecond
	outcome = runner.Run(context.Background(), Account{ID: "cookie-acc2", Name: "Cookie2", Site: server.URL + "/"}, nil)
	if outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "面板") {
		t.Fatalf("没有 Cookie 应等不到面板：%+v", outcome)
	}
}
