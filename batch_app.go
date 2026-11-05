//go:build !cli

package main

// batch_app.go 把多账号批量核心（internal/batch）绑定到 Wails 前端。
// P1 范围：账号库 CRUD + 首次可见浏览器登录 + 登录态检测；队列/运行在 P2 接入。

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
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
	a.batchNotified = map[string]bool{}
	a.batchMu.Unlock()

	// 崩溃恢复：上次 running/stopping 的队列标记为中断，避免误以为还在跑。
	persisted, err := batch.LoadQueueState(store.QueueStatePath())
	if err != nil {
		a.emit("读取上次批量状态失败：" + err.Error())
		return
	}
	recovered := batch.RecoverQueueState(persisted)
	if recovered.State == "interrupted" {
		_ = batch.SaveQueueState(store.QueueStatePath(), recovered)
		a.batchMu.Lock()
		a.batchLastState = recovered
		a.batchMu.Unlock()
		if a.ctx != nil {
			wruntime.EventsEmit(a.ctx, "batch:state", recovered)
		}
	}
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

// ensureBatchQueue 懒初始化批量队列（执行器为无头 Chrome + 内置用户脚本）。
func (a *App) ensureBatchQueue() (*batch.Queue, error) {
	a.batchMu.Lock()
	if a.batchQueue != nil {
		defer a.batchMu.Unlock()
		return a.batchQueue, nil
	}
	store := a.batchStore
	scriptSource := a.scriptSource
	a.batchMu.Unlock()
	if store == nil {
		return nil, errors.New("多账号功能未初始化，请重启应用")
	}

	runner := &batch.YKTRunner{
		ScriptSource: scriptSource,
		ProfileDir: func(account batch.Account) (string, error) {
			return store.ProfileDir(account.ID)
		},
		Headless:     true,
		StartTimeout: 90 * time.Second,
		RunTimeout:   4 * time.Hour,
		PollInterval: 500 * time.Millisecond,
	}
	queue := batch.NewQueue(runner, func(state batch.QueueState) {
		a.recordBatchQueueState(state)
		if a.ctx != nil {
			wruntime.EventsEmit(a.ctx, "batch:state", state)
		}
	}, func(accountID, level, text string) {
		if a.ctx != nil {
			wruntime.EventsEmit(a.ctx, "batch:log", map[string]string{
				"account_id": accountID, "level": level, "text": text,
			})
		}
	})
	// 账号之间随机停顿 30–90 秒，避免同一时间密集请求。
	queue.SetPacing(func(completed int) time.Duration {
		return time.Duration(30+rand.Intn(61)) * time.Second
	})

	a.batchMu.Lock()
	a.batchQueue = queue
	a.batchMu.Unlock()
	return queue, nil
}

// recordBatchQueueState 把队列终态写回账号库、落盘并发送系统通知。
func (a *App) recordBatchQueueState(state batch.QueueState) {
	a.batchMu.Lock()
	store := a.batchStore
	a.batchLastState = state
	a.batchMu.Unlock()
	if store == nil {
		return
	}
	_ = batch.SaveQueueState(store.QueueStatePath(), state)

	for _, item := range state.Items {
		var result string
		switch item.Status {
		case batch.RunOK:
			result = "ok"
		case batch.RunFailed:
			result = "failed"
		case batch.RunNeedsLogin:
			result = "needs_login"
		case batch.RunCancelled:
			result = "cancelled"
		default:
			continue
		}
		_ = store.SetLastRun(item.AccountID, result, item.Message)
		a.notifyBatchItem(item, result)
	}
	if state.State == "done" {
		a.notifyBatchDone(state)
	}
}

// notifyBatchItem 每个账号终态只通知一次。
func (a *App) notifyBatchItem(item batch.QueueItem, result string) {
	key := item.AccountID + ":" + string(item.Status) + ":" + item.EndedAt
	a.batchMu.Lock()
	if a.batchNotified == nil {
		a.batchNotified = map[string]bool{}
	}
	if a.batchNotified[key] {
		a.batchMu.Unlock()
		return
	}
	a.batchNotified[key] = true
	a.batchMu.Unlock()

	switch result {
	case "needs_login":
		notifyUser("需要重新登录", "账号「"+item.Name+"」登录态失效，请打开雨课助手重新登录")
	case "failed":
		notifyUser("账号运行失败", "「"+item.Name+"」："+item.Message)
	case "cancelled":
		notifyUser("账号已取消", "「"+item.Name+"」未完成")
	}
}

// notifyBatchDone 整批结束时只通知一次。
func (a *App) notifyBatchDone(state batch.QueueState) {
	key := "done:" + state.StartedAt
	a.batchMu.Lock()
	if a.batchNotified == nil {
		a.batchNotified = map[string]bool{}
	}
	if a.batchNotified[key] {
		a.batchMu.Unlock()
		return
	}
	a.batchNotified[key] = true
	a.batchMu.Unlock()

	okCount, failCount, needCount := 0, 0, 0
	for _, item := range state.Items {
		switch item.Status {
		case batch.RunOK:
			okCount++
		case batch.RunFailed:
			failCount++
		case batch.RunNeedsLogin:
			needCount++
		}
	}
	message := fmt.Sprintf("成功 %d，失败 %d，需登录 %d", okCount, failCount, needCount)
	notifyUser("批量运行结束", message)
}

// StartBatch 开始顺序批量运行（只运行已登录账号；未登录会提示先登录）。
func (a *App) StartBatch(ids []string) (batch.QueueState, error) {
	store, err := a.batchStoreOrErr()
	if err != nil {
		return batch.QueueState{}, err
	}
	queue, err := a.ensureBatchQueue()
	if err != nil {
		return batch.QueueState{}, err
	}
	accounts := make([]batch.Account, 0, len(ids))
	skipped := make([]string, 0)
	for _, id := range ids {
		account, ok := store.Get(id)
		if !ok {
			continue
		}
		if account.LoginState != batch.LoginOK {
			skipped = append(skipped, account.Name)
			_ = store.SetLastRun(id, "needs_login", "还没有完成登录")
			continue
		}
		accounts = append(accounts, account)
	}
	if len(accounts) == 0 {
		if len(skipped) > 0 {
			return batch.QueueState{}, fmt.Errorf("没有已登录的账号：%s", strings.Join(skipped, "、"))
		}
		return batch.QueueState{}, errors.New("没有选中可运行的账号")
	}
	a.batchMu.Lock()
	a.batchNotified = map[string]bool{}
	a.batchLastState = batch.QueueState{State: "idle"}
	a.batchMu.Unlock()
	if err := queue.Start(accounts); err != nil {
		return batch.QueueState{}, err
	}
	a.emitBatchChanged()
	return queue.Snapshot(), nil
}

// StopBatch 请求停止当前批量任务。
func (a *App) StopBatch() batch.QueueState {
	queue, err := a.ensureBatchQueue()
	if err != nil {
		return batch.QueueState{State: "idle"}
	}
	queue.Stop()
	return queue.Snapshot()
}

// GetBatchState 返回批量队列快照；没有活动队列时回退到上次落盘状态。
func (a *App) GetBatchState() batch.QueueState {
	queue, err := a.ensureBatchQueue()
	if err != nil {
		return batch.QueueState{State: "idle"}
	}
	snapshot := queue.Snapshot()
	if snapshot.State != "idle" {
		return snapshot
	}
	a.batchMu.Lock()
	defer a.batchMu.Unlock()
	if a.batchLastState.State != "" {
		return a.batchLastState
	}
	return batch.QueueState{State: "idle"}
}
