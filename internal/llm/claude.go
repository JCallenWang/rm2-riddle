// Package llm 呼叫遠端 LLM,將手寫 PNG 送去辨識並取得回覆。
//
// 為維持 armv7 + CGO_ENABLED=0 的零相依靜態編譯,直接用 net/http 打
// Anthropic Messages API,不引入官方 SDK(SDK 會帶入額外相依)。
// 端點/標頭/影像區塊格式依 Anthropic Messages API 規格。
package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Provider 抽象雙供應商;目前實作 Claude,保留 Gemini 介面。
type Provider interface {
	// Recognize 傳入手寫 PNG bytes,回傳 LLM 的文字回覆。
	Recognize(ctx context.Context, pngData []byte) (string, error)
}

// Config 由設定檔 [llm] 區塊帶入。
type Config struct {
	Provider     string
	APIKey       string
	Model        string
	SystemPrompt string
	MaxTokens    int
}

// New 依 provider 建立對應實作。
func New(c Config) (Provider, error) {
	switch c.Provider {
	case "", "claude":
		if c.Model == "" {
			c.Model = "claude-sonnet-5"
		}
		if c.MaxTokens == 0 {
			c.MaxTokens = 500
		}
		return &claude{cfg: c, http: &http.Client{Timeout: 60 * time.Second}}, nil
	case "gemini":
		return nil, fmt.Errorf("gemini 尚未實作(架構已保留)")
	default:
		return nil, fmt.Errorf("未知的 provider: %q", c.Provider)
	}
}

type claude struct {
	cfg  Config
	http *http.Client
}

// Anthropic Messages API 請求/回應結構(僅取用需要的欄位)。
type anthReq struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []anthMsg `json:"messages"`
}

type anthMsg struct {
	Role    string      `json:"role"`
	Content []anthBlock `json:"content"`
}

type anthBlock struct {
	Type   string      `json:"type"`
	Text   string      `json:"text,omitempty"`
	Source *anthSource `json:"source,omitempty"`
}

type anthSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *claude) Recognize(ctx context.Context, pngData []byte) (string, error) {
	if c.cfg.APIKey == "" {
		return "", fmt.Errorf("尚未設定 API key(config.toml 的 [llm].api_key 為空)")
	}

	body := anthReq{
		Model:     c.cfg.Model,
		MaxTokens: c.cfg.MaxTokens,
		System:    c.cfg.SystemPrompt,
		Messages: []anthMsg{{
			Role: "user",
			Content: []anthBlock{
				{
					Type: "image",
					Source: &anthSource{
						Type:      "base64",
						MediaType: "image/png",
						Data:      base64.StdEncoding.EncodeToString(pngData),
					},
				},
				{
					Type: "text",
					// 時間取自裝置系統時鐘(chronyd 走 NTP 自動校時),格式含時區縮寫,
					// 讓 LLM 知道這是哪個時區的時間,而不是憑空猜「現在」。
					Text: fmt.Sprintf("Current date and time: %s.\n\n"+
						"This image shows my handwriting on an e-ink notebook. "+
						"Read it, then respond to what I wrote. Reply in English only.",
						time.Now().Format("2006-01-02 15:04 (Mon) MST")),
				},
			},
		}},
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("呼叫 Anthropic API 失敗: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var ar anthResp
	if err := json.Unmarshal(raw, &ar); err != nil {
		return "", fmt.Errorf("解析回應失敗(HTTP %d): %s", resp.StatusCode, string(raw))
	}
	if ar.Error != nil {
		return "", fmt.Errorf("API 錯誤(HTTP %d)%s: %s", resp.StatusCode, ar.Error.Type, ar.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API 非 200(HTTP %d): %s", resp.StatusCode, string(raw))
	}

	var out string
	for _, b := range ar.Content {
		if b.Type == "text" {
			out += b.Text
		}
	}
	if out == "" {
		return "", fmt.Errorf("回應無文字內容(stop_reason=%s)", ar.StopReason)
	}
	return out, nil
}

// RecognizeFile 是方便測試的輔助:讀 PNG 檔後呼叫 Recognize。
func RecognizeFile(ctx context.Context, p Provider, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return p.Recognize(ctx, data)
}

// Ping 用一個最小的請求(max_tokens=1)驗證 API key 與 model 是否可用,
// 供設定介面的「測試 API key」使用;成功回傳 nil。
func Ping(ctx context.Context, c Config) error {
	if c.APIKey == "" {
		return fmt.Errorf("尚未填入 API key")
	}
	if c.Provider != "" && c.Provider != "claude" {
		return fmt.Errorf("目前只支援 provider=claude(收到 %q)", c.Provider)
	}
	if c.Model == "" {
		c.Model = "claude-sonnet-5"
	}

	buf, err := json.Marshal(anthReq{
		Model:     c.Model,
		MaxTokens: 1,
		Messages:  []anthMsg{{Role: "user", Content: []anthBlock{{Type: "text", Text: "ping"}}}},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("連線失敗: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var ar anthResp
	_ = json.Unmarshal(raw, &ar)
	if ar.Error != nil {
		return fmt.Errorf("%s: %s", ar.Error.Type, ar.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}
