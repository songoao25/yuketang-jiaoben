package batch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// cdpConn 是 Chrome DevTools Protocol 的最小客户端：通过 --remote-debugging-pipe
// 的 fd3(写命令)/fd4(读响应) 收发 NUL 结尾的 JSON，不引入 WebSocket 依赖。
type cdpConn struct {
	cmd        *exec.Cmd
	toChrome   *os.File
	fromChrome *os.File
	writeMu    sync.Mutex

	nextID  int64
	mu      sync.Mutex
	pending map[int64]chan cdpResult

	readerDone chan struct{}
	readErr    error
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cdpMessage struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
}

type cdpResult struct {
	result json.RawMessage
	err    error
}

// startCDP 启动 Chrome 并返回可用的 CDP 连接；调用方必须 Close。
func startCDP(profileDir string, visible bool) (*cdpConn, error) {
	chromePath, err := ChromePath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建账号浏览器目录失败：%v", err)
	}

	toChromeR, toChromeW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	fromChromeR, fromChromeW, err := os.Pipe()
	if err != nil {
		_ = toChromeR.Close()
		_ = toChromeW.Close()
		return nil, err
	}

	cmd := chromeCommand(chromePath, profileDir, visible)
	cmd.ExtraFiles = []*os.File{toChromeR, fromChromeW} // 子进程 fd3 = 读命令，fd4 = 写响应
	cmd.Stdout = io.Discard
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = toChromeR.Close()
		_ = toChromeW.Close()
		_ = fromChromeR.Close()
		_ = fromChromeW.Close()
		return nil, fmt.Errorf("启动 Chrome 失败：%v", err)
	}
	// 父进程只保留写命令端与读响应端。
	_ = toChromeR.Close()
	_ = fromChromeW.Close()

	conn := &cdpConn{
		cmd:        cmd,
		toChrome:   toChromeW,
		fromChrome: fromChromeR,
		pending:    map[int64]chan cdpResult{},
		readerDone: make(chan struct{}),
	}
	go conn.readLoop()
	return conn, nil
}

func (c *cdpConn) readLoop() {
	defer close(c.readerDone)
	reader := bufio.NewReaderSize(c.fromChrome, 1<<20)
	for {
		raw, err := reader.ReadBytes(0)
		if err != nil {
			c.mu.Lock()
			c.readErr = err
			for id, ch := range c.pending {
				delete(c.pending, id)
				ch <- cdpResult{err: fmt.Errorf("CDP 连接已断开：%v", err)}
			}
			c.mu.Unlock()
			return
		}
		payload := bytes.TrimSpace(bytes.TrimSuffix(raw, []byte{0}))
		if len(payload) == 0 {
			continue
		}
		var msg cdpMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			continue
		}
		if msg.ID == 0 {
			continue // 事件：P0 暂不消费，后续按需扩展
		}
		c.mu.Lock()
		ch := c.pending[msg.ID]
		delete(c.pending, msg.ID)
		c.mu.Unlock()
		if ch == nil {
			continue
		}
		if msg.Error != nil {
			ch <- cdpResult{err: fmt.Errorf("CDP %d：%s", msg.Error.Code, msg.Error.Message)}
		} else {
			ch <- cdpResult{result: msg.Result}
		}
	}
}

func (c *cdpConn) call(ctx context.Context, method string, params any, sessionID string) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	ch := make(chan cdpResult, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	payload := map[string]any{"id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	if sessionID != "" {
		payload["sessionId"] = sessionID
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, 0)

	c.writeMu.Lock()
	_, err = c.toChrome.Write(encoded)
	c.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("写入 CDP 失败：%v", err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.readerDone:
		return nil, fmt.Errorf("CDP 连接已断开")
	case result := <-ch:
		return result.result, result.err
	}
}

// CreateTarget 新建标签页并返回 targetId。
func (c *cdpConn) CreateTarget(ctx context.Context, url string) (string, error) {
	raw, err := c.call(ctx, "Target.createTarget", map[string]any{"url": url}, "")
	if err != nil {
		return "", err
	}
	var out struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.TargetID == "" {
		return "", fmt.Errorf("CDP 未返回 targetId")
	}
	return out.TargetID, nil
}

// Attach 附加到 target（flatten=true），返回 sessionId。
func (c *cdpConn) Attach(ctx context.Context, targetID string) (string, error) {
	raw, err := c.call(ctx, "Target.attachToTarget", map[string]any{
		"targetId": targetID, "flatten": true,
	}, "")
	if err != nil {
		return "", err
	}
	var out struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.SessionID == "" {
		return "", fmt.Errorf("CDP 未返回 sessionId")
	}
	return out.SessionID, nil
}

// EnablePage 打开 Page/Runtime 域名。
func (c *cdpConn) EnablePage(ctx context.Context, sessionID string) error {
	if _, err := c.call(ctx, "Page.enable", nil, sessionID); err != nil {
		return err
	}
	_, err := c.call(ctx, "Runtime.enable", nil, sessionID)
	return err
}

// Navigate 打开网址，并等待 document.readyState == complete。
func (c *cdpConn) Navigate(ctx context.Context, sessionID, url string) error {
	if _, err := c.call(ctx, "Page.navigate", map[string]any{"url": url}, sessionID); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		state, err := c.EvaluateString(ctx, sessionID, "document.readyState")
		if err == nil && (state == "complete" || state == "interactive") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(120 * time.Millisecond):
		}
	}
	return fmt.Errorf("等待页面加载超时：%s", url)
}

