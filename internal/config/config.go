// Package config 负责 ykt 的本机配置目录：路径约定、JSON 读写与校验。
//
// 所有文件都保存在同一个 home 目录下（默认 ~/.ykt，可用 YKT_HOME 覆盖，
// 测试可用 OpenAt 指定临时目录）：
//
//	config.json  设置、AI 配置与功能开关
//	state.json   用户脚本安装记录
//	server.json  本地服务进程信息
//	server.log   JSON Lines 运行日志（由 internal/server 写入）
//
// 写入一律使用「临时文件 + rename」的原子写，权限 0600；目录权限 0700。
// 任何单个文件损坏都不会导致进程崩溃：解析失败时返回默认值。
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/songoao25/yuketang-jiaoben/internal/model"
)

// homeEnv 是覆盖配置目录的环境变量。
const homeEnv = "YKT_HOME"

// Store 表示一个 ykt 配置目录。字段不导出，只能通过 Open / OpenAt 创建。
type Store struct {
	home string

	mu sync.Mutex // 保护 config.json 的读改写，避免同进程并发写互相覆盖
}

// Open 打开配置目录：YKT_HOME 环境变量优先，否则使用 ~/.ykt。
func Open() (*Store, error) {
	if home := strings.TrimSpace(os.Getenv(homeEnv)); home != "" {
		return OpenAt(home)
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("无法确定用户主目录: %w", err)
	}
	return OpenAt(filepath.Join(userHome, ".ykt"))
}

// OpenAt 打开指定目录（测试用），不存在时创建。
func OpenAt(home string) (*Store, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil, errors.New("配置目录不能为空")
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return nil, fmt.Errorf("解析配置目录失败: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("创建配置目录失败: %w", err)
	}
	// 目录可能是旧版本（Node CLI）创建的，统一收紧到 0700。
	if err := os.Chmod(abs, 0o700); err != nil {
		return nil, fmt.Errorf("设置配置目录权限失败: %w", err)
	}
	// 清理上次异常退出留下的原子写临时文件（QA R2：GUI 与 CLI 共用此入口）。
	cleanupStaleTempFiles(abs, 30*time.Second)
	return &Store{home: abs}, nil
}

// cleanupStaleTempFiles 删除配置目录中超过 age 的 .tmp-* 原子写残留。
// 只删除本程序写文件时使用的临时文件前缀，失败静默忽略。
func cleanupStaleTempFiles(dir string, age time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	deadline := time.Now().Add(-age)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), ".tmp-") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(deadline) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
}

// HomeDir 返回配置目录。
func (s *Store) HomeDir() string { return s.home }

// ConfigPath 返回 config.json 路径。
func (s *Store) ConfigPath() string { return filepath.Join(s.home, "config.json") }

// StatePath 返回 state.json 路径。
func (s *Store) StatePath() string { return filepath.Join(s.home, "state.json") }

// ServerPath 返回 server.json 路径。
func (s *Store) ServerPath() string { return filepath.Join(s.home, "server.json") }

// LogPath 返回 server.log 路径。
func (s *Store) LogPath() string { return filepath.Join(s.home, "server.log") }

// DefaultSettings 返回默认设置。
func DefaultSettings() model.Settings {
	return model.Settings{
		Port:        model.MinPort,
		DefaultURL:  model.DefaultURL,
		AutoStart:   true,
		SyncEnabled: true,
		Appearance:  "system",
	}
}

// ValidateSettings 校验设置：端口必须在 [model.MinPort, model.MaxPort]，
// 默认地址必须是合法的 http(s) URL，外观只允许 system/light/dark（空串视为默认）。
func ValidateSettings(settings model.Settings) error {
	if settings.Port < model.MinPort || settings.Port > model.MaxPort {
		return fmt.Errorf("端口必须在 %d~%d 之间，当前为 %d", model.MinPort, model.MaxPort, settings.Port)
	}
	raw := strings.TrimSpace(settings.DefaultURL)
	if raw == "" {
		return errors.New("默认网址不能为空")
	}
	if !validHTTPURL(raw) {
		return errors.New("默认网址必须是合法的 http:// 或 https:// 地址")
	}
	switch settings.Appearance {
	case "", "system", "light", "dark":
	default:
		return errors.New("外观必须是 system、light 或 dark")
	}
	return nil
}

// validHTTPURL 判断字符串是否是带主机的 http(s) URL。
func validHTTPURL(raw string) bool {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return parsed.Host != ""
}

// fileConfig 是 config.json 的磁盘结构。
type fileConfig struct {
	Settings *model.Settings      `json:"settings,omitempty"`
	AI       *model.AIConfig      `json:"ai,omitempty"`
	Features *model.FeatureConfig `json:"features,omitempty"`
}

// settingsJSON 用指针区分「字段缺失」与「显式零值」，用于缺省值填充。
type settingsJSON struct {
	Port           *int    `json:"port"`
	DefaultURL     *string `json:"defaultUrl"`
	AutoStart      *bool   `json:"autoStart"`
	SyncEnabled    *bool   `json:"syncEnabled"`
	Appearance     *string `json:"appearance"`
	OnboardingDone *bool   `json:"onboardingDone"`
}

