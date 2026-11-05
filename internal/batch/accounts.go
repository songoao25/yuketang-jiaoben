package batch

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LoginState 是账号登录态。
type LoginState string

const (
	LoginUnknown LoginState = "unknown"     // 刚添加，尚未登录
	LoginOK      LoginState = "logged_in"   // 登录态有效
	LoginNeeded  LoginState = "needs_login" // 登录态失效，需要重新手动登录
	LoginRunning LoginState = "running"     // 正在批量运行
	LoginError   LoginState = "error"       // 上次运行/登录出错
)

// Account 是一个待批量运行的账号；不含密码，登录态由独立浏览器 profile 承担。
type Account struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Site       string     `json:"site"`
	Profile    string     `json:"profile"` // 相对 baseDir 的 profile 目录
	LoginState LoginState `json:"login_state"`
	AddedAt    string     `json:"added_at"`
	LastRunAt  string     `json:"last_run_at,omitempty"`
	LastResult string     `json:"last_result,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
}

type storeData struct {
	Version  int       `json:"version"`
	Accounts []Account `json:"accounts"`
}

// Store 管理 ~/.ykt/batch/accounts.json 与每个账号的浏览器 profile 目录。
// 所有写操作串行化，落盘使用临时文件 + rename，避免半截文件。
type Store struct {
	mu           sync.Mutex
	baseDir      string
	path         string
	profilesRoot string
	data         storeData
}

// NewStore 打开（或初始化）账号库。baseDir 为空时使用 ~/.ykt/batch。
func NewStore(baseDir string) (*Store, error) {
	if strings.TrimSpace(baseDir) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("无法确定用户目录：%v", err)
		}
		baseDir = filepath.Join(home, ".ykt", "batch")
	}
	store := &Store{
		baseDir:      baseDir,
		path:         filepath.Join(baseDir, "accounts.json"),
		profilesRoot: filepath.Join(baseDir, "profiles"),
		data:         storeData{Version: 1, Accounts: []Account{}},
	}
	if err := os.MkdirAll(store.profilesRoot, 0o700); err != nil {
		return nil, fmt.Errorf("创建账号目录失败：%v", err)
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取账号库失败：%v", err)
	}
	var data storeData
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("账号库文件损坏：%v", err)
	}
	if data.Version <= 0 {
		data.Version = 1
	}
	if data.Accounts == nil {
		data.Accounts = []Account{}
	}
	for i := range data.Accounts {
		if data.Accounts[i].LoginState == "" {
			data.Accounts[i].LoginState = LoginUnknown
		}
	}
	s.data = data
	return nil
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(s.baseDir, 0o700); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(s.baseDir, "accounts-*.tmp")
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
	return os.Rename(tempName, s.path)
}

// List 返回账号快照（副本）。
func (s *Store) List() []Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Account, len(s.data.Accounts))
	copy(out, s.data.Accounts)
	return out
}

// Get 按 ID 查账号。
func (s *Store) Get(id string) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, account := range s.data.Accounts {
		if account.ID == id {
			return account, true
		}
	}
	return Account{}, false
}

// Add 新建账号并创建 0700 的独立 profile 目录。
func (s *Store) Add(name, site string) (Account, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Account{}, errors.New("账号备注不能为空")
	}
	if len([]rune(name)) > 40 {
		return Account{}, errors.New("账号备注最多 40 个字")
	}
	site = strings.TrimSpace(site)
	if site == "" {
		site = "https://yuketang.cn/"
	}
	id, err := newAccountID()
	if err != nil {
		return Account{}, err
	}
	profileRel := filepath.Join("profiles", id)
	profileAbs := filepath.Join(s.baseDir, profileRel)
	if err := os.MkdirAll(profileAbs, 0o700); err != nil {
		return Account{}, fmt.Errorf("创建账号浏览器目录失败：%v", err)
	}
	account := Account{
		ID:         id,
		Name:       name,
		Site:       site,
		Profile:    profileRel,
		LoginState: LoginUnknown,
		AddedAt:    time.Now().Format(time.RFC3339),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Accounts = append(s.data.Accounts, account)
	if err := s.saveLocked(); err != nil {
		return Account{}, err
	}
	return account, nil
}

// Remove 删除账号并清空它的浏览器 profile（Cookie/登录态）。找不到视为已删除。
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	index := -1
	var account Account
	for i, candidate := range s.data.Accounts {
		if candidate.ID == id {
			index = i
			account = candidate
			break
		}
	}
	if index < 0 {
		s.mu.Unlock()
		return nil
	}
	s.data.Accounts = append(s.data.Accounts[:index], s.data.Accounts[index+1:]...)
	err := s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	profileAbs := filepath.Join(s.baseDir, account.Profile)
	if rel, relErr := filepath.Rel(s.profilesRoot, profileAbs); relErr != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("拒绝删除可疑的 profile 路径：%s", profileAbs)
	}
	if err := os.RemoveAll(profileAbs); err != nil {
		return fmt.Errorf("删除浏览器数据失败：%v", err)
	}
	return nil
}

// Rename 修改账号备注。
func (s *Store) Rename(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("账号备注不能为空")
	}
	if len([]rune(name)) > 40 {
		return errors.New("账号备注最多 40 个字")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Accounts {
		if s.data.Accounts[i].ID == id {
			s.data.Accounts[i].Name = name
			return s.saveLocked()
		}
	}
	return fmt.Errorf("账号不存在：%s", id)
}

// SetLoginState 更新登录态。
func (s *Store) SetLoginState(id string, state LoginState, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Accounts {
		if s.data.Accounts[i].ID == id {
			s.data.Accounts[i].LoginState = state
			s.data.Accounts[i].LastError = errMsg
			return s.saveLocked()
		}
	}
	return fmt.Errorf("账号不存在：%s", id)
}

// SetLastRun 记录一次运行结果（ok / failed / cancelled / skipped）。
func (s *Store) SetLastRun(id, result, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Accounts {
		if s.data.Accounts[i].ID == id {
			s.data.Accounts[i].LastRunAt = time.Now().Format(time.RFC3339)
			s.data.Accounts[i].LastResult = result
			s.data.Accounts[i].LastError = errMsg
			if result == "ok" {
				s.data.Accounts[i].LoginState = LoginOK
			} else if result == "needs_login" {
				s.data.Accounts[i].LoginState = LoginNeeded
			} else if result == "failed" {
				s.data.Accounts[i].LoginState = LoginError
			}
			return s.saveLocked()
		}
	}
	return fmt.Errorf("账号不存在：%s", id)
}

// ProfileDir 返回账号 profile 的绝对路径。
func (s *Store) ProfileDir(id string) (string, error) {
	account, ok := s.Get(id)
	if !ok {
		return "", fmt.Errorf("账号不存在：%s", id)
	}
	return filepath.Join(s.baseDir, account.Profile), nil
}

func newAccountID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return "acc_" + hex.EncodeToString(buf[:]), nil
}

// QueueStatePath 返回批量队列快照文件路径（用于崩溃恢复）。
func (s *Store) QueueStatePath() string {
	return filepath.Join(s.baseDir, "queue-state.json")
}
