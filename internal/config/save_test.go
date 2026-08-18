package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `# ===== rm2-scribe 設定 =====

[llm]
provider = "claude"
api_key  = "sk-old"          # ← 請填入你的 Anthropic API key
model    = "claude-sonnet-5"
max_tokens = 500

[trigger]
idle_seconds = 8             # 停筆 N 秒後送出
notebook = "Tom"
`

func writeSample(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSaveRoundTrip(t *testing.T) {
	p := writeSample(t)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	c.LLM.APIKey = "sk-new"
	c.Trigger.Notebook = "Riddle"
	c.Trigger.IdleSeconds = 12
	c.Web.Enabled = true
	c.Web.Listen = "0.0.0.0:8443"

	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.LLM.APIKey != "sk-new" || got.Trigger.Notebook != "Riddle" || got.Trigger.IdleSeconds != 12 {
		t.Errorf("值未寫入: %+v", got.LLM.APIKey)
	}
	if !got.Web.Enabled || got.Web.Listen != "0.0.0.0:8443" {
		t.Errorf("缺少的 [web] section 未追加: %+v", got.Web)
	}
	// 未改動的值必須保持原樣
	if got.LLM.Model != "claude-sonnet-5" || got.LLM.MaxTokens != 500 {
		t.Errorf("未改動的值走樣: %+v", got.LLM)
	}
}

func TestSavePreservesComments(t *testing.T) {
	p := writeSample(t)
	c, _ := Load(p)
	c.LLM.APIKey = "sk-new"
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(p)
	s := string(out)
	for _, want := range []string{
		"# ===== rm2-scribe 設定 =====",
		"← 請填入你的 Anthropic API key",
		"# 停筆 N 秒後送出",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("註解遺失: %q\n---\n%s", want, s)
		}
	}
	if strings.Contains(s, "sk-old") {
		t.Error("舊 api_key 仍留在檔案中")
	}
}

func TestSaveQuotesAndHash(t *testing.T) {
	p := writeSample(t)
	c, _ := Load(p)
	// 使用者的 prompt 很可能含引號、井號
	c.LLM.SystemPrompt = `Always call me "Master" #1`
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.LLM.SystemPrompt != `Always call me "Master" #1` {
		t.Errorf("含引號/井號的字串未正確 round-trip: %q", got.LLM.SystemPrompt)
	}
}

func TestSaveKeepsBackup(t *testing.T) {
	p := writeSample(t)
	c, _ := Load(p)
	c.LLM.APIKey = "sk-new"
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	bak, err := os.ReadFile(p + ".bak")
	if err != nil {
		t.Fatalf("未留下 .bak: %v", err)
	}
	if !strings.Contains(string(bak), "sk-old") {
		t.Error(".bak 不是原始內容")
	}
}

func TestSaveUnchangedIsByteIdentical(t *testing.T) {
	p := writeSample(t)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p)
	// 只有原檔沒有的 [web]/[capture] 等 key 會被追加,既有行必須一字不差
	for _, line := range strings.Split(string(before), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.Contains(string(after), line) {
			t.Errorf("未改動的行被重寫了:\n舊: %q", line)
		}
	}
}
