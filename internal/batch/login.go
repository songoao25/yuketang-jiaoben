package batch

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// LoginSignals 是从登录页面读到的登录判断信号。
type LoginSignals struct {
	URL             string `json:"url"`
	Title           string `json:"title"`
	HasPasswordForm bool   `json:"has_password_form"`
	HasLoginUI      bool   `json:"has_login_ui"`    // 密码框/手机号输入/扫码登录/登录表单文案
	PostLoginPage   bool   `json:"post_login_page"` // 位于登录后的业务页面（/v2/、课程页等）
	CookieChanged   bool   `json:"cookie_changed"`  // 与打开浏览器时的 Cookie 快照相比出现了新增/变化
}

// LoggedIn 做保守判断：必须「不在登录页 + 没有登录 UI」，
// 并且出现登录后的业务页面或登录态 Cookie 变化，才认为已登录。
// 主页、扫码页、未登录的业务页都不会被误判。
func (s LoginSignals) LoggedIn() bool {
	raw := strings.TrimSpace(s.URL)
	if raw == "" {
		return false
	}
	if isLoginURL(raw) || s.HasPasswordForm || s.HasLoginUI {
		return false
	}
	return s.PostLoginPage || s.CookieChanged
}

func isLoginURL(raw string) bool {
	lower := strings.ToLower(raw)
	for _, marker := range []string{"login", "passport", "sso", "signin", "signin", "qrcode", "qrlogin"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// isPostLoginURL 判断是否在登录后的业务页面（首页 / 不算）。
func isPostLoginURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	path := strings.ToLower(strings.TrimSpace(parsed.Path))
	if path == "" || path == "/" {
		return false
	}
	for _, marker := range []string{"login", "passport", "sso", "signin"} {
		if strings.Contains(path, marker) {
			return false
		}
	}
	for _, marker := range []string{"/v2/", "/studentlog", "/course", "/learn", "/my/", "/workspace"} {
		if strings.Contains(path, marker) {
			return true
		}
	}
	return false
}

// LoginSession 是一次可见浏览器登录会话：打开登录页、读信号、成功后关闭。
type LoginSession struct {
	accountID       string
	conn            *cdpConn
	session         string
	baselineCookies map[string]string
}

// StartLoginSession 打开可见浏览器到站点登录页；visible=false 仅用于测试。
func StartLoginSession(ctx context.Context, accountID, profileDir, site string) (*LoginSession, error) {
	return newLoginSession(ctx, accountID, profileDir, site, true)
}

func newLoginSession(ctx context.Context, accountID, profileDir, site string, visible bool) (*LoginSession, error) {
	session, err := startCDP(profileDir, visible)
	//nolint:staticcheck // 命名与既有 CDP 类型保持一致
	if err != nil {
		return nil, fmt.Errorf("启动登录浏览器失败：%v", err)
	}
	conn := session
	target, err := conn.CreateTarget(ctx, "about:blank")
	if err != nil {
		conn.Close()
		return nil, err
	}
	attached, err := conn.Attach(ctx, target)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.EnablePage(ctx, attached); err != nil {
		conn.Close()
		return nil, err
	}
	if strings.TrimSpace(site) == "" {
		site = "https://yuketang.cn/"
	}
	if err := conn.Navigate(ctx, attached, site); err != nil {
		conn.Close()
		return nil, fmt.Errorf("打开登录页失败：%v", err)
	}
	baseline, _ := conn.AllCookies(ctx, attached)
	return &LoginSession{accountID: accountID, conn: conn, session: attached, baselineCookies: baseline}, nil
}

// AccountID 返回会话对应账号。
func (s *LoginSession) AccountID() string { return s.accountID }

// Signals 读取当前页面信号，并与打开浏览器时的 Cookie 快照比较。
func (s *LoginSession) Signals(ctx context.Context) (LoginSignals, error) {
	value, err := s.conn.Evaluate(ctx, s.session, `(function(){
		var body = document.body ? document.body.innerText : '';
		body = body.slice(0, 2000);
		var password = !!document.querySelector('input[type="password"]');
		var account = !!document.querySelector('input[name*="phone"],input[name*="account"],input[name*="username"],input[type="tel"]');
		var qr = /扫码登录|二维码登录|微信扫码|验证码登录/.test(body);
		var formText = /登录/.test(body) && /(手机号|账号|密码|验证码|二维码)/.test(body);
		return {
			url: location.href,
			title: document.title,
			has_password_form: password,
			has_login_ui: password || account || qr || formText
		};
	})()`)
	if err != nil {
		return LoginSignals{}, err
	}
	signals := LoginSignals{}
	if fields, ok := value.(map[string]any); ok {
		if raw, ok := fields["url"].(string); ok {
			signals.URL = raw
		}
		if raw, ok := fields["title"].(string); ok {
			signals.Title = raw
		}
		if raw, ok := fields["has_password_form"].(bool); ok {
			signals.HasPasswordForm = raw
		}
		if raw, ok := fields["has_login_ui"].(bool); ok {
			signals.HasLoginUI = raw
		}
	}
	signals.PostLoginPage = isPostLoginURL(signals.URL)
	if current, err := s.conn.AllCookies(ctx, s.session); err == nil && s.baselineCookies != nil {
		signals.CookieChanged = cookiesChanged(s.baselineCookies, current)
	}
	return signals, nil
}

func cookiesChanged(baseline, current map[string]string) bool {
	for key, value := range current {
		if previous, ok := baseline[key]; !ok || previous != value {
			return true
		}
	}
	return false
}

// LoggedIn 读取信号并判断是否已登录。
func (s *LoginSession) LoggedIn(ctx context.Context) (LoginSignals, bool, error) {
	signals, err := s.Signals(ctx)
	if err != nil {
		return LoginSignals{}, false, err
	}
	return signals, signals.LoggedIn(), nil
}

// Close 关闭登录浏览器。
func (s *LoginSession) Close() {
	if s != nil && s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
}
