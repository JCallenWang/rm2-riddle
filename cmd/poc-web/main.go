// 本機預覽用 PoC:在 Mac 上直接起網頁設定介面,調整 index.html 的版面時
// 不必每次部署到裝置。裝置專屬的資料(筆記本清單、journalctl 日誌)在本機讀不到,
// 會顯示成空值,其餘互動與實機相同。
//
// 用法:go run ./cmd/poc-web  → 開 https://127.0.0.1:8443/(自簽憑證,需點過警告)
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"rm2scribe/internal/config"
	"rm2scribe/internal/web"
)

const sample = `[llm]
provider = "claude"
api_key  = "sk-preview-not-a-real-key"   # 預覽用假 key
model    = "claude-sonnet-5"
system_prompt = "You are an assistant living inside a handwritten notebook."
max_tokens = 500

[trigger]
mode = "idle_timeout"
idle_seconds = 8
notebook = "Riddle"

[animation]
write_speed = 1.0
font_size_px = 44
line_spacing = 1.5
llm_fadeout = 30
clear_mode = "region"

[capture]
method = "strokes"
`

func main() {
	dir := flag.String("dir", "", "設定檔目錄(預設:臨時目錄)")
	addr := flag.String("addr", "127.0.0.1:8443", "監聽位址")
	flag.Parse()

	d := *dir
	if d == "" {
		var err error
		if d, err = os.MkdirTemp("", "rm2-scribe-web"); err != nil {
			log.Fatal(err)
		}
	}
	path := filepath.Join(d, "config.toml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
			log.Fatal(err)
		}
	}

	cfg, err := config.Load(path)
	if err != nil {
		log.Fatal(err)
	}
	cfg.Web.Enabled = true
	cfg.Web.Listen = *addr

	if err := web.Start(web.Options{ConfigPath: path, Config: cfg}); err != nil {
		log.Fatalf("啟動失敗: %v", err)
	}
	log.Printf("設定檔:%s", path)
	select {}
}
