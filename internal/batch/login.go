package batch

import (
	"context"
	"fmt"
	"strings"
)

// LoginSignals 是从登录页面读到的登录判断信号。
type LoginSignals struct {
	URL             string `json:"url"`
	Title           string `json:"title"`
	HasPasswordForm bool   `json:"has_password_form"`
}

// LoggedIn 用「不在登录页 + 没有密码输入框」做保守判断；
// 真正的完成动作仍由用户点「我已完成登录」，避免误判。
func (s LoginSignals) LoggedIn() bool {
	url := strings.ToLower(strings.TrimSpace(s.URL))
	if url == "" {
		return false
	}
	for _, marker := range []string{"login", "passport", "sso", "signin", "auth"} {
		if strings.Contains(url, marker) {
			return false
		}
	}
	return !s.HasPasswordForm
}

// LoginSession 是一次可见浏览器登录会话：打开登录页、读信号、成功后关闭。
type LoginSession struct {
	accountID string
	conn      *cdpConn
	session   string
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
	return &LoginSession{accountID: accountID, conn: conn, session: attached}, nil
}

// AccountID 返回会话对应账号。
func (s *LoginSession) AccountID() string { return s.accountID }

// Signals 读取当前页面信号。
func (s *LoginSession) Signals(ctx context.Context) (LoginSignals, error) {
	value, err := s.conn.Evaluate(ctx, s.session,
		`(function(){return {url: location.href, title: document.title, has_password_form: !!document.querySelector('input[type="password"]')};})()`)
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
	}
	return signals, nil
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
