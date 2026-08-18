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
		Notebook    string // 限定專屬筆記本的可見名稱;留空 = 停用服務(不偵測任何筆記本)
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
	Web struct {
		Enabled  bool
		Listen   string // 監聽位址;127.0.0.1 = 僅本機(需 SSH 轉發),0.0.0.0 = 區網可存取
		Password string // HTTP Basic 密碼(帳號固定 admin);對外綁定時必填
		CertFile string // 自備 TLS 憑證;留空 = 首次啟動自動產生自簽憑證
		KeyFile  string
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
	c.Trigger.Notebook = "Tom"
	c.Animation.WriteSpeed = 1.0
	c.Animation.FontSizePx = 44
	c.Animation.LineSpacing = 1.5
	c.Animation.LLMFadeout = 30
	c.Animation.ClearMode = "region"
	c.Capture.Method = "strokes"
	c.Web.Enabled = false
	c.Web.Listen = "127.0.0.1:8443"
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
		case "web":
			switch key {
			case "enabled":
				c.Web.Enabled = atob(val, c.Web.Enabled)
			case "listen":
				c.Web.Listen = sv
			case "password":
				c.Web.Password = sv
			case "cert_file":
				c.Web.CertFile = sv
			case "key_file":
				c.Web.KeyFile = sv
			}
		}
	}
	return c, sc.Err()
}

func stripComment(s string) string {
	if i := commentIndex(s); i >= 0 {
		return s[:i]
	}
	return s
}

// unquote 去掉外層雙引號並還原跳脫字元(與 save.go 的 quote 對稱)。
func unquote(s string) string {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	inner := s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) {
			i++
			switch inner[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(inner[i])
			}
			continue
		}
		b.WriteByte(inner[i])
	}
	return b.String()
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

func atob(s string, def bool) bool {
	if b, err := strconv.ParseBool(strings.TrimSpace(unquote(s))); err == nil {
		return b
	}
	return def
}
