package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// 更新/保存配置时不能把其他段或未知字段一起覆盖掉。
func TestSaveSectionPreservesOtherSectionsAndUnknownFields(t *testing.T) {
	store := newTestStore(t)
	initial := `{
  "ai": {"url": "https://api.deepseek.com", "model": "deepseek-chat", "key": ""},
  "settings": {"port": 8899, "defaultUrl": "https://yuketang.cn/"},
  "features": {"autoAI": false},
  "futureField": {"keep": true}
}`
	if err := os.WriteFile(store.ConfigPath(), []byte(initial), 0o600); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}
	ai := store.LoadAIConfig()
	ai.Model = "deepseek-reasoner"
	if err := store.SaveAIConfig(ai); err != nil {
		t.Fatalf("保存 AI 配置失败: %v", err)
	}
	data, err := os.ReadFile(store.ConfigPath())
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("配置不是合法 JSON: %v", err)
	}
	for _, key := range []string{"ai", "settings", "features", "futureField"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("保存 AI 后丢了 %q：%s", key, data)
		}
	}
	if got := store.LoadSettings(); got.Port != 8899 {
		t.Fatalf("保存 AI 不应改动设置：%+v", got)
	}
	if got := store.LoadFeatures(); got.AutoAI {
		t.Fatalf("保存 AI 不应改动功能开关：%+v", got)
	}
}

// 覆盖前必须保留上一版 .bak，防止一次保存把配置全清空。
func TestSaveKeepsPreviousConfigBackup(t *testing.T) {
	store := newTestStore(t)
	first := DefaultSettings()
	first.Port = 8901
	if err := store.SaveSettings(first); err != nil {
		t.Fatalf("首次保存失败: %v", err)
	}
	second := first
	second.Port = 8902
	if err := store.SaveSettings(second); err != nil {
		t.Fatalf("二次保存失败: %v", err)
	}
	data, err := os.ReadFile(store.ConfigPath() + ".bak")
	if err != nil {
		t.Fatalf("应保留 config.json.bak: %v", err)
	}
	if !strings.Contains(string(data), `"port": 8901`) {
		t.Fatalf("备份内容不是上一版: %s", data)
	}
}
