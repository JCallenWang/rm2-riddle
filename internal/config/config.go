// Package config 載入 /home/root/.config/rm2-scribe/config.toml。
// 為維持零相依,自行解析 TOML 的子集(section、key = value、字串/數字/布林)。
package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	LLM struct {
		Provider     string
		APIKey       string
		Model        string
		SystemPrompt string
		MaxTokens    int
	}
	Trigger struct {
		Mode        string
		IdleSeconds float64
		Notebook    string // 限定專屬筆記本的可見名稱;空 = 所有筆記本都觸發
	}
	Animation struct {
		WriteSpeed  float64
		FontSizePx  float64
		LineSpacing float64
		LLMFadeout  float64 // LLM 回應顯示幾秒後自動擦除;0 = 不消失
		ClearMode   string  // "region"=只清內容範圍框(快、乾淨) | "page"=整頁清除
	}
	Capture struct {
		Method string
	}
}

// Default 提供合理預設(對應 DEVELOPMENT.md 的決議)。
func Default() Config {
	var c Config
	c.LLM.Provider = "claude"
	c.LLM.Model = "claude-sonnet-5"
	c.LLM.SystemPrompt = "You are an assistant living inside a handwritten notebook. Reply in English, briefly and warmly."
	c.LLM.MaxTokens = 500
	c.Trigger.Mode = "idle_timeout"
	c.Trigger.IdleSeconds = 8
	c.Animation.WriteSpeed = 1.0
	c.Animation.FontSizePx = 44
	c.Animation.LineSpacing = 1.5
	c.Animation.LLMFadeout = 30
	c.Animation.ClearMode = "region"
	c.Capture.Method = "strokes"
	return c
}

// Load 讀取設定檔並覆蓋預設值;檔案不存在時回傳預設。
func Load(path string) (Config, error) {
	c := Default()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	defer f.Close()

	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// 去除行內註解(在引號外的 #)
		val = stripComment(val)
		val = strings.TrimSpace(val)
		sv := unquote(val)

		switch section {
		case "llm":
			switch key {
			case "provider":
				c.LLM.Provider = sv
			case "api_key":
				c.LLM.APIKey = sv
			case "model":
				c.LLM.Model = sv
			case "system_prompt":
				c.LLM.SystemPrompt = sv
			case "max_tokens":
				c.LLM.MaxTokens = atoi(val, c.LLM.MaxTokens)
			}
		case "trigger":
			switch key {
			case "mode":
				c.Trigger.Mode = sv
			case "idle_seconds":
				c.Trigger.IdleSeconds = atof(val, c.Trigger.IdleSeconds)
			case "notebook":
				c.Trigger.Notebook = sv
			}
		case "animation":
			switch key {
			case "write_speed":
				c.Animation.WriteSpeed = atof(val, c.Animation.WriteSpeed)
			case "font_size_px":
				c.Animation.FontSizePx = atof(val, c.Animation.FontSizePx)
			case "line_spacing":
				c.Animation.LineSpacing = atof(val, c.Animation.LineSpacing)
			case "llm_fadeout":
				c.Animation.LLMFadeout = atof(val, c.Animation.LLMFadeout)
			case "clear_mode":
				c.Animation.ClearMode = sv
			}
		case "capture":
			if key == "method" {
				c.Capture.Method = sv
			}
		}
	}
	return c, sc.Err()
}

func stripComment(s string) string {
	inQ := false
	for i, r := range s {
		if r == '"' {
			inQ = !inQ
		}
		if r == '#' && !inQ {
			return s[:i]
		}
	}
	return s
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func atoi(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(unquote(s))); err == nil {
		return n
	}
	return def
}

func atof(s string, def float64) float64 {
	if n, err := strconv.ParseFloat(strings.TrimSpace(unquote(s)), 64); err == nil {
		return n
	}
	return def
}
