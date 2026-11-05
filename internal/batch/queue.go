package batch

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// RunStatus 是单个账号在一次批量队列中的状态。
type RunStatus string

const (
	RunQueued     RunStatus = "queued"
	RunRunning    RunStatus = "running"
	RunOK         RunStatus = "ok"
	RunFailed     RunStatus = "failed"
	RunNeedsLogin RunStatus = "needs_login"
	RunSkipped    RunStatus = "skipped"
	RunCancelled  RunStatus = "cancelled"
)

// QueueItem 是队列中的一个账号任务。
type QueueItem struct {
	AccountID string    `json:"account_id"`
	Name      string    `json:"name"`
	Status    RunStatus `json:"status"`
	Message   string    `json:"message,omitempty"`
	StartedAt string    `json:"started_at,omitempty"`
	EndedAt   string    `json:"ended_at,omitempty"`
}

// QueueState 是队列快照，供前端渲染。
type QueueState struct {
	State     string      `json:"state"` // idle | running | stopping | done
	Items     []QueueItem `json:"items"`
	Current   string      `json:"current,omitempty"`
	StartedAt string      `json:"started_at,omitempty"`
	UpdatedAt string      `json:"updated_at,omitempty"`
	Message   string      `json:"message,omitempty"`
}

// RunOutcome 是执行器对一个账号的结论。
type RunOutcome struct {
	NeedsLogin bool
	Err        error
}

// Runner 是账号执行器：雨课是无头浏览器，超星是 Python 引擎。
type Runner interface {
	Run(ctx context.Context, account Account, logf func(level, text string)) RunOutcome
}

// Queue 顺序执行账号任务，默认并发 1；账号之间可配置停顿。
type Queue struct {
	mu       sync.Mutex
	runner   Runner
	onEvent  func(QueueState)
	onLog    func(accountID, level, text string)
	state    QueueState
	cancel   context.CancelFunc
	pacing   func(completed int) time.Duration
	stopping bool
}

// NewQueue 创建队列；onEvent 可为 nil。
func NewQueue(runner Runner, onEvent func(QueueState), onLog func(accountID, level, text string)) *Queue {
	queue := &Queue{runner: runner, onEvent: onEvent, onLog: onLog, state: QueueState{State: "idle"}}
	queue.pacing = func(completed int) time.Duration { return 0 }
	return queue
}

// SetPacing 设置账号之间的停顿（测试可设为 0）。
func (q *Queue) SetPacing(pacing func(completed int) time.Duration) {
	if pacing != nil {
		q.pacing = pacing
	}
}

// Snapshot 返回队列快照副本。
func (q *Queue) Snapshot() QueueState {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := make([]QueueItem, len(q.state.Items))
	copy(items, q.state.Items)
	return QueueState{
		State: q.state.State, Items: items, Current: q.state.Current,
		StartedAt: q.state.StartedAt, UpdatedAt: q.state.UpdatedAt, Message: q.state.Message,
	}
}

// Running 返回是否有批量任务在执行。
func (q *Queue) Running() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.state.State == "running" || q.state.State == "stopping"
}

// Start 异步开始批量；已有任务运行时返回错误。
func (q *Queue) Start(accounts []Account) error {
	if q.runner == nil {
		return fmt.Errorf("批量执行器未初始化")
	}
	if len(accounts) == 0 {
		return fmt.Errorf("没有可运行的账号")
	}
	q.mu.Lock()
	if q.state.State == "running" || q.state.State == "stopping" {
		q.mu.Unlock()
		return fmt.Errorf("已有批量任务在运行")
	}
	items := make([]QueueItem, 0, len(accounts))
	for _, account := range accounts {
		items = append(items, QueueItem{AccountID: account.ID, Name: account.Name, Status: RunQueued})
	}
	ctx, cancel := context.WithCancel(context.Background())
	q.cancel = cancel
	q.stopping = false
	q.state = QueueState{
		State: "running", Items: items,
		StartedAt: time.Now().Format(time.RFC3339), UpdatedAt: time.Now().Format(time.RFC3339),
	}
	snapshot := q.snapshotLocked()
	q.mu.Unlock()
	q.emit(snapshot)

	go q.run(ctx, accounts)
	return nil
}

// Stop 请求停止：当前账号结束后不再开始下一个。
func (q *Queue) Stop() {
	q.mu.Lock()
	if q.state.State != "running" && q.state.State != "stopping" {
		q.mu.Unlock()
		return
	}
	q.stopping = true
	q.state.State = "stopping"
	q.state.Message = "正在停止…"
	cancel := q.cancel
	snapshot := q.snapshotLocked()
	q.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	q.emit(snapshot)
}