// Evaluate 执行 JS 表达式并返回 JSON 解码后的值（string/bool/float64/nil/map/slice）。
func (c *cdpConn) Evaluate(ctx context.Context, sessionID, expression string) (any, error) {
	raw, err := c.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  true,
	}, sessionID)
	if err != nil {
		return nil, err
	}
	var out struct {
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out.ExceptionDetails != nil {
		return nil, fmt.Errorf("页面脚本异常：%s", out.ExceptionDetails.Text)
	}
	if len(out.Result.Value) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(out.Result.Value, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// EvaluateString 执行 JS 表达式并转成字符串（数字/布尔也做可读转换）。
func (c *cdpConn) EvaluateString(ctx context.Context, sessionID, expression string) (string, error) {
	value, err := c.Evaluate(ctx, sessionID, expression)
	if err != nil {
		return "", err
	}
	switch v := value.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	default:
		encoded, marshalErr := json.Marshal(v)
		if marshalErr != nil {
			return fmt.Sprint(v), nil
		}
		return string(encoded), nil
	}
}

// EvaluateBool 执行 JS 表达式并返回布尔值（缺失/异常按 false 处理）。
func (c *cdpConn) EvaluateBool(ctx context.Context, sessionID, expression string) (bool, error) {
	value, err := c.Evaluate(ctx, sessionID, expression)
	if err != nil {
		return false, err
	}
	flag, _ := value.(bool)
	return flag, nil
}

// InjectOnNewDocument 在后续每个文档创建前注入脚本（用于用户脚本/登录检测）。
func (c *cdpConn) InjectOnNewDocument(ctx context.Context, sessionID, source string) error {
	_, err := c.call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": source}, sessionID)
	return err
}

// Close 关闭 CDP 连接并结束 Chrome 进程。
func (c *cdpConn) Close() {
	c.writeMu.Lock()
	if c.toChrome != nil {
		_ = c.toChrome.Close()
		c.toChrome = nil
	}
	c.writeMu.Unlock()
	if c.fromChrome != nil {
		_ = c.fromChrome.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
}

// AllCookies 返回当前浏览器的全部 Cookie（name@domain@path -> value），
// 用于判断登录过程中是否真的产生了新的登录态，而不是停在主页/扫码页。
func (c *cdpConn) AllCookies(ctx context.Context, sessionID string) (map[string]string, error) {
	if _, err := c.call(ctx, "Network.enable", nil, sessionID); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, "Network.getAllCookies", nil, sessionID)
	if err != nil {
		return nil, err
	}
	var out struct {
		Cookies []struct {
			Name     string `json:"name"`
			Value    string `json:"value"`
			Domain   string `json:"domain"`
			Path     string `json:"path"`
			HTTPOnly bool   `json:"httpOnly"`
		} `json:"cookies"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	cookies := make(map[string]string, len(out.Cookies))
	for _, cookie := range out.Cookies {
		if cookie.Name == "" {
			continue
		}
		cookies[cookie.Name+"@"+cookie.Domain+cookie.Path] = cookie.Value
	}
	return cookies, nil
}

// CookieRecord 是可持久化的完整 Cookie（用于把登录态从登录浏览器带到批量浏览器）。
type CookieRecord struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Secure   bool    `json:"secure"`
	HTTPOnly bool    `json:"httpOnly"`
	Expires  float64 `json:"expires,omitempty"`
	SameSite string  `json:"sameSite,omitempty"`
}

// AllCookieRecords 返回完整 Cookie 记录（含 HttpOnly / 过期时间）。
func (c *cdpConn) AllCookieRecords(ctx context.Context, sessionID string) ([]CookieRecord, error) {
	if _, err := c.call(ctx, "Network.enable", nil, sessionID); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, "Network.getAllCookies", nil, sessionID)
	if err != nil {
		return nil, err
	}
	var out struct {
		Cookies []CookieRecord `json:"cookies"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	records := make([]CookieRecord, 0, len(out.Cookies))
	for _, cookie := range out.Cookies {
		if cookie.Name == "" {
			continue
		}
		records = append(records, cookie)
	}
	return records, nil
}

// SetCookies 把保存的登录 Cookie 注入当前浏览器。
func (c *cdpConn) SetCookies(ctx context.Context, sessionID string, records []CookieRecord) error {
	if len(records) == 0 {
		return nil
	}
	if _, err := c.call(ctx, "Network.enable", nil, sessionID); err != nil {
		return err
	}
	for _, record := range records {
		if strings.TrimSpace(record.Domain) == "" {
			continue
		}
		params := map[string]any{
			"name":     record.Name,
			"value":    record.Value,
			"domain":   record.Domain,
			"path":     defaultString(record.Path, "/"),
			"secure":   record.Secure,
			"httpOnly": record.HTTPOnly,
		}
		if record.Expires > 0 {
			params["expires"] = record.Expires
		}
		if record.SameSite != "" {
			params["sameSite"] = record.SameSite
		}
		if _, err := c.call(ctx, "Network.setCookie", params, sessionID); err != nil {
			return err
		}
	}
	return nil
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// Shutdown 请求 Chrome 优雅退出并等待 Cookie/Profile 落盘，避免直接 kill 丢失会话 Cookie。
func (c *cdpConn) Shutdown(ctx context.Context) {
	if c == nil {
		return
	}
	_, _ = c.call(ctx, "Browser.close", nil, "")
	done := make(chan struct{})
	go func() {
		if c.cmd != nil && c.cmd.Process != nil {
			_, _ = c.cmd.Process.Wait()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	c.writeMu.Lock()
	if c.toChrome != nil {
		_ = c.toChrome.Close()
		c.toChrome = nil
	}
	c.writeMu.Unlock()
	if c.fromChrome != nil {
		_ = c.fromChrome.Close()
		c.fromChrome = nil
	}
}
