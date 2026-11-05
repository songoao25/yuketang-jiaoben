package script

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/songoao25/yuketang-jiaoben/internal/model"
)

const sampleScript = `// ==UserScript==
// @name         测试脚本
// @namespace    http://tampermonkey.net/
// @version      1.2.3
// @description  用于测试的用户脚本
// @author       张三
// @match        *://*.yuketang.cn/*
// @match        *://*.gdufemooc.cn/*
// ==/UserScript==

(() => {
  console.log('hello');
})();
`

func TestFromSourceParsesHeader(t *testing.T) {
	meta, err := FromSource(sampleScript)
	if err != nil {
		t.Fatalf("FromSource 失败: %v", err)
	}
	if meta.Name != "测试脚本" {
		t.Fatalf("Name = %q, want 测试脚本", meta.Name)
	}
	if meta.Version != "1.2.3" {
		t.Fatalf("Version = %q, want 1.2.3", meta.Version)
	}
	if meta.Author != "张三" {
		t.Fatalf("Author = %q, want 张三", meta.Author)
	}
	wantMatches := []string{"*://*.yuketang.cn/*", "*://*.gdufemooc.cn/*"}
	if len(meta.Matches) != len(wantMatches) {
		t.Fatalf("Matches = %v, want %v", meta.Matches, wantMatches)
	}
	for i, want := range wantMatches {
		if meta.Matches[i] != want {
			t.Fatalf("Matches[%d] = %q, want %q", i, meta.Matches[i], want)
		}
	}

	sum := sha256.Sum256([]byte(sampleScript))
	wantHash := hex.EncodeToString(sum[:])
	if meta.Hash != wantHash {
		t.Fatalf("Hash = %q, want %q", meta.Hash, wantHash)
	}
	if meta.ShortHash != wantHash[:8] {
		t.Fatalf("ShortHash = %q, want %q", meta.ShortHash, wantHash[:8])
	}
	if len(meta.Hash) != 64 {
		t.Fatalf("sha256 指纹长度 = %d, want 64", len(meta.Hash))
	}
	if meta.Lines != strings.Count(sampleScript, "\n") {
		t.Fatalf("Lines = %d, want %d", meta.Lines, strings.Count(sampleScript, "\n"))
	}
}

func TestFromSourceLineCountingWithoutTrailingNewline(t *testing.T) {
	source := strings.TrimSuffix(sampleScript, "\n")
	meta, err := FromSource(source)
	if err != nil {
		t.Fatalf("FromSource 失败: %v", err)
	}
	if want := strings.Count(source, "\n") + 1; meta.Lines != want {
		t.Fatalf("Lines = %d, want %d", meta.Lines, want)
	}
}

func TestFromSourceMissingHeader(t *testing.T) {
	if _, err := FromSource("console.log('no header');\n"); err == nil {
		t.Fatal("缺少 UserScript 头应返回错误")
	}
	if _, err := FromSource(""); err == nil {
		t.Fatal("空脚本应返回错误")
	}
}

func TestFromSourceMissingVersion(t *testing.T) {
	source := "// ==UserScript==\n// @name 无版本\n// @match *://*/*\n// ==/UserScript==\n"
	if _, err := FromSource(source); err == nil {
		t.Fatal("缺少 @version 应返回错误")
	}
}

func TestFromSourceVersionOutsideHeaderIgnored(t *testing.T) {
	source := "// ==UserScript==\n// @name 无版本\n// ==/UserScript==\n// @version 9.9.9\n"
	if _, err := FromSource(source); err == nil {
		t.Fatal("头里的 @version 缺失时应返回错误")
	}
}

func TestFromSourceNameFallback(t *testing.T) {
	source := "// ==UserScript==\n// @version 1.0.0\n// ==/UserScript==\n"
	meta, err := FromSource(source)
	if err != nil {
		t.Fatalf("FromSource 失败: %v", err)
	}
	if meta.Name != defaultName {
		t.Fatalf("Name = %q, want %q", meta.Name, defaultName)
	}
}

func TestFromSourceHandlesCRLF(t *testing.T) {
	source := strings.ReplaceAll(sampleScript, "\n", "\r\n")
	meta, err := FromSource(source)
	if err != nil {
		t.Fatalf("FromSource(CRLF) 失败: %v", err)
	}
	if meta.Name != "测试脚本" {
		t.Fatalf("CRLF 下 Name = %q", meta.Name)
	}
	if meta.Version != "1.2.3" {
		t.Fatalf("CRLF 下 Version = %q", meta.Version)
	}
	if strings.ContainsAny(meta.Author, "\r\n") {
		t.Fatalf("Author 含换行符: %q", meta.Author)
	}
}

func TestFromSourceIgnoresBodyMetadata(t *testing.T) {
	source := "// ==UserScript==\n// @name 脚本\n// @version 1.0.0\n// ==/UserScript==\n" +
		"// @version 2.0.0\nconst x = '// @name 假名字';\n"
	meta, err := FromSource(source)
	if err != nil {
		t.Fatalf("FromSource 失败: %v", err)
	}
	if meta.Version != "1.0.0" {
		t.Fatalf("Version = %q, want 1.0.0", meta.Version)
	}
	if meta.Name != "脚本" {
		t.Fatalf("Name = %q, want 脚本", meta.Name)
	}
}

func TestShortHash(t *testing.T) {
	cases := map[string]string{
		"":                   "",
		"abc":                "abc",
		"12345678":           "12345678",
		"1234567890abcdef":   "12345678",
		"abcdef0123456789ff": "abcdef01",
	}
	for input, want := range cases {
		if got := ShortHash(input); got != want {
			t.Fatalf("ShortHash(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDescribe(t *testing.T) {
	meta, err := FromSource(sampleScript)
	if err != nil {
		t.Fatalf("FromSource 失败: %v", err)
	}
	description := Describe(meta)
	if !strings.Contains(description, "1.2.3") || !strings.Contains(description, meta.ShortHash) {
		t.Fatalf("Describe 输出不含版本或指纹: %q", description)
	}
	if got := Describe(model.ScriptMeta{Version: "1.0.0"}); got != "v1.0.0" {
		t.Fatalf("Describe(无指纹) = %q, want v1.0.0", got)
	}
}

// TestFromSourceRealScript 验证仓库内置的 yuketang.js 能被正确解析。
func TestFromSourceRealScript(t *testing.T) {
	data, err := os.ReadFile("../../yuketang.js")
	if err != nil {
		t.Skipf("仓库内置脚本不可用，跳过: %v", err)
	}
	meta, err := FromSource(string(data))
	if err != nil {
		t.Fatalf("解析真实脚本失败: %v", err)
	}
	if meta.Version == "" || meta.Name == "" {
		t.Fatalf("真实脚本元信息不完整: %+v", meta)
	}
	if len(meta.Hash) != 64 || len(meta.ShortHash) != 8 {
		t.Fatalf("真实脚本指纹异常: hash=%q short=%q", meta.Hash, meta.ShortHash)
	}
	if meta.Lines < 100 {
		t.Fatalf("真实脚本行数异常: %d", meta.Lines)
	}
	found := false
	for _, match := range meta.Matches {
		if strings.Contains(match, "yuketang.cn") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("真实脚本缺少 yuketang.cn 匹配规则: %v", meta.Matches)
	}
}
