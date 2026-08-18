// Package web 提供設定用的網頁介面:在裝置上開一個小的 HTTPS 服務,
// 讓同網段的瀏覽器(手機也可以)直接編輯 /home/root/.config/rm2-scribe/config.toml
// 並重新啟動套用,不必 SSH 進去改檔。
//
// 安全性設計:
//   - 設定檔裡有 API key,所以對外綁定時強制要求密碼(HTTP Basic,帳號 admin),
//     未設密碼就拒絕啟動;綁 127.0.0.1 時可免密碼(存取者已經要有 SSH 權限)。
//   - 一律走 TLS;沒有自備憑證就自動產生自簽憑證(見 cert.go)。
//   - API key 永遠不回傳給瀏覽器,表單留空 = 維持原值。
//
// 依然零外部相依:只用標準庫,維持 armv7 + CGO_ENABLED=0 靜態編譯。
package web

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"rm2scribe/internal/config"
	"rm2scribe/internal/llm"
	"rm2scribe/internal/xochitl"
)

const (
	unitName = "rm2-scribe"
	authUser = "admin"
)

//go:embed index.html
var indexHTML []byte

// Options 由主程式提供。
type Options struct {
	ConfigPath string
	Config     config.Config
	// Busy 回報目前是否正在注入筆劃;重新啟動會等它結束,
	// 避免在落筆狀態下結束程式,讓 xochitl 卡在「筆還按著」。
	Busy func() bool
}

type server struct {
	cfgPath string
	busy    func() bool
	started time.Time
}

// Start 啟動網頁設定介面(enabled=false 時什麼都不做)。
// 服務在背景 goroutine 中執行,不阻塞呼叫端。
func Start(o Options) error {
	if !o.Config.Web.Enabled {
		return nil
	}
	listen := o.Config.Web.Listen
	if listen == "" {
		listen = "127.0.0.1:8443"
	}
	if _, _, err := net.SplitHostPort(listen); err != nil {
		return fmt.Errorf("web.listen 格式錯誤 %q(需要 host:port): %w", listen, err)
	}
	if !loopbackOnly(listen) && o.Config.Web.Password == "" {
		return fmt.Errorf("web.listen=%q 會對區網開放,但 web.password 是空的;請先設定密碼", listen)
	}

	certFile, keyFile := o.Config.Web.CertFile, o.Config.Web.KeyFile
	if certFile == "" || keyFile == "" {
		dir := filepath.Dir(o.ConfigPath)
		certFile = filepath.Join(dir, "web-cert.pem")
		keyFile = filepath.Join(dir, "web-key.pem")
		if err := ensureCert(certFile, keyFile); err != nil {
			return fmt.Errorf("產生自簽憑證失敗: %w", err)
		}
	}

	s := &server{cfgPath: o.ConfigPath, busy: o.Busy, started: time.Now()}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/config", s.handleSaveConfig)
	mux.HandleFunc("/api/test-key", s.handleTestKey)
	mux.HandleFunc("/api/service", s.handleService)
	mux.HandleFunc("/api/logs", s.handleLogs)

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("監聽 %s 失敗: %w", listen, err)
	}
	srv := &http.Server{
		Handler:           s.guard(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.ServeTLS(ln, certFile, keyFile); err != nil && err != http.ErrServerClosed {
			log.Printf("網頁設定介面結束: %v", err)
		}
	}()
	log.Printf("網頁設定介面:https://%s/(帳號 %s;自簽憑證,瀏覽器會先跳安全警告)",
		displayAddr(listen), authUser)
	return nil
}