// readRawConfig 读取 config.json 原始字节；文件不存在或不可读返回 nil。
func (s *Store) readRawConfig() []byte {
	data, err := os.ReadFile(s.ConfigPath())
	if err != nil {
		return nil
	}
	return data
}

// parseFileConfig 解析 config.json；失败时把损坏文件备份一份，再返回空结构。
// 备份保证后续保存不会静默覆盖用户原来的配置（可从 .corrupt-* 恢复）。
func (s *Store) parseFileConfig() fileConfig {
	data := s.readRawConfig()
	if len(data) == 0 {
		return fileConfig{}
	}
	var cfg fileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		s.backupCorruptConfig()
		return fileConfig{}
	}
	return cfg
}

// backupCorruptConfig 把无法解析的 config.json 复制成带时间戳的备份（只备份一次）。
func (s *Store) backupCorruptConfig() {
	path := s.ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return
	}
	backup := path + ".corrupt-" + time.Now().Format("20060102-150405")
	if existing, err := os.Stat(backup); err == nil && existing.Size() > 0 {
		return
	}
	_ = os.WriteFile(backup, data, 0o600)
}

// LoadSettings 读取设置，缺失字段用默认值填充；读取或解析失败返回默认值。
//
// 兼容旧 Node CLI 的扁平结构 config.json（{"url": "...", "port": 8900}）：
// 当文件里没有 settings 段时，把顶层 url/port 作为迁移来源；下一次保存会写新结构。
func (s *Store) LoadSettings() model.Settings {
	defaults := DefaultSettings()
	data := s.readRawConfig()
	if len(data) == 0 {
		return defaults
	}
	var raw struct {
		Settings *settingsJSON `json:"settings"`
		// 旧 Node CLI 扁平字段（QA M4 迁移来源）
		LegacyURL  *string `json:"url"`
		LegacyPort *int    `json:"port"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return defaults
	}
	if raw.Settings == nil {
		// 没有 settings 段：仅在存在旧扁平字段时迁移，否则读默认值。
		settings := defaults
		if raw.LegacyPort != nil {
			settings.Port = *raw.LegacyPort
		}
		if raw.LegacyURL != nil {
			settings.DefaultURL = *raw.LegacyURL
		}
		return sanitizeSettings(settings)
	}
	settings := defaults
	if raw.Settings.Port != nil {
		settings.Port = *raw.Settings.Port
	}
	if raw.Settings.DefaultURL != nil {
		settings.DefaultURL = strings.TrimSpace(*raw.Settings.DefaultURL)
	}
	if raw.Settings.AutoStart != nil {
		settings.AutoStart = *raw.Settings.AutoStart
	}
	if raw.Settings.SyncEnabled != nil {
		settings.SyncEnabled = *raw.Settings.SyncEnabled
	}
	if raw.Settings.Appearance != nil {
		settings.Appearance = strings.TrimSpace(*raw.Settings.Appearance)
	}
	if raw.Settings.OnboardingDone != nil {
		settings.OnboardingDone = *raw.Settings.OnboardingDone
	}
	return sanitizeSettings(settings)
}

// sanitizeSettings 把磁盘上的非法值回退为默认值，保证读取结果总是可用。
func sanitizeSettings(settings model.Settings) model.Settings {
	defaults := DefaultSettings()
	if settings.Port < model.MinPort || settings.Port > model.MaxPort {
		settings.Port = defaults.Port
	}
	raw := strings.TrimSpace(settings.DefaultURL)
	if !validHTTPURL(raw) {
		settings.DefaultURL = defaults.DefaultURL
	} else {
		settings.DefaultURL = raw
	}
	switch settings.Appearance {
	case "system", "light", "dark":
	default:
		settings.Appearance = defaults.Appearance
	}
	return settings
}

// SaveSettings 保存设置，保留已有的 AI 配置与功能开关。
func (s *Store) SaveSettings(settings model.Settings) error {
	if err := ValidateSettings(settings); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := settings
	return s.writeConfigSection("settings", next)
}

// LoadAIConfig 读取 AI 配置；缺失或损坏时返回零值。
func (s *Store) LoadAIConfig() model.AIConfig {
	cfg := s.parseFileConfig()
	if cfg.AI == nil {
		return model.AIConfig{}
	}
	return *cfg.AI
}

// SaveAIConfig 保存 AI 配置，保留设置与功能开关。
func (s *Store) SaveAIConfig(ai model.AIConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := ai
	return s.writeConfigSection("ai", next)
}

// LoadFeatures 读取功能开关；缺失或损坏时返回零值（全部关闭）。
func (s *Store) LoadFeatures() model.FeatureConfig {
	cfg := s.parseFileConfig()
	if cfg.Features == nil {
		return model.FeatureConfig{}
	}
	return *cfg.Features
}

// SaveFeatures 保存功能开关，保留设置与 AI 配置。
func (s *Store) SaveFeatures(features model.FeatureConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := features
	return s.writeConfigSection("features", next)
}

// HasConfigSection 报告 config.json 顶层是否存在指定段（"ai" / "features" 等）。
// 基于原始 JSON 键判断：文件缺失、损坏或键不存在都返回 false。
// 用户脚本据此按段覆盖自己的网页面板配置（QA M3）。
func (s *Store) HasConfigSection(section string) bool {
	section = strings.TrimSpace(section)
	if section == "" {
		return false
	}
	data := s.readRawConfig()
	if len(data) == 0 {
		return false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	_, ok := raw[section]
	return ok
}

// HasUserConfig 报告 config.json 中是否存在 "ai" 或 "features" 段。
// 只有 settings 或文件不存在时返回 false；解析失败同样返回 false。
func (s *Store) HasUserConfig() bool {
	return s.HasConfigSection("ai") || s.HasConfigSection("features")
}

// writeConfigSection 只更新 config.json 的一个顶层段，保留其他段与未知字段。
// 如果现有文件损坏：先备份，再拒绝覆盖并返回错误，避免一次保存把整份配置清空。
func (s *Store) writeConfigSection(section string, value any) error {
	section = strings.TrimSpace(section)
	if section == "" {
		return errors.New("配置段不能为空")
	}
	raw := map[string]json.RawMessage{}
	data, err := os.ReadFile(s.ConfigPath())
	switch {
	case err == nil && len(bytes.TrimSpace(data)) > 0:
		if unmarshalErr := json.Unmarshal(data, &raw); unmarshalErr != nil {
			// 损坏文件先留 .corrupt-* 备份，再按空配置继续写（备份可恢复，不会静默丢失）。
			s.backupCorruptConfig()
			raw = map[string]json.RawMessage{}
		}
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("读取配置失败: %w", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	// 覆盖前保留上一版（与 1.1.4 的「配置易丢失」修复意图一致）。
	if len(raw) > 0 {
		if previous, readErr := os.ReadFile(s.ConfigPath()); readErr == nil && len(bytes.TrimSpace(previous)) > 0 {
			_ = os.WriteFile(s.ConfigPath()+".bak", previous, 0o600)
		}
	}
	raw[section] = encoded
	// settings 落盘时顺便清掉旧 Node CLI 的扁平字段（迁移完成）。
	if section == "settings" {
		delete(raw, "url")
		delete(raw, "port")
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	out = append(out, '\n')
	if err := writeFileAtomic(s.ConfigPath(), out, 0o600); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	return nil
}

// LoadInstallState 读取脚本安装记录；缺失或损坏时返回零值。
//
// 兼容旧 Node CLI 的 state.json（{"openedHash": "...", "version": "...", "openedAt": ...}）：
// 新结构没有 hash 时把 openedHash 映射为 hash 并标记已安装（QA M4）。
func (s *Store) LoadInstallState() model.InstallState {
	data, err := os.ReadFile(s.StatePath())
	if err != nil || len(data) == 0 {
		return model.InstallState{}
	}
	var state model.InstallState
	if err := json.Unmarshal(data, &state); err != nil {
		return model.InstallState{}
	}
	if strings.TrimSpace(state.Hash) == "" {
		var legacy struct {
			OpenedHash string `json:"openedHash"`
		}
		if err := json.Unmarshal(data, &legacy); err == nil {
			if hash := strings.TrimSpace(legacy.OpenedHash); hash != "" {
				state.Hash = hash
				state.Installed = true
				if strings.TrimSpace(state.Note) == "" {
					state.Note = "旧版本安装记录（已迁移）"
				}
			}
		}
	}
	state.Hash = strings.TrimSpace(state.Hash)
	return state
}

// SaveInstallState 保存脚本安装记录。
func (s *Store) SaveInstallState(state model.InstallState) error {
	if err := writeJSONAtomic(s.StatePath(), state, 0o600); err != nil {
		return fmt.Errorf("写入安装记录失败: %w", err)
	}
	return nil
}

// LoadServerInfo 读取本地服务进程记录；文件缺失、损坏或字段不完整时返回 false。
func (s *Store) LoadServerInfo() (model.ServerInfo, bool) {
	var info model.ServerInfo
	if err := readJSON(s.ServerPath(), &info); err != nil {
		return model.ServerInfo{}, false
	}
	if info.PID <= 0 || info.Port <= 0 {
		return model.ServerInfo{}, false
	}
	return info, true
}

// SaveServerInfo 保存本地服务进程记录。
func (s *Store) SaveServerInfo(info model.ServerInfo) error {
	if err := writeJSONAtomic(s.ServerPath(), info, 0o600); err != nil {
		return fmt.Errorf("写入服务记录失败: %w", err)
	}
	return nil
}

// ClearServerInfo 删除本地服务进程记录；文件不存在不算错误。
func (s *Store) ClearServerInfo() error {
	if err := os.Remove(s.ServerPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("清理服务记录失败: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------- 内部工具

// readJSON 读取并解析 JSON 文件。
func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return errors.New("空文件")
	}
	return json.Unmarshal(data, target)
}

// writeJSONAtomic 以缩进 JSON + 换行原子写入文件。
func writeJSONAtomic(path string, value any, perm os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, perm)
}

// writeFileAtomic 通过同目录临时文件 + rename 原子替换目标文件。
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
