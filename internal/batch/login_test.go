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
		{"含密码框", LoginSignals{URL: "https://www.yuketang.cn/v2/web/index", PostLoginPage: true, HasPasswordForm: true}, false},
		{"学校 SSO", LoginSignals{URL: "https://sso.example.edu.cn/login?next=yuketang"}, false},
		{"未登录主页", LoginSignals{URL: "https://www.yuketang.cn/"}, false},
		{"扫码登录页", LoginSignals{URL: "https://www.yuketang.cn/passport/login", HasLoginUI: true}, false},
		{"登录后首页", LoginSignals{URL: "https://www.yuketang.cn/v2/web/index", PostLoginPage: true}, true},
		{"登录后课程页", LoginSignals{URL: "https://www.yuketang.cn/v2/web/studentLog/123", PostLoginPage: true}, true},
		{"主页但登录态 Cookie 变了", LoginSignals{URL: "https://www.yuketang.cn/", CookieChanged: true}, true},
		{"登录 UI 未消失", LoginSignals{URL: "https://www.yuketang.cn/", HasLoginUI: true, CookieChanged: true}, false},
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
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>雨课堂首页</title><div id="home">欢迎</div>`)
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>登录</title><div>扫码登录</div><div>手机号 验证码</div>`)
	})
	mux.HandleFunc("/set-cookie", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>首页</title><div id="home">欢迎</div>`)
	})
	mux.HandleFunc("/v2/web/index", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>我的课程</title><div id="courses">课程列表</div>`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	session, err := newLoginSession(ctx, "acc_test", filepath.Join(t.TempDir(), "profile"), server.URL+"/", false)
	if err != nil {
		t.Fatalf("启动登录会话失败：%v", err)
	}
	defer session.Close()

	// 公开主页：不能判定已登录。
	signals, logged, err := session.LoggedIn(ctx)
	if err != nil {
		t.Fatalf("读取主页信号失败：%v", err)
	}
	if logged {
		t.Fatalf("未登录主页不应判定已登录：%+v", signals)
	}

	// 扫码登录页：没有密码框也不能判定已登录。
	if err := session.conn.Navigate(ctx, session.session, server.URL+"/login"); err != nil {
		t.Fatalf("导航到扫码登录页失败：%v", err)
	}
	signals, logged, err = session.LoggedIn(ctx)
	if err != nil {
		t.Fatalf("读取登录页信号失败：%v", err)
	}
	if logged || !signals.HasLoginUI {
		t.Fatalf("扫码登录页应判定未登录且识别登录 UI：%+v", signals)
	}

	// 登录后的业务页：判定已登录。
	if err := session.conn.Navigate(ctx, session.session, server.URL+"/v2/web/index"); err != nil {
		t.Fatalf("导航到登录后页面失败：%v", err)
	}
	signals, logged, err = session.LoggedIn(ctx)
	if err != nil {
		t.Fatalf("读取登录后信号失败：%v", err)
	}
	if !logged || !signals.PostLoginPage {
		t.Fatalf("登录后业务页应判定已登录：%+v", signals)
	}
}

func TestLoginSessionDetectsCookieChange(t *testing.T) {
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
	defer session.Close()

	if _, logged, _ := session.LoggedIn(ctx); logged {
		t.Fatal("刚打开的主页不应判定已登录")
	}
	if _, err := session.conn.Evaluate(ctx, session.session, `document.cookie = "sessionid=test-login; path=/"; true`); err != nil {
		t.Fatalf("写入测试 Cookie 失败：%v", err)
	}
	signals, logged, err := session.LoggedIn(ctx)
	if err != nil {
		t.Fatalf("读取信号失败：%v", err)
	}
	if !logged || !signals.CookieChanged {
		t.Fatalf("登录态 Cookie 变化应判定已登录：%+v", signals)
	}
}
