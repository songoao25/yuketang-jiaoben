package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/songoao25/yuketang-jiaoben/internal/model"
)

// newTestStore 在临时目录创建 Store，绝不触碰真实 ~/.ykt。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenAt(t.TempDir())
	if err != nil {
		t.Fatalf("OpenAt 失败: %v", err)
	}
	return store
}

func TestOpenAtCreatesPrivateDir(t *testing.T) {
	home := filepath.Join(t.TempDir(), "nested", "ykt")
	store, err := OpenAt(home)
	if err != nil {
		t.Fatalf("OpenAt 失败: %v", err)
	}
	if store.HomeDir() != home {
		t.Fatalf("HomeDir = %q, want %q", store.HomeDir(), home)
	}
	info, err := os.Stat(home)
	if err != nil {
		t.Fatalf("配置目录不存在: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("配置路径不是目录")
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("目录权限 = %#o, want 0700", got)
	}
}

func TestOpenAtRejectsEmpty(t *testing.T) {
	if _, err := OpenAt("   "); err == nil {
		t.Fatal("OpenAt 空目录应返回错误")
	}
}

func TestOpenUsesYKTHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "ykt-home")
	t.Setenv("YKT_HOME", home)
	store, err := Open()
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	if store.HomeDir() != home {
		t.Fatalf("HomeDir = %q, want %q", store.HomeDir(), home)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("YKT_HOME 目录未创建: %v", err)
	}
}

func TestPaths(t *testing.T) {
	store := newTestStore(t)
	home := store.HomeDir()
	cases := map[string]string{
		store.ConfigPath(): filepath.Join(home, "config.json"),
		store.StatePath():  filepath.Join(home, "state.json"),
		store.ServerPath(): filepath.Join(home, "server.json"),
		store.LogPath():    filepath.Join(home, "server.log"),
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("路径 = %q, want %q", got, want)
		}
	}
}

func TestDefaultSettings(t *testing.T) {
	defaults := DefaultSettings()
	if defaults.Port != model.MinPort {
		t.Fatalf("Port = %d, want %d", defaults.Port, model.MinPort)
	}
	if defaults.DefaultURL != model.DefaultURL {
		t.Fatalf("DefaultURL = %q, want %q", defaults.DefaultURL, model.DefaultURL)
	}
	if !defaults.AutoStart || !defaults.SyncEnabled {
		t.Fatalf("AutoStart/SyncEnabled 应默认开启: %+v", defaults)
	}
	if defaults.Appearance != "system" {
		t.Fatalf("Appearance = %q, want system", defaults.Appearance)
	}
}

func TestLoadSettingsFreshStoreReturnsDefaults(t *testing.T) {
	store := newTestStore(t)
	if got := store.LoadSettings(); got != DefaultSettings() {
		t.Fatalf("LoadSettings = %+v, want %+v", got, DefaultSettings())
	}
}