// guard 同時做認證與跨站保護。
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		cfg, _ := config.Load(s.cfgPath)
		if cfg.Web.Password != "" {
			u, p, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(authUser)) != 1 ||
				subtle.ConstantTimeCompare([]byte(p), []byte(cfg.Web.Password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="rm2-scribe"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		// 所有寫入操作都要求 application/json:瀏覽器對跨站的 JSON POST 會先送
		// preflight,而我們不回任何 CORS 標頭 → 擋掉「別的網頁偷偷送表單」。
		if r.Method == http.MethodPost &&
			!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "需要 Content-Type: application/json", http.StatusUnsupportedMediaType)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

// cfgJSON 是給瀏覽器看的設定檢視:不含 api_key 與網頁密碼。
type cfgJSON struct {
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	SystemPrompt string  `json:"system_prompt"`
	MaxTokens    int     `json:"max_tokens"`
	IdleSeconds  float64 `json:"idle_seconds"`
	Notebook     string  `json:"notebook"`
	WriteSpeed   float64 `json:"write_speed"`
	FontSizePx   float64 `json:"font_size_px"`
	LineSpacing  float64 `json:"line_spacing"`
	LLMFadeout   float64 `json:"llm_fadeout"`
	ClearMode    string  `json:"clear_mode"`
	WebEnabled   bool    `json:"web_enabled"`
	WebListen    string  `json:"web_listen"`
}

func toJSON(c config.Config) cfgJSON {
	return cfgJSON{
		Provider:     c.LLM.Provider,
		Model:        c.LLM.Model,
		SystemPrompt: c.LLM.SystemPrompt,
		MaxTokens:    c.LLM.MaxTokens,
		IdleSeconds:  c.Trigger.IdleSeconds,
		Notebook:     c.Trigger.Notebook,
		WriteSpeed:   c.Animation.WriteSpeed,
		FontSizePx:   c.Animation.FontSizePx,
		LineSpacing:  c.Animation.LineSpacing,
		LLMFadeout:   c.Animation.LLMFadeout,
		ClearMode:    c.Animation.ClearMode,
		WebEnabled:   c.Web.Enabled,
		WebListen:    c.Web.Listen,
	}
}

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(s.cfgPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	current := xochitl.CurrentName()
	writeJSON(w, http.StatusOK, map[string]any{
		"config":           toJSON(cfg),
		"has_key":          cfg.LLM.APIKey != "",
		"has_web_password": cfg.Web.Password != "",
		"notebooks":        xochitl.ListNotebooks(),
		"current_notebook": current,
		"gate_open":        cfg.Trigger.Notebook == "" || current == cfg.Trigger.Notebook,
		"pid":              os.Getpid(),
		"uptime":           time.Since(s.started).Round(time.Second).String(),
		"busy":             s.busy != nil && s.busy(),
	})
}

type saveReq struct {
	cfgJSON
	APIKey      string `json:"api_key"`      // 留空 = 不變
	WebPassword string `json:"web_password"` // 留空 = 不變
	Apply       bool   `json:"apply"`        // true = 存檔後重新啟動套用
}

func (s *server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只接受 POST", http.StatusMethodNotAllowed)
		return
	}
	var req saveReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON 解析失敗: " + err.Error()})
		return
	}
	cfg, err := config.Load(s.cfgPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	cfg.LLM.Provider = req.Provider
	cfg.LLM.Model = req.Model
	cfg.LLM.SystemPrompt = req.SystemPrompt
	cfg.LLM.MaxTokens = req.MaxTokens
	cfg.Trigger.IdleSeconds = req.IdleSeconds
	cfg.Trigger.Notebook = req.Notebook
	cfg.Animation.WriteSpeed = req.WriteSpeed
	cfg.Animation.FontSizePx = req.FontSizePx
	cfg.Animation.LineSpacing = req.LineSpacing
	cfg.Animation.LLMFadeout = req.LLMFadeout
	cfg.Animation.ClearMode = req.ClearMode
	cfg.Web.Enabled = req.WebEnabled
	cfg.Web.Listen = req.WebListen
	if req.APIKey != "" {
		cfg.LLM.APIKey = req.APIKey
	}
	if req.WebPassword != "" {
		cfg.Web.Password = req.WebPassword
	}

	if err := validate(cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := config.Save(s.cfgPath, cfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "寫入設定失敗: " + err.Error()})
		return
	}
	log.Printf("設定已由網頁介面更新")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "applying": req.Apply})
	if req.Apply {
		s.restart()
	}
}

