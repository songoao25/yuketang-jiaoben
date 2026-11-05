package batch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type fakeRunner struct {
	mu       sync.Mutex
	order    []string
	outcomes map[string]RunOutcome
	delay    time.Duration
	blockOn  chan struct{}
}

func (f *fakeRunner) Run(ctx context.Context, account Account, logf func(level, text string)) RunOutcome {
	f.mu.Lock()
	f.order = append(f.order, account.ID)
	outcome := f.outcomes[account.ID]
	f.mu.Unlock()
	if logf != nil {
		logf("info", "fake run "+account.ID)
	}
	if f.blockOn != nil {
		select {
		case <-f.blockOn:
		case <-ctx.Done():
			return RunOutcome{Err: ctx.Err()}
		}
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return RunOutcome{Err: ctx.Err()}
		}
	}
	return outcome
}

func waitQueueState(t *testing.T, queue *Queue, want string, timeout time.Duration) QueueState {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snapshot := queue.Snapshot()
		if snapshot.State == want {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待队列状态 %q 超时，当前：%+v", want, queue.Snapshot())
	return QueueState{}
}

func TestQueueSequentialAndStatuses(t *testing.T) {
	runner := &fakeRunner{outcomes: map[string]RunOutcome{
		"a1": {},
		"a2": {NeedsLogin: true},
		"a3": {Err: context.DeadlineExceeded},
	}}
	events := 0
	queue := NewQueue(runner, func(QueueState) { events++ }, nil)
	accounts := []Account{{ID: "a1", Name: "一号"}, {ID: "a2", Name: "二号"}, {ID: "a3", Name: "三号"}}
	if err := queue.Start(accounts); err != nil {
		t.Fatalf("开始队列失败：%v", err)
	}
	snapshot := waitQueueState(t, queue, "done", 5*time.Second)
	if len(runner.order) != 3 || runner.order[0] != "a1" || runner.order[2] != "a3" {
		t.Fatalf("执行顺序不对：%v", runner.order)
	}
	want := map[string]RunStatus{"a1": RunOK, "a2": RunNeedsLogin, "a3": RunFailed}
	for _, item := range snapshot.Items {
		if item.Status != want[item.AccountID] {
			t.Errorf("%s 状态 = %s，期望 %s", item.AccountID, item.Status, want[item.AccountID])
		}
	}
	if events == 0 {
		t.Fatal("应发出队列事件")
	}
	if queue.Running() {
		t.Fatal("结束后不应处于运行中")
	}
}

func TestQueueStopMarksRemaining(t *testing.T) {
	runner := &fakeRunner{outcomes: map[string]RunOutcome{}, blockOn: make(chan struct{})}
	queue := NewQueue(runner, nil, nil)
	accounts := []Account{{ID: "b1", Name: "一号"}, {ID: "b2", Name: "二号"}}
	if err := queue.Start(accounts); err != nil {
		t.Fatalf("开始失败：%v", err)
	}
	time.Sleep(30 * time.Millisecond)
	queue.Stop()
	close(runner.blockOn)
	snapshot := waitQueueState(t, queue, "done", 5*time.Second)
	for _, item := range snapshot.Items {
		if item.Status != RunCancelled && item.Status != RunFailed {
			t.Fatalf("停止后状态异常：%+v", snapshot)
		}
	}
}

func TestQueueRejectsDoubleStartAndEmpty(t *testing.T) {
	runner := &fakeRunner{outcomes: map[string]RunOutcome{}, blockOn: make(chan struct{})}
	queue := NewQueue(runner, nil, nil)
	if err := queue.Start(nil); err == nil {
		t.Fatal("空账号应报错")
	}
	if err := queue.Start([]Account{{ID: "c1", Name: "一号"}}); err != nil {
		t.Fatalf("第一次开始失败：%v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := queue.Start([]Account{{ID: "c2", Name: "二号"}}); err == nil {
		t.Fatal("运行中重复开始应报错")
	}
	queue.Stop()
	close(runner.blockOn)
	waitQueueState(t, queue, "done", 5*time.Second)
}

func TestQueueWithYKTRunnerFakePage(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过真实浏览器测试")
	}
	if _, err := ChromePath(); err != nil {
		t.Skipf("本机没有 Chrome/Chromium：%v", err)
	}
	server := fakeCourseServer(t)
	runner := newTestRunner(t, t.TempDir(), fakeStartScript)
	queue := NewQueue(runner, nil, nil)
	queue.SetPacing(func(int) time.Duration { return 0 })
	accounts := []Account{
		{ID: "q1", Name: "一号", Site: server.URL},
		{ID: "q2", Name: "二号", Site: server.URL},
	}
	if err := queue.Start(accounts); err != nil {
		t.Fatalf("开始队列失败：%v", err)
	}
	snapshot := waitQueueState(t, queue, "done", 30*time.Second)
	for _, item := range snapshot.Items {
		if item.Status != RunOK {
			t.Fatalf("账号 %s 状态 = %s，期望 ok（%s）", item.Name, item.Status, item.Message)
		}
	}
}

func TestQueueWithYKTRunnerNeedsLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过真实浏览器测试")
	}
	if _, err := ChromePath(); err != nil {
		t.Skipf("本机没有 Chrome/Chromium：%v", err)
	}
	loginServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>登录</title><input type="password">`)
	}))
	defer loginServer.Close()
	runner := newTestRunner(t, t.TempDir(), fakeStartScript)
	queue := NewQueue(runner, nil, nil)
	queue.SetPacing(func(int) time.Duration { return 0 })
	if err := queue.Start([]Account{{ID: "n1", Name: "未登录", Site: loginServer.URL}}); err != nil {
		t.Fatalf("开始失败：%v", err)
	}
	snapshot := waitQueueState(t, queue, "done", 20*time.Second)
	if len(snapshot.Items) != 1 || snapshot.Items[0].Status != RunNeedsLogin {
		t.Fatalf("应为 needs_login：%+v", snapshot)
	}
}
