package batch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// YKTRunner 用无头 Chrome 打开账号的雨课堂页面，注入用户脚本，
// 等脚本面板出现后自动点「开始刷课」，并等待运行结束。
//
// 真实账号的课程目录 URL / 脚本内部状态无法在离线环境验证，
// 因此完成判定采用保守启发式：
//  1. 已观察到按钮进入「停止刷课」等运行态；
//  2. 之后按钮回到初始文案，或页面给出完成标记。
//
// 失败时返回错误，由队列记录；登录失效单独返回 NeedsLogin。
type YKTRunner struct {
	ScriptSource string
	ProfileDir   func(account Account) (string, error)
	Cookies      func(account Account) ([]CookieRecord, error)

	Headless     bool
	StartTimeout time.Duration
	RunTimeout   time.Duration
	PollInterval time.Duration
}

func (r *YKTRunner) profileDir(account Account) (string, error) {
	if r.ProfileDir == nil {
		return "", errors.New("缺少账号 profile 目录解析器")
	}
	return r.ProfileDir(account)
}

func (r *YKTRunner) pollInterval() time.Duration {
	if r.PollInterval <= 0 {
		return 500 * time.Millisecond
	}
	return r.PollInterval
}

func (r *YKTRunner) startTimeout() time.Duration {
	if r.StartTimeout <= 0 {
		return 90 * time.Second
	}
	return r.StartTimeout
}

func (r *YKTRunner) runTimeout() time.Duration {
	if r.RunTimeout <= 0 {
		return 4 * time.Hour
	}
	return r.RunTimeout
}

// Run 执行单个账号。
func (r *YKTRunner) Run(ctx context.Context, account Account, logf func(level, text string)) RunOutcome {
	log := func(level, text string) {
		if logf != nil {
			logf(level, text)
		}
	}
	profileDir, err := r.profileDir(account)
	if err != nil {
		return RunOutcome{Err: err}
	}
	entry := strings.TrimSpace(account.Site)
	if entry == "" {
		entry = "https://yuketang.cn/"
	}

	conn, err := startCDP(profileDir, !r.Headless)
	if err != nil {
		return RunOutcome{Err: err}
	}
	defer conn.Close()

	target, err := conn.CreateTarget(ctx, "about:blank")
	if err != nil {
		return RunOutcome{Err: fmt.Errorf("创建页面失败：%v", err)}
	}
	session, err := conn.Attach(ctx, target)
	if err != nil {
		return RunOutcome{Err: fmt.Errorf("附加页面失败：%v", err)}
	}
	if err := conn.EnablePage(ctx, session); err != nil {
		return RunOutcome{Err: fmt.Errorf("初始化页面失败：%v", err)}
	}
	// 把登录时保存的 Cookie 注入批量浏览器（Chrome 会话 Cookie 不会跨进程保留）。
	if r.Cookies != nil {
		if records, err := r.Cookies(account); err == nil && len(records) > 0 {
			if err := conn.SetCookies(ctx, session, records); err != nil {
				log("warning", "注入登录 Cookie 失败："+err.Error())
			} else {
				log("info", fmt.Sprintf("已恢复 %d 个登录 Cookie", len(records)))
			}
		}
	}

	// 先注入 shim 与用户脚本，再导航，确保脚本在 document-start 阶段生效。
	if err := conn.InjectOnNewDocument(ctx, session, gmShimSource()); err != nil {
		return RunOutcome{Err: fmt.Errorf("注入 GM shim 失败：%v", err)}
	}
	if strings.TrimSpace(r.ScriptSource) != "" {
		if err := conn.InjectOnNewDocument(ctx, session, r.ScriptSource); err != nil {
			return RunOutcome{Err: fmt.Errorf("注入用户脚本失败：%v", err)}
		}
	}
	log("info", "打开账号页面："+entry)
	if err := conn.Navigate(ctx, session, entry); err != nil {
		return RunOutcome{Err: fmt.Errorf("打开页面失败：%v", err)}
	}

	needsLogin, signals, err := readLoginSignals(ctx, conn, session)
	if err != nil {
		return RunOutcome{Err: fmt.Errorf("读取登录状态失败：%v", err)}
	}
	if needsLogin {
		return RunOutcome{NeedsLogin: true, Err: fmt.Errorf("登录态失效（当前页：%s）", signals.URL)}
	}

	// 等待脚本面板出现。
	log("info", "等待脚本面板…")
	if err := waitFor(ctx, r.startTimeout(), r.pollInterval(), func() (bool, error) {
		return conn.EvaluateBool(ctx, session, "!!document.querySelector('#btn-start')")
	}); err != nil {
		return RunOutcome{Err: fmt.Errorf("没有等到刷课面板，可能不在课程目录页：%v", err)}
	}
	startText, err := conn.EvaluateString(ctx, session, "((document.querySelector('#btn-start')||{}).textContent||'').trim()")
	if err != nil {
		return RunOutcome{Err: err}
	}
	if startText == "" {
		startText = "开始刷课"
	}
	log("info", "点击「"+startText+"」")
	if err := clickStart(ctx, conn, session); err != nil {
		return RunOutcome{Err: fmt.Errorf("点击开始刷课失败：%v", err)}
	}

	deadline := time.Now().Add(r.runTimeout())
	sawRunning := false
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return RunOutcome{Err: ctx.Err()}
		default:
		}
		state, err := readButtonState(ctx, conn, session)
		if err != nil {
			return RunOutcome{Err: fmt.Errorf("读取运行状态失败：%v", err)}
		}
		if state.Done || (sawRunning && state.Text == startText) {
			log("info", "账号任务结束")
			return RunOutcome{}
		}
		if state.Text != "" && state.Text != startText {
			if !sawRunning {
				log("info", "已开始："+state.Text)
			}
			sawRunning = true
		}
		select {
		case <-ctx.Done():
			return RunOutcome{Err: ctx.Err()}
		case <-time.After(r.pollInterval()):
		}
	}
	return RunOutcome{Err: fmt.Errorf("等待运行结束超时（%s）", r.runTimeout())}
}