// validate 擋掉會讓程式起不來或行為明顯錯誤的設定。
func validate(c config.Config) error {
	if c.LLM.Provider != "claude" && c.LLM.Provider != "" {
		return fmt.Errorf("provider 目前只支援 claude(gemini 尚未實作)")
	}
	if c.LLM.Model == "" {
		return fmt.Errorf("model 不可為空")
	}
	if c.LLM.MaxTokens < 1 || c.LLM.MaxTokens > 8192 {
		return fmt.Errorf("max_tokens 需在 1–8192 之間")
	}
	if c.Trigger.IdleSeconds < 1 || c.Trigger.IdleSeconds > 300 {
		return fmt.Errorf("idle_seconds 需在 1–300 之間")
	}
	if c.Animation.WriteSpeed <= 0 || c.Animation.WriteSpeed > 10 {
		return fmt.Errorf("write_speed 需大於 0 且不超過 10")
	}
	if c.Animation.FontSizePx < 10 || c.Animation.FontSizePx > 200 {
		return fmt.Errorf("font_size_px 需在 10–200 之間")
	}
	if c.Animation.LineSpacing < 0.5 || c.Animation.LineSpacing > 5 {
		return fmt.Errorf("line_spacing 需在 0.5–5 之間")
	}
	if c.Animation.LLMFadeout < 0 || c.Animation.LLMFadeout > 3600 {
		return fmt.Errorf("llm_fadeout 需在 0–3600 之間")
	}
	if c.Animation.ClearMode != "region" && c.Animation.ClearMode != "page" {
		return fmt.Errorf("clear_mode 只能是 region 或 page")
	}
	if c.Web.Enabled {
		if _, _, err := net.SplitHostPort(c.Web.Listen); err != nil {
			return fmt.Errorf("web listen 需為 host:port 格式")
		}
		if !loopbackOnly(c.Web.Listen) && c.Web.Password == "" {
			return fmt.Errorf("對區網開放時必須設定網頁密碼")
		}
	}
	return nil
}

func (s *server) handleTestKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只接受 POST", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		APIKey string `json:"api_key"`
		Model  string `json:"model"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)

	cfg, _ := config.Load(s.cfgPath)
	key := req.APIKey
	if key == "" {
		key = cfg.LLM.APIKey
	}
	model := req.Model
	if model == "" {
		model = cfg.LLM.Model
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err := llm.Ping(ctx, llm.Config{Provider: cfg.LLM.Provider, APIKey: key, Model: model}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只接受 POST", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	switch req.Action {
	case "restart":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "重新啟動中…"})
		s.restart()
	case "stop":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已停止;要再啟動需 SSH 執行 systemctl start rm2-scribe"})
		go func() {
			time.Sleep(500 * time.Millisecond)
			log.Printf("依網頁要求停止服務")
			_ = exec.Command("systemctl", "stop", unitName).Run()
		}()
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "action 只能是 restart 或 stop"})
	}
}

func (s *server) handleLogs(w http.ResponseWriter, r *http.Request) {
	n := 80
	if v := r.URL.Query().Get("n"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			n = p
		}
	}
	if n < 10 {
		n = 10
	}
	if n > 500 {
		n = 500
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "journalctl", "-u", unitName,
		"-n", strconv.Itoa(n), "--no-pager").CombinedOutput()
	if err != nil && len(out) == 0 {
		out = []byte("讀取日誌失敗: " + err.Error())
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(out)
}

// restart 讓程式結束,由 systemd 的 Restart=always 帶著新設定重新啟動。
// 這比在執行中熱重載簡單可靠(LLM client、監聽逾時等都會重新初始化)。
func (s *server) restart() {
	go func() {
		time.Sleep(400 * time.Millisecond) // 先讓 HTTP 回應送出去
		// 正在注入筆劃時不能直接結束,否則筆會停在「按下」狀態
		deadline := time.Now().Add(60 * time.Second)
		for s.busy != nil && s.busy() && time.Now().Before(deadline) {
			time.Sleep(200 * time.Millisecond)
		}
		log.Printf("依網頁要求重新啟動(systemd 會自動拉起)")
		os.Exit(0)
	}()
}

// loopbackOnly 判斷這個監聽位址是否只有本機能連。
func loopbackOnly(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	switch host {
	case "", "0.0.0.0", "::", "*":
		return false
	case "localhost":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// displayAddr 把 0.0.0.0 這種綁定顯示成實際可連的位址,方便日誌直接複製。
func displayAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		// mDNS 名稱(remarkable.local)不是每個網路都查得到 A 記錄——實測有的環境
		// 只回一個失效的 IPv6 link-local,瀏覽器會直接 unreachable。
		// 印實際的區網 IP 比較可靠,可以直接複製貼上。
		for _, ip := range localIPs() {
			if !ip.IsLoopback() {
				return net.JoinHostPort(ip.String(), port)
			}
		}
		return net.JoinHostPort("remarkable.local", port)
	}
	return listen
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
