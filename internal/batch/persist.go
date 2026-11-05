package batch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// persistedQueue 是落盘的队列快照。
type persistedQueue struct {
	Version int        `json:"version"`
	State   QueueState `json:"state"`
}

// SaveQueueState 原子写入队列快照（0600），用于崩溃恢复。
func SaveQueueState(path string, state QueueState) error {
	if path == "" {
		return errors.New("队列状态路径为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(persistedQueue{Version: 1, State: state}, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "queue-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(encoded); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

// LoadQueueState 读取队列快照；文件不存在返回 idle。
func LoadQueueState(path string) (QueueState, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return QueueState{State: "idle"}, nil
	}
	if err != nil {
		return QueueState{}, err
	}
	var data persistedQueue
	if err := json.Unmarshal(raw, &data); err != nil {
		return QueueState{}, fmt.Errorf("队列状态文件损坏：%v", err)
	}
	if data.State.State == "" {
		data.State.State = "idle"
	}
	return data.State, nil
}

// RecoverQueueState 把崩溃/强退时残留的 running/stopping 队列标记为 interrupted，
// 并把未完成的账号标成 cancelled，避免下次启动误以为还在运行。
func RecoverQueueState(state QueueState) QueueState {
	if state.State != "running" && state.State != "stopping" {
		return state
	}
	now := time.Now().Format(time.RFC3339)
	for i := range state.Items {
		switch state.Items[i].Status {
		case RunRunning, RunQueued:
			state.Items[i].Status = RunCancelled
			state.Items[i].Message = "应用退出导致中断"
			state.Items[i].EndedAt = now
		}
	}
	state.State = "interrupted"
	state.Current = ""
	state.Message = "上次批量未完成（应用退出）"
	state.UpdatedAt = now
	return state
}

// ActiveQueueState 判断快照是否仍有未完成内容（用于决定是否恢复）。
func ActiveQueueState(state QueueState) bool {
	return state.State == "running" || state.State == "stopping"
}