type buttonState struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

func readButtonState(ctx context.Context, conn *cdpConn, session string) (buttonState, error) {
	value, err := conn.Evaluate(ctx, session,
		`(function(){var b=document.querySelector('#btn-start');return {text:b?String(b.textContent||'').trim():'', done:!!window.__yktBatchDone};})()`)
	if err != nil {
		return buttonState{}, err
	}
	state := buttonState{}
	if fields, ok := value.(map[string]any); ok {
		if text, ok := fields["text"].(string); ok {
			state.Text = text
		}
		if done, ok := fields["done"].(bool); ok {
			state.Done = done
		}
	}
	return state, nil
}

// readLoginSignals 判断是否停在登录页。
// 批量执行时账号的 profile 本来就是已登录状态，不能用「Cookie 是否变化」判断；
// 这里只检查登录 UI（密码框/手机号/扫码/登录表单），没有登录 UI 就继续等脚本面板，
// 面板出现本身就说明登录和课程页都正常。
func readLoginSignals(ctx context.Context, conn *cdpConn, session string) (bool, LoginSignals, error) {
	value, err := conn.Evaluate(ctx, session, `(function(){
		var body = document.body ? document.body.innerText : '';
		body = body.slice(0, 2000);
		var password = !!document.querySelector('input[type="password"]');
		var account = !!document.querySelector('input[name*="phone"],input[name*="account"],input[name*="username"],input[type="tel"]');
		var qr = /扫码登录|二维码登录|微信扫码|验证码登录/.test(body);
		var formText = /登录/.test(body) && /(手机号|账号|密码|验证码|二维码)/.test(body);
		return {url:location.href,title:document.title,has_password_form:password,has_login_ui:password||account||qr||formText};
	})()`)
	if err != nil {
		return false, LoginSignals{}, err
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
	needsLogin := isLoginURL(signals.URL) || signals.HasPasswordForm || signals.HasLoginUI
	return needsLogin, signals, nil
}

func clickStart(ctx context.Context, conn *cdpConn, session string) error {
	_, err := conn.Evaluate(ctx, session,
		`(function(){var b=document.querySelector('#btn-start');if(!b)return false;b.click();return true;})()`)
	return err
}

func waitFor(ctx context.Context, timeout, interval time.Duration, check func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ok, err := check()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
	return fmt.Errorf("等待超时（%s）", timeout)
}

// gmShimSource 补上用户脚本依赖的 GM_* / unsafeWindow 最小实现。
func gmShimSource() string {
	return `(function(){
  if (window.__yktBatchShim) return;
  window.__yktBatchShim = true;
  try { window.unsafeWindow = window; } catch (e) {}
  if (typeof window.GM_xmlhttpRequest === 'function') return;
  window.GM_xmlhttpRequest = function(details) {
    details = details || {};
    var opts = { method: details.method || 'GET', headers: details.headers || {} };
    if (details.data) opts.body = details.data;
    var aborted = false;
    fetch(details.url, opts).then(function(resp){
      return resp.text().then(function(text){
        return { status: resp.status, responseText: text, responseHeaders: '' };
      });
    }).then(function(res){
      if (aborted) return;
      if (details.onload) details.onload(res);
    }).catch(function(err){
      if (aborted) return;
      if (details.onerror) details.onerror(err);
    });
    return { abort: function(){ aborted = true; if (details.onabort) details.onabort(); } };
  };
})();`
}
