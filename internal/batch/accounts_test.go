package batch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "batch"))
	if err != nil {
		t.Fatalf("NewStore 失败：%v", err)
	}
	return store
}

func TestAccountAddListPersist(t *testing.T) {
	base := filepath.Join(t.TempDir(), "batch")
	store, err := NewStore(base)
	if err != nil {
		t.Fatalf("NewStore 失败：%v", err)
	}
	a1, err := store.Add("  张三  ", "")
	if err != nil {
		t.Fatalf("添加账号失败：%v", err)
	}
	if a1.Name != "张三" || a1.Site != "https://yuketang.cn/" || a1.LoginState != LoginUnknown {
		t.Fatalf("账号字段不对：%+v", a1)
	}
	if _, err := store.Add("李四", "https://www.yuketang.cn/"); err != nil {
		t.Fatalf("添加第二个账号失败：%v", err)
	}

	reloaded, err := NewStore(base)
	if err != nil {
		t.Fatalf("重新打开失败：%v", err)
	}
	accounts := reloaded.List()
	if len(accounts) != 2 || accounts[0].Name != "张三" || accounts[1].Name != "李四" {
		t.Fatalf("持久化结果不对：%+v", accounts)
	}

	raw, err := os.ReadFile(filepath.Join(base, "accounts.json"))
	if err != nil {
		t.Fatalf("读取账号库失败：%v", err)
	}
	if strings.Contains(string(raw), "password") || strings.Contains(string(raw), "passwd") {
		t.Fatalf("账号库不应包含密码字段：%s", raw)
	}
	var data struct {
		Version  int       `json:"version"`
		Accounts []Account `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &data); err != nil || data.Version != 1 {
		t.Fatalf("账号库 JSON 不合法：%v %+v", err, data)
	}
	info, err := os.Stat(filepath.Join(base, "accounts.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("账号库权限应为 0600：%v %v", err, info)
	}
}

func TestAccountRemoveCleansProfile(t *testing.T) {
	store := newTestStore(t)
	account, err := store.Add("要删除的账号", "")
	if err != nil {
		t.Fatalf("添加失败：%v", err)
	}
	profileDir, err := store.ProfileDir(account.ID)
	if err != nil {
		t.Fatalf("读取 profile 失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "cookie"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("写测试文件失败：%v", err)
	}
	if err := store.Remove(account.ID); err != nil {
		t.Fatalf("删除失败：%v", err)
	}
	if _, err := os.Stat(profileDir); !os.IsNotExist(err) {
		t.Fatalf("profile 目录应被删除：%v", err)
	}
	if len(store.List()) != 0 {
		t.Fatal("账号列表应为空")
	}
	if err := store.Remove("acc_not_exist"); err != nil {
		t.Fatalf("删除不存在的账号不应报错：%v", err)
	}
}

func TestAccountLoginStateAndLastRun(t *testing.T) {
	store := newTestStore(t)
	account, _ := store.Add("状态账号", "")
	if err := store.SetLoginState(account.ID, LoginOK, ""); err != nil {
		t.Fatalf("更新登录态失败：%v", err)
	}
	got, _ := store.Get(account.ID)
	if got.LoginState != LoginOK {
		t.Fatalf("登录态 = %s，期望 logged_in", got.LoginState)
	}
	if err := store.SetLastRun(account.ID, "needs_login", "登录已失效"); err != nil {
		t.Fatalf("记录运行结果失败：%v", err)
	}
	got, _ = store.Get(account.ID)
	if got.LoginState != LoginNeeded || got.LastError != "登录已失效" || got.LastRunAt == "" {
		t.Fatalf("运行结果未正确落盘：%+v", got)
	}
	if err := store.SetLastRun("acc_missing", "ok", ""); err == nil {
		t.Fatal("不存在的账号应报错")
	}
}

func TestAccountValidation(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Add("   ", ""); err == nil {
		t.Fatal("空备注应报错")
	}
	if _, err := store.Add(strings.Repeat("长", 41), ""); err == nil {
		t.Fatal("超长备注应报错")
	}
	account, _ := store.Add("正常", "")
	if err := store.Rename(account.ID, "新备注"); err != nil {
		t.Fatalf("重命名失败：%v", err)
	}
	if got, _ := store.Get(account.ID); got.Name != "新备注" {
		t.Fatalf("重命名未生效：%+v", got)
	}
}
