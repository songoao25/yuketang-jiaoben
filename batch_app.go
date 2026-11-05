//go:build !cli

package main

// batch_app.go 把多账号批量核心（internal/batch）绑定到 Wails 前端。
// P1 范围：账号库 CRUD + 首次可见浏览器登录 + 登录态检测；队列/运行在 P2 接入。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/songoao25/yuketang-jiaoben/internal/batch"
)

// BatchLoginStatus 是登录向导轮询用的状态。
type BatchLoginStatus struct {
	Open     bool   `json:"open"`
	URL      string `json:"url"`
	LoggedIn bool   `json:"logged_in"`
	Error    string `json:"error,omitempty"`
}

// initBatch 在 App 启动时初始化账号库（失败只提示，不阻塞主界面）。
func (a *App) initBatch() {
	store, err := batch.NewStore("")
	if err != nil {
		a.emit("多账号功能初始化失败：" + err.Error())
		return
	}
	a.batchMu.Lock()
	a.batchStore = store
	a.batchMu.Unlock()
}

func (a *App) batchStoreOrErr() (*batch.Store, error) {
	a.batchMu.Lock()
	defer a.batchMu.Unlock()
	if a.batchStore == nil {
		return nil, errors.New("多账号功能未初始化，请重启应用")
	}
	return a.batchStore, nil
}

func (a *App) emitBatchChanged() {
	if a.ctx == nil {
		return
	}
	wruntime.EventsEmit(a.ctx, "batch:changed")
}

// ListBatchAccounts 返回账号列表（不含密码）。
func (a *App) ListBatchAccounts() ([]batch.Account, error) {
	store, err := a.batchStoreOrErr()
	if err != nil {
		return nil, err
	}
	return store.List(), nil
}

// AddBatchAccount 新增账号（只存备注，不存密码）。
func (a *App) AddBatchAccount(name string) (batch.Account, error) {
	store, err := a.batchStoreOrErr()
	if err != nil {
		return batch.Account{}, err
	}
	account, err := store.Add(name, "")
	if err != nil {
		return batch.Account{}, err
	}
	a.emitBatchChanged()
	return account, nil
}

// RemoveBatchAccount 删除账号并清空浏览器登录态。
func (a *App) RemoveBatchAccount(id string) error {
	store, err := a.batchStoreOrErr()
	if err != nil {
		return err
	}
	a.batchMu.Lock()
	if a.batchLogin != nil && a.batchLoginAccount == id {
		a.batchLogin.Close()
		a.batchLogin = nil
		a.batchLoginAccount = ""
	}
	a.batchMu.Unlock()
	if err := store.Remove(id); err != nil {
		return err
	}
	a.emitBatchChanged()
	return nil
}

// OpenBatchLogin 打开可见浏览器到登录页；用户手动完成登录（扫码/密码/SSO 均可）。
func (a *App) OpenBatchLogin(id string) error {
	store, err := a.batchStoreOrErr()
	if err != nil {
		return err
	}
	account, ok := store.Get(id)
	if !ok {
		return fmt.Errorf("账号不存在：%s", id)
	}
	profileDir, err := store.ProfileDir(id)
	if err != nil {
		return err
	}

	a.batchMu.Lock()
	if a.batchLogin != nil {
		a.batchLogin.Close()
		a.batchLogin = nil
		a.batchLoginAccount = ""
	}
	a.batchMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	session, err := batch.StartLoginSession(ctx, id, profileDir, account.Site)
	if err != nil {
		return err
	}
	a.batchMu.Lock()
	a.batchLogin = session
	a.batchLoginAccount = id
	a.batchMu.Unlock()
	a.emitBatchChanged()
	return nil
}

// GetBatchLoginState 读取当前登录会话的状态（前端轮询用）。
func (a *App) GetBatchLoginState(id string) (BatchLoginStatus, error) {
	a.batchMu.Lock()
	session := a.batchLogin
	accountID := a.batchLoginAccount
	a.batchMu.Unlock()
	if session == nil || accountID != id {
		return BatchLoginStatus{Open: false}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	signals, logged, err := session.LoggedIn(ctx)
	status := BatchLoginStatus{Open: true, LoggedIn: logged, URL: signals.URL}
	if err != nil {
		status.Error = err.Error()
	}
	return status, nil
}

// CompleteBatchLogin 确认登录完成：校验登录态，写入本地账号库并关闭登录浏览器。
func (a *App) CompleteBatchLogin(id string) error {
	a.batchMu.Lock()
	session := a.batchLogin
	accountID := a.batchLoginAccount
	a.batchMu.Unlock()
	if session == nil || accountID != id {
		return errors.New("没有正在进行的登录，请先点「登录」")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	signals, logged, err := session.LoggedIn(ctx)
	if err != nil {
		return fmt.Errorf("读取登录状态失败：%v", err)
	}
	if !logged {
		return fmt.Errorf("还没有检测到登录成功（当前页：%s），请先在浏览器里完成登录", displayURL(signals.URL))
	}
	store, err := a.batchStoreOrErr()
	if err != nil {
		return err
	}
	if err := store.SetLoginState(id, batch.LoginOK, ""); err != nil {
		return err
	}
	a.batchMu.Lock()
	session.Close()
	a.batchLogin = nil
	a.batchLoginAccount = ""
	a.batchMu.Unlock()
	a.emitBatchChanged()
	return nil
}

// CancelBatchLogin 取消本次登录并关闭浏览器（保留 profile 供下次重试）。
func (a *App) CancelBatchLogin(id string) error {
	a.batchMu.Lock()
	if a.batchLogin != nil && (id == "" || a.batchLoginAccount == id) {
		a.batchLogin.Close()
		a.batchLogin = nil
		a.batchLoginAccount = ""
	}
	a.batchMu.Unlock()
	a.emitBatchChanged()
	return nil
}

func displayURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "未知页面"
	}
	return raw
}

// closeBatchLogin 在 App 退出时清理登录浏览器。
func (a *App) closeBatchLogin() {
	a.batchMu.Lock()
	defer a.batchMu.Unlock()
	if a.batchLogin != nil {
		a.batchLogin.Close()
		a.batchLogin = nil
		a.batchLoginAccount = ""
	}
}
