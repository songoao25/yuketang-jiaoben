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

func TestLoginSignalsLoggedIn(t *testing.T) {
	cases := []struct {
		name    string
		signals LoginSignals
		want    bool
	}{
		{"空 URL", LoginSignals{}, false},
		{"登录页", LoginSignals{URL: "https://www.yuketang.cn/passport/login"}, false},
		{"含密码框", LoginSignals{URL: "https://www.yuketang.cn/v2/web/index", HasPasswordForm: true}, false},
		{"学校 SSO", LoginSignals{URL: "https://sso.example.edu.cn/login?next=yuketang"}, false},
		{"登录后首页", LoginSignals{URL: "https://www.yuketang.cn/v2/web/index"}, true},
		{"登录后课程页", LoginSignals{URL: "https://www.yuketang.cn/v2/web/studentLog/123"}, true},
	}
	for _, tc := range cases {
		if got := tc.signals.LoggedIn(); got != tc.want {
			t.Errorf("%s: LoggedIn() = %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

func TestLoginSessionDetectsFakePages(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过真实浏览器测试")
	}
	if _, err := ChromePath(); err != nil {
		t.Skipf("本机没有 Chrome/Chromium：%v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>登录</title><form><input type="password" name="pwd"></form>`)
	})
	mux.HandleFunc("/home", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>首页</title><div id="courses">课程</div>`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	session, err := newLoginSession(ctx, "acc_test", filepath.Join(t.TempDir(), "profile"), server.URL+"/login", false)
	if err != nil {
		t.Fatalf("启动登录会话失败：%v", err)
	}
	defer session.Close()

	signals, logged, err := session.LoggedIn(ctx)
	if err != nil {
		t.Fatalf("读取登录信号失败：%v", err)
	}
	if logged {
		t.Fatalf("登录页应判定未登录：%+v", signals)
	}
	if err := session.conn.Navigate(ctx, session.session, server.URL+"/home"); err != nil {
		t.Fatalf("导航到假首页失败：%v", err)
	}
	signals, logged, err = session.LoggedIn(ctx)
	if err != nil {
		t.Fatalf("读取登录信号失败：%v", err)
	}
	if !logged {
		t.Fatalf("假首页应判定已登录：%+v", signals)
	}
}
