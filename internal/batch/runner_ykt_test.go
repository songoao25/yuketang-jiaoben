package batch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

const fakeStartScript = `(function(){
  function mount(){
    if (document.querySelector('#btn-start')) return;
    var btn = document.createElement('button');
    btn.id = 'btn-start';
    btn.textContent = '开始刷课';
    document.body.appendChild(btn);
    btn.addEventListener('click', function(){
      btn.textContent = '停止刷课';
      window.__yktBatchRunning = true;
      setTimeout(function(){
        btn.textContent = '开始刷课';
        window.__yktBatchDone = true;
      }, 200);
    });
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', mount);
  else mount();
})();`

func fakeCourseServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><title>课程目录</title></head><body><h1>课程目录</h1></body></html>`)
	}))
	t.Cleanup(server.Close)
	return server
}

func newTestRunner(t *testing.T, baseDir, script string) *YKTRunner {
	t.Helper()
	return &YKTRunner{
		ScriptSource: script,
		ProfileDir: func(account Account) (string, error) {
			return filepath.Join(baseDir, account.ID), nil
		},
		Headless:     true,
		StartTimeout: 10 * time.Second,
		RunTimeout:   10 * time.Second,
		PollInterval: 60 * time.Millisecond,
	}
}

func TestYKTRunnerClicksStartAndWaitsDone(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过真实浏览器测试")
	}
	if _, err := ChromePath(); err != nil {
		t.Skipf("本机没有 Chrome/Chromium：%v", err)
	}
	server := fakeCourseServer(t)
	runner := newTestRunner(t, t.TempDir(), fakeStartScript)
	var logs []string
	outcome := runner.Run(context.Background(), Account{ID: "acc_a", Name: "测试", Site: server.URL}, func(level, text string) {
		logs = append(logs, level+":"+text)
	})
	if outcome.Err != nil || outcome.NeedsLogin {
		t.Fatalf("运行失败：%+v，日志：%v", outcome, logs)
	}
	if len(logs) == 0 {
		t.Fatal("应输出运行日志")
	}
}

func TestYKTRunnerNeedsLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过真实浏览器测试")
	}
	if _, err := ChromePath(); err != nil {
		t.Skipf("本机没有 Chrome/Chromium：%v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>登录</title><form><input type="password"></form>`)
	}))
	defer server.Close()
	runner := newTestRunner(t, t.TempDir(), fakeStartScript)
	outcome := runner.Run(context.Background(), Account{ID: "acc_b", Name: "测试", Site: server.URL}, nil)
	if !outcome.NeedsLogin {
		t.Fatalf("登录页应返回 NeedsLogin：%+v", outcome)
	}
}

func TestYKTRunnerTimesOutWithoutPanel(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过真实浏览器测试")
	}
	if _, err := ChromePath(); err != nil {
		t.Skipf("本机没有 Chrome/Chromium：%v", err)
	}
	server := fakeCourseServer(t)
	runner := newTestRunner(t, t.TempDir(), "")
	runner.StartTimeout = 400 * time.Millisecond
	outcome := runner.Run(context.Background(), Account{ID: "acc_c", Name: "测试", Site: server.URL}, nil)
	if outcome.Err == nil || outcome.NeedsLogin {
		t.Fatalf("没有面板时应超时失败：%+v", outcome)
	}
}
