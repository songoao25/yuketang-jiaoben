// Package script 解析内置用户脚本的元信息（UserScript 头、sha256 指纹、行数）。
package script

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/songoao25/yuketang-jiaoben/internal/model"
)

const (
	headerStart = "// ==UserScript=="
	headerEnd   = "// ==/UserScript=="
	metaPrefix  = "// @"
	defaultName = "雨课助手"
)

// FromSource 从用户脚本源码解析元信息，并计算指纹与行数。
// 缺少 ==UserScript== 头或 @version 时返回错误。
func FromSource(source string) (model.ScriptMeta, error) {
	lines := splitLines(source)
	if len(lines) == 0 {
		return model.ScriptMeta{}, errors.New("脚本内容为空，缺少 ==UserScript== 头")
	}

	start, end := -1, len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if start < 0 {
			if trimmed == headerStart {
				start = i
			}
			continue
		}
		if trimmed == headerEnd {
			end = i
			break
		}
	}
	if start < 0 {
		return model.ScriptMeta{}, errors.New("脚本缺少 ==UserScript== 头")
	}

	meta := model.ScriptMeta{}
	for _, line := range lines[start+1 : end] {
		key, value, ok := parseMetaLine(line)
		if !ok {
			continue
		}
		switch key {
		case "name":
			if meta.Name == "" {
				meta.Name = value
			}
		case "version":
			if meta.Version == "" {
				meta.Version = value
			}
		case "author":
			if meta.Author == "" {
				meta.Author = value
			}
		case "match":
			if value != "" {
				meta.Matches = append(meta.Matches, value)
			}
		}
	}
	if meta.Version == "" {
		return model.ScriptMeta{}, errors.New("脚本头缺少 @version")
	}
	if meta.Name == "" {
		meta.Name = defaultName
	}

	hash := fingerprint(source)
	meta.Hash = hash
	meta.ShortHash = ShortHash(hash)
	meta.Lines = countLines(source)
	return meta, nil
}

// ShortHash 返回指纹前 8 位；不足 8 位时原样返回。
func ShortHash(hash string) string {
	if len(hash) <= 8 {
		return hash
	}
	return hash[:8]
}

// parseMetaLine 解析一行脚本头元数据，例如 "// @name 雨课助手"。
func parseMetaLine(line string) (key, value string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, metaPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(trimmed, metaPrefix)
	if rest == "" {
		return "", "", false
	}
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		key = rest[:i]
		value = strings.TrimSpace(rest[i+1:])
	} else {
		key = rest
	}
	if key == "" {
		return "", "", false
	}
	return strings.ToLower(key), value, true
}

// splitLines 按 \n 切分并去掉行尾 \r（兼容 CRLF）。
func splitLines(source string) []string {
	raw := strings.Split(source, "\n")
	lines := make([]string, len(raw))
	for i, line := range raw {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

// countLines 统计逻辑行数：末尾没有换行符时补一行，空内容为 0 行。
func countLines(source string) int {
	if source == "" {
		return 0
	}
	n := strings.Count(source, "\n")
	if !strings.HasSuffix(source, "\n") {
		n++
	}
	return n
}

// fingerprint 计算源码的 sha256 十六进制指纹。
func fingerprint(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

// Describe 返回便于日志/CLI 展示的简短描述，例如 "v3.0.7 · 指纹 a1b2c3d4"。
func Describe(meta model.ScriptMeta) string {
	version := meta.Version
	if version == "" {
		version = "未知版本"
	}
	short := meta.ShortHash
	if short == "" {
		short = ShortHash(meta.Hash)
	}
	if short == "" {
		return fmt.Sprintf("v%s", version)
	}
	return fmt.Sprintf("v%s · 指纹 %s", version, short)
}