func (q *Queue) snapshotLocked() QueueState {
	items := make([]QueueItem, len(q.state.Items))
	copy(items, q.state.Items)
	return QueueState{
		State: q.state.State, Items: items, Current: q.state.Current,
		StartedAt: q.state.StartedAt, UpdatedAt: q.state.UpdatedAt, Message: q.state.Message,
	}
}

func (q *Queue) emit(snapshot QueueState) {
	if q.onEvent != nil {
		q.onEvent(snapshot)
	}
}

func (q *Queue) updateItem(id string, mutate func(*QueueItem)) QueueState {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.state.Items {
		if q.state.Items[i].AccountID == id {
			mutate(&q.state.Items[i])
			break
		}
	}
	q.state.UpdatedAt = time.Now().Format(time.RFC3339)
	return q.snapshotLocked()
}

func (q *Queue) log(accountID, level, message string) {
	if q.onLog != nil {
		q.onLog(accountID, level, message)
	}
}

func (q *Queue) run(ctx context.Context, accounts []Account) {
	completed := 0
	var snapshot QueueState
	for index, account := range accounts {
		if ctx.Err() != nil {
			q.markRemaining(accounts[index:], RunCancelled, "已停止")
			break
		}
		q.mu.Lock()
		q.state.Current = account.ID
		q.state.Message = ""
		q.mu.Unlock()

		started := time.Now().Format(time.RFC3339)
		snapshot = q.updateItem(account.ID, func(item *QueueItem) {
			item.Status = RunRunning
			item.StartedAt = started
			item.Message = "运行中"
		})
		q.emit(snapshot)
		q.log(account.ID, "info", "开始处理账号："+account.Name)

		outcome := q.runner.Run(ctx, account, func(level, text string) {
			q.log(account.ID, level, text)
		})

		ended := time.Now().Format(time.RFC3339)
		var status RunStatus
		message := ""
		switch {
		case outcome.NeedsLogin:
			status = RunNeedsLogin
			message = "登录态失效，需要重新登录"
		case outcome.Err != nil:
			status = RunFailed
			message = outcome.Err.Error()
		default:
			status = RunOK
			message = "完成"
		}
		if ctx.Err() != nil && status == RunFailed {
			status = RunCancelled
			message = "已停止"
		}
		completed++
		snapshot = q.updateItem(account.ID, func(item *QueueItem) {
			item.Status = status
			item.EndedAt = ended
			item.Message = message
		})
		q.stateMessage(status, message)
		q.emit(snapshot)
		q.log(account.ID, "info", "账号结束："+account.Name+" · "+string(status)+" · "+message)

		if ctx.Err() != nil || q.isStopping() {
			q.markRemaining(accounts[index+1:], RunCancelled, "已停止")
			break
		}
		if index < len(accounts)-1 {
			delay := q.pacing(completed)
			if delay > 0 {
				q.log("", "info", fmt.Sprintf("等待 %s 后处理下一个账号", delay.Round(time.Second)))
				select {
				case <-ctx.Done():
					q.markRemaining(accounts[index+1:], RunCancelled, "已停止")
					return
				case <-time.After(delay):
				}
			}
		}
	}

	q.mu.Lock()
	q.state.Current = ""
	q.state.State = "done"
	if q.state.Message == "正在停止…" {
		q.state.Message = "已停止"
	}
	q.state.UpdatedAt = time.Now().Format(time.RFC3339)
	snapshot = q.snapshotLocked()
	q.mu.Unlock()
	q.emit(snapshot)
}

func (q *Queue) stateMessage(status RunStatus, message string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if status == RunFailed || status == RunNeedsLogin {
		q.state.Message = message
	}
}

func (q *Queue) isStopping() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.stopping
}

func (q *Queue) markRemaining(accounts []Account, status RunStatus, message string) {
	for _, account := range accounts {
		snapshot := q.updateItem(account.ID, func(item *QueueItem) {
			if item.Status == RunQueued || item.Status == RunRunning {
				item.Status = status
				item.Message = message
				item.EndedAt = time.Now().Format(time.RFC3339)
			}
		})
		q.emit(snapshot)
	}
}

// markRemaining 也接受切片形式（Stop 时当前账号之后再调）。
func (q *Queue) markRemainingFrom(index int) {
	q.mu.Lock()
	rest := make([]Account, 0)
	for _, item := range q.state.Items[index:] {
		rest = append(rest, Account{ID: item.AccountID, Name: item.Name})
	}
	q.mu.Unlock()
	q.markRemaining(rest, RunCancelled, "已停止")
}