func TestSaveLoadSettingsRoundTrip(t *testing.T) {
	store := newTestStore(t)
	want := model.Settings{
		Port:        8905,
		DefaultURL:  "https://changjiang.yuketang.cn/",
		AutoStart:   false,
		SyncEnabled: false,
		Appearance:  "dark",
	}
	if err := store.SaveSettings(want); err != nil {
		t.Fatalf("SaveSettings 失败: %v", err)
	}
	if got := store.LoadSettings(); got != want {
		t.Fatalf("LoadSettings = %+v, want %+v", got, want)
	}

	// 文件权限必须是 0600。
	info, err := os.Stat(store.ConfigPath())
	if err != nil {
		t.Fatalf("config.json 不存在: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config.json 权限 = %#o, want 0600", got)
	}

	// 磁盘结构应为 {"settings":{...}}。
	data, err := os.ReadFile(store.ConfigPath())
	if err != nil {
		t.Fatalf("读取 config.json 失败: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("config.json 不是合法 JSON: %v", err)
	}
	if _, ok := raw["settings"]; !ok {
		t.Fatalf("config.json 缺少 settings 键: %s", data)
	}
	if _, ok := raw["ai"]; ok {
		t.Fatalf("只保存 settings 时不应写入 ai 键: %s", data)
	}
}

func TestSaveSettingsValidates(t *testing.T) {
	store := newTestStore(t)
	bad := DefaultSettings()
	bad.Port = 80
	if err := store.SaveSettings(bad); err == nil {
		t.Fatal("非法端口应返回错误")
	}
	if _, err := os.Stat(store.ConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("校验失败时不应写入 config.json")
	}
}

func TestPartialSettingsFillDefaults(t *testing.T) {
	store := newTestStore(t)
	if err := os.WriteFile(store.ConfigPath(), []byte(`{"settings":{"port":8901}}`), 0o600); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	got := store.LoadSettings()
	want := DefaultSettings()
	want.Port = 8901
	if got != want {
		t.Fatalf("缺失字段未填充默认值: got %+v, want %+v", got, want)
	}
}

func TestLoadSettingsSanitizesInvalidValues(t *testing.T) {
	store := newTestStore(t)
	payload := `{"settings":{"port":80,"defaultUrl":"ftp://example.com/","autoStart":false,"syncEnabled":false,"appearance":"neon"}}`
	if err := os.WriteFile(store.ConfigPath(), []byte(payload), 0o600); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	got := store.LoadSettings()
	if got.Port != model.MinPort {
		t.Fatalf("非法端口未回退默认值: %d", got.Port)
	}
	if got.DefaultURL != model.DefaultURL {
		t.Fatalf("非法网址未回退默认值: %q", got.DefaultURL)
	}
	if got.Appearance != "system" {
		t.Fatalf("非法外观未回退默认值: %q", got.Appearance)
	}
	// 显式的 false 必须保留。
	if got.AutoStart || got.SyncEnabled {
		t.Fatalf("显式 false 被覆盖: %+v", got)
	}
}

func TestCorruptConfigFallsBackToDefaults(t *testing.T) {
	store := newTestStore(t)
	if err := os.WriteFile(store.ConfigPath(), []byte(`{"settings":{not-json`), 0o600); err != nil {
		t.Fatalf("写入损坏配置失败: %v", err)
	}
	if got := store.LoadSettings(); got != DefaultSettings() {
		t.Fatalf("损坏文件应返回默认设置: %+v", got)
	}
	if got := store.LoadAIConfig(); got != (model.AIConfig{}) {
		t.Fatalf("损坏文件应返回零值 AI 配置: %+v", got)
	}
	if got := store.LoadFeatures(); got != (model.FeatureConfig{}) {
		t.Fatalf("损坏文件应返回零值功能开关: %+v", got)
	}
	if store.HasUserConfig() {
		t.Fatal("损坏文件 HasUserConfig 应为 false")
	}
	// 损坏后仍可正常保存并恢复。
	if err := store.SaveSettings(DefaultSettings()); err != nil {
		t.Fatalf("损坏后保存失败: %v", err)
	}
	if got := store.LoadSettings(); got != DefaultSettings() {
		t.Fatalf("恢复后设置异常: %+v", got)
	}
}

func TestValidateSettings(t *testing.T) {
	valid := DefaultSettings()
	if err := ValidateSettings(valid); err != nil {
		t.Fatalf("默认设置应通过校验: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*model.Settings)
	}{
		{"端口过小", func(s *model.Settings) { s.Port = model.MinPort - 1 }},
		{"端口过大", func(s *model.Settings) { s.Port = model.MaxPort + 1 }},
		{"端口为零", func(s *model.Settings) { s.Port = 0 }},
		{"空网址", func(s *model.Settings) { s.DefaultURL = "" }},
		{"非 http 协议", func(s *model.Settings) { s.DefaultURL = "ftp://example.com/" }},
		{"无协议", func(s *model.Settings) { s.DefaultURL = "example.com" }},
		{"缺主机", func(s *model.Settings) { s.DefaultURL = "https://" }},
		{"非法外观", func(s *model.Settings) { s.Appearance = "neon" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := DefaultSettings()
			tt.mutate(&settings)
			if err := ValidateSettings(settings); err == nil {
				t.Fatalf("应校验失败: %+v", settings)
			}
		})
	}

	for _, port := range []int{model.MinPort, model.MinPort + 1, model.MaxPort} {
		settings := DefaultSettings()
		settings.Port = port
		if err := ValidateSettings(settings); err != nil {
			t.Fatalf("端口 %d 应合法: %v", port, err)
		}
	}
	for _, appearance := range []string{"", "system", "light", "dark"} {
		settings := DefaultSettings()
		settings.Appearance = appearance
		if err := ValidateSettings(settings); err != nil {
			t.Fatalf("外观 %q 应合法: %v", appearance, err)
		}
	}
	for _, rawURL := range []string{"http://127.0.0.1:8899/", "https://yuketang.cn/"} {
		settings := DefaultSettings()
		settings.DefaultURL = rawURL
		if err := ValidateSettings(settings); err != nil {
			t.Fatalf("网址 %q 应合法: %v", rawURL, err)
		}
	}
}

func TestAIConfigRoundTripPreservesOtherSections(t *testing.T) {
	store := newTestStore(t)
	settings := DefaultSettings()
	settings.Port = 8902
	if err := store.SaveSettings(settings); err != nil {
		t.Fatalf("SaveSettings 失败: %v", err)
	}
	ai := model.AIConfig{URL: "https://api.example.com/v1", Key: "sk-test", Model: "gpt", APIFormat: "openai", AuthMethod: "bearer"}
	if err := store.SaveAIConfig(ai); err != nil {
		t.Fatalf("SaveAIConfig 失败: %v", err)
	}
	if got := store.LoadAIConfig(); got != ai {
		t.Fatalf("LoadAIConfig = %+v, want %+v", got, ai)
	}
	if got := store.LoadSettings(); got != settings {
		t.Fatalf("保存 AI 配置后设置被破坏: %+v", got)
	}
}

func TestFeaturesRoundTripPreservesOtherSections(t *testing.T) {
	store := newTestStore(t)
	features := model.FeatureConfig{AutoAI: true, AutoComment: false}
	if err := store.SaveFeatures(features); err != nil {
		t.Fatalf("SaveFeatures 失败: %v", err)
	}
	if got := store.LoadFeatures(); got != features {
		t.Fatalf("LoadFeatures = %+v, want %+v", got, features)
	}
	ai := model.AIConfig{URL: "https://api.example.com/", Key: "k"}
	if err := store.SaveAIConfig(ai); err != nil {
		t.Fatalf("SaveAIConfig 失败: %v", err)
	}
	if got := store.LoadFeatures(); got != features {
		t.Fatalf("保存 AI 配置后功能开关被破坏: %+v", got)
	}
}

func TestHasUserConfig(t *testing.T) {
	store := newTestStore(t)
	if store.HasUserConfig() {
		t.Fatal("新目录 HasUserConfig 应为 false")
	}
	if err := store.SaveSettings(DefaultSettings()); err != nil {
		t.Fatalf("SaveSettings 失败: %v", err)
	}
	if store.HasUserConfig() {
		t.Fatal("只保存 settings 时 HasUserConfig 应为 false")
	}
	if err := store.SaveAIConfig(model.AIConfig{Key: "k"}); err != nil {
		t.Fatalf("SaveAIConfig 失败: %v", err)
	}
	if !store.HasUserConfig() {
		t.Fatal("保存 AI 配置后 HasUserConfig 应为 true")
	}

	other := newTestStore(t)
	if err := other.SaveFeatures(model.FeatureConfig{AutoAI: true}); err != nil {
		t.Fatalf("SaveFeatures 失败: %v", err)
	}
	if !other.HasUserConfig() {
		t.Fatal("保存功能开关后 HasUserConfig 应为 true")
	}

	// 手工构造只含 ai 键的配置。
	manual := newTestStore(t)
	if err := os.WriteFile(manual.ConfigPath(), []byte(`{"ai":null}`), 0o600); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	if !manual.HasUserConfig() {
		t.Fatal("存在 ai 键时 HasUserConfig 应为 true")
	}
}

func TestInstallStateRoundTrip(t *testing.T) {
	store := newTestStore(t)
	if got := store.LoadInstallState(); got != (model.InstallState{}) {
		t.Fatalf("初始安装记录应为零值: %+v", got)
	}
	state := model.InstallState{Installed: true, InSync: true, Version: "3.0.7", Hash: "abc", OpenedAt: 1700000000000, Note: "test"}
	if err := store.SaveInstallState(state); err != nil {
		t.Fatalf("SaveInstallState 失败: %v", err)
	}
	if got := store.LoadInstallState(); got != state {
		t.Fatalf("LoadInstallState = %+v, want %+v", got, state)
	}
	info, err := os.Stat(store.StatePath())
	if err != nil {
		t.Fatalf("state.json 不存在: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state.json 权限 = %#o, want 0600", got)
	}

	if err := os.WriteFile(store.StatePath(), []byte("not json"), 0o600); err != nil {
		t.Fatalf("写入损坏 state.json 失败: %v", err)
	}
	if got := store.LoadInstallState(); got != (model.InstallState{}) {
		t.Fatalf("损坏 state.json 应返回零值: %+v", got)
	}
}

func TestServerInfoRoundTrip(t *testing.T) {
	store := newTestStore(t)
	if _, ok := store.LoadServerInfo(); ok {
		t.Fatal("初始 ServerInfo 不应存在")
	}
	info := model.ServerInfo{PID: 12345, Port: 8899, StartedAt: 1700000000000, Version: "1.0.0"}
	if err := store.SaveServerInfo(info); err != nil {
		t.Fatalf("SaveServerInfo 失败: %v", err)
	}
	got, ok := store.LoadServerInfo()
	if !ok || got != info {
		t.Fatalf("LoadServerInfo = %+v/%v, want %+v/true", got, ok, info)
	}
	if err := store.ClearServerInfo(); err != nil {
		t.Fatalf("ClearServerInfo 失败: %v", err)
	}
	if _, ok := store.LoadServerInfo(); ok {
		t.Fatal("清理后 ServerInfo 不应存在")
	}
	// 重复清理不应报错。
	if err := store.ClearServerInfo(); err != nil {
		t.Fatalf("重复 ClearServerInfo 不应报错: %v", err)
	}
}

func TestLoadServerInfoRejectsIncompleteRecord(t *testing.T) {
	store := newTestStore(t)
	if err := os.WriteFile(store.ServerPath(), []byte(`{"pid":0,"port":8899}`), 0o600); err != nil {
		t.Fatalf("写入 server.json 失败: %v", err)
	}
	if _, ok := store.LoadServerInfo(); ok {
		t.Fatal("PID 为 0 的记录应视为无效")
	}
	if err := os.WriteFile(store.ServerPath(), []byte(`{broken`), 0o600); err != nil {
		t.Fatalf("写入损坏 server.json 失败: %v", err)
	}
	if _, ok := store.LoadServerInfo(); ok {
		t.Fatal("损坏记录应视为无效")
	}
}

func TestAtomicWriteLeavesNoTempFiles(t *testing.T) {
	store := newTestStore(t)
	for i := 0; i < 5; i++ {
		if err := store.SaveSettings(DefaultSettings()); err != nil {
			t.Fatalf("SaveSettings 失败: %v", err)
		}
		if err := store.SaveAIConfig(model.AIConfig{Key: strings.Repeat("k", i)}); err != nil {
			t.Fatalf("SaveAIConfig 失败: %v", err)
		}
	}
	entries, err := os.ReadDir(store.HomeDir())
	if err != nil {
		t.Fatalf("读取配置目录失败: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Fatalf("原子写残留临时文件: %s", entry.Name())
		}
	}
}

// ---------------------------------------------------------------- QA M3 / M4 回归

func TestHasConfigSection(t *testing.T) {
	store := newTestStore(t)
	if store.HasConfigSection("ai") || store.HasConfigSection("features") {
		t.Fatal("新目录不应存在任何配置段")
	}
	if store.HasConfigSection("") {
		t.Fatal("空段名应返回 false")
	}

	if err := store.SaveAIConfig(model.AIConfig{Key: "k"}); err != nil {
		t.Fatalf("SaveAIConfig 失败: %v", err)
	}
	if !store.HasConfigSection("ai") {
		t.Fatal("保存 AI 配置后 HasConfigSection(ai) 应为 true")
	}
	if store.HasConfigSection("features") {
		t.Fatal("只保存 AI 配置时 HasConfigSection(features) 应为 false")
	}

	if err := store.SaveFeatures(model.FeatureConfig{AutoAI: true}); err != nil {
		t.Fatalf("SaveFeatures 失败: %v", err)
	}
	if !store.HasConfigSection("features") {
		t.Fatal("保存功能开关后 HasConfigSection(features) 应为 true")
	}

	// 只有 settings 时两段都不存在。
	settingsOnly := newTestStore(t)
	if err := settingsOnly.SaveSettings(DefaultSettings()); err != nil {
		t.Fatalf("SaveSettings 失败: %v", err)
	}
	if settingsOnly.HasConfigSection("ai") || settingsOnly.HasConfigSection("features") {
		t.Fatal("只有 settings 时不应报告 ai/features 段")
	}
	if !settingsOnly.HasConfigSection("settings") {
		t.Fatal("HasConfigSection(settings) 应为 true")
	}

	// 损坏文件返回 false。
	broken := newTestStore(t)
	if err := os.WriteFile(broken.ConfigPath(), []byte(`{"ai":{`), 0o600); err != nil {
		t.Fatalf("写入损坏配置失败: %v", err)
	}
	if broken.HasConfigSection("ai") || broken.HasUserConfig() {
		t.Fatal("损坏文件 HasConfigSection/HasUserConfig 应为 false")
	}
}

func TestLoadSettingsMigratesLegacyFlatConfig(t *testing.T) {
	store := newTestStore(t)
	legacy := `{"url":"https://changjiang.yuketang.cn/","port":8900}`
	if err := os.WriteFile(store.ConfigPath(), []byte(legacy), 0o600); err != nil {
		t.Fatalf("写入旧配置失败: %v", err)
	}
	got := store.LoadSettings()
	if got.Port != 8900 {
		t.Fatalf("迁移后端口 = %d, want 8900", got.Port)
	}
	if got.DefaultURL != "https://changjiang.yuketang.cn/" {
		t.Fatalf("迁移后默认地址 = %q", got.DefaultURL)
	}
	if !got.AutoStart || !got.SyncEnabled || got.Appearance != "system" {
		t.Fatalf("迁移后其余字段应为默认值: %+v", got)
	}

	// 迁移只在读取时发生；下一次保存写新结构。
	if err := store.SaveSettings(got); err != nil {
		t.Fatalf("SaveSettings 失败: %v", err)
	}
	data, err := os.ReadFile(store.ConfigPath())
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("配置不是合法 JSON: %v", err)
	}
	if _, ok := raw["settings"]; !ok {
		t.Fatalf("保存后应写入 settings 段: %s", data)
	}
	if _, ok := raw["url"]; ok {
		t.Fatalf("保存后不应保留旧 url 顶层字段: %s", data)
	}
	if _, ok := raw["port"]; ok {
		t.Fatalf("保存后不应保留旧 port 顶层字段: %s", data)
	}
}

func TestLoadSettingsLegacyFlatInvalidValuesFallback(t *testing.T) {
	store := newTestStore(t)
	// 旧 CLI 允许任意端口，新区间外的端口应回退默认值。
	legacy := `{"url":"ftp://example.com/","port":9000}`
	if err := os.WriteFile(store.ConfigPath(), []byte(legacy), 0o600); err != nil {
		t.Fatalf("写入旧配置失败: %v", err)
	}
	got := store.LoadSettings()
	if got.Port != model.MinPort {
		t.Fatalf("越界旧端口应回退默认值: %d", got.Port)
	}
	if got.DefaultURL != model.DefaultURL {
		t.Fatalf("非法旧网址应回退默认值: %q", got.DefaultURL)
	}
}

func TestLoadSettingsNewSectionWinsOverLegacyFlat(t *testing.T) {
	store := newTestStore(t)
	// 新旧字段同时存在时以 settings 段为准（旧字段只在没有 settings 段时迁移）。
	content := `{"url":"https://old.example.com/","port":8901,` +
		`"settings":{"port":8905,"defaultUrl":"https://new.example.com/","autoStart":false,"syncEnabled":false,"appearance":"dark"}}`
	if err := os.WriteFile(store.ConfigPath(), []byte(content), 0o600); err != nil {
		t.Fatalf("写入混合配置失败: %v", err)
	}
	got := store.LoadSettings()
	if got.Port != 8905 || got.DefaultURL != "https://new.example.com/" {
		t.Fatalf("settings 段应优先: %+v", got)
	}
	if got.AutoStart || got.SyncEnabled || got.Appearance != "dark" {
		t.Fatalf("settings 段字段未生效: %+v", got)
	}
}

func TestLoadInstallStateMigratesOpenedHash(t *testing.T) {
	store := newTestStore(t)
	legacy := `{"openedHash":"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789","version":"3.0.7","openedAt":1700000000000}`
	if err := os.WriteFile(store.StatePath(), []byte(legacy), 0o600); err != nil {
		t.Fatalf("写入旧安装记录失败: %v", err)
	}
	got := store.LoadInstallState()
	if got.Hash != "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789" {
		t.Fatalf("openedHash 未迁移为 hash: %+v", got)
	}
	if !got.Installed {
		t.Fatalf("旧记录迁移后 Installed 应为 true: %+v", got)
	}
	if got.Version != "3.0.7" || got.OpenedAt != 1700000000000 {
		t.Fatalf("旧记录其它字段未保留: %+v", got)
	}

	// 保存后写新结构（不再含 openedHash）。
	if err := store.SaveInstallState(got); err != nil {
		t.Fatalf("SaveInstallState 失败: %v", err)
	}
	data, err := os.ReadFile(store.StatePath())
	if err != nil {
		t.Fatalf("读取安装记录失败: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("安装记录不是合法 JSON: %v", err)
	}
	if _, ok := raw["openedHash"]; ok {
		t.Fatalf("保存后不应保留 openedHash: %s", data)
	}
	if _, ok := raw["hash"]; !ok {
		t.Fatalf("保存后应写入 hash: %s", data)
	}
}

func TestLoadInstallStateNewFormatUnaffected(t *testing.T) {
	store := newTestStore(t)
	state := model.InstallState{Installed: true, InSync: true, Version: "3.0.7", Hash: "abc", OpenedAt: 1700000000001, Note: "n"}
	if err := store.SaveInstallState(state); err != nil {
		t.Fatalf("SaveInstallState 失败: %v", err)
	}
	if got := store.LoadInstallState(); got != state {
		t.Fatalf("新格式安装记录被迁移逻辑破坏: %+v", got)
	}
}
