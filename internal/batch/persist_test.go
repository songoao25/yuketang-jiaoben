package batch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQueueStateSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batch", "queue-state.json")
	state := QueueState{
		State: "running", Current: "a1",
		Items: []QueueItem{{AccountID: "a1", Name: "一号", Status: RunRunning}, {AccountID: "a2", Name: "二号", Status: RunQueued}},
	}
	if err := SaveQueueState(path, state); err != nil {
		t.Fatalf("保存队列状态失败：%v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("队列状态文件权限应为 0600：%v %v", err, info)
	}
	loaded, err := LoadQueueState(path)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	if loaded.State != "running" || len(loaded.Items) != 2 {
		t.Fatalf("读取结果不对：%+v", loaded)
	}
}

func TestQueueStateLoadMissing(t *testing.T) {
	state, err := LoadQueueState(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || state.State != "idle" {
		t.Fatalf("缺失文件应返回 idle：%+v %v", state, err)
	}
}

func TestRecoverQueueState(t *testing.T) {
	state := QueueState{
		State: "running", Current: "a1",
		Items: []QueueItem{
			{AccountID: "a1", Name: "一号", Status: RunRunning},
			{AccountID: "a2", Name: "二号", Status: RunQueued},
			{AccountID: "a3", Name: "三号", Status: RunOK},
		},
	}
	recovered := RecoverQueueState(state)
	if recovered.State != "interrupted" || recovered.Current != "" {
		t.Fatalf("恢复结果不对：%+v", recovered)
	}
	if recovered.Items[0].Status != RunCancelled || recovered.Items[1].Status != RunCancelled {
		t.Fatalf("未完成账号应标记 cancelled：%+v", recovered.Items)
	}
	if recovered.Items[2].Status != RunOK {
		t.Fatalf("已完成账号不应被改动：%+v", recovered.Items[2])
	}
	done := QueueState{State: "done"}
	if RecoverQueueState(done).State != "done" {
		t.Fatal("已完成队列不应被改写")
	}
}
