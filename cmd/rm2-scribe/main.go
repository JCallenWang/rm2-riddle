// rm2-scribe 主程式:監聽手寫 → 渲染 PNG → 送 LLM 辨識+回覆 →
// 以單線字型合成筆劃 → 注入 /dev/input/event1 讓 xochitl 逐劃寫出回覆。
//
// 互動流程:停筆逾時觸發 → 吸收(擦除)使用者手寫 → 送 LLM → 寫出回覆 →
// 經 llm_fadeout 秒後自動擦除回覆。限定於指定筆記本;關閉該筆記本時清除記錄與內容。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"rm2scribe/internal/config"
	"rm2scribe/internal/font"
	"rm2scribe/internal/input"
	"rm2scribe/internal/llm"
	"rm2scribe/internal/pen"
	"rm2scribe/internal/render"
	"rm2scribe/internal/web"
	"rm2scribe/internal/xochitl"
)

const device = "/dev/input/event1"

const (
	screenW  = 1404
	screenH  = 1872
	wacomXMx = 20966
	wacomYMx = 15725

	// 安全繪圖區:避開 xochitl 左側工具列(約 100px)與各邊緣,
	// 否則注入的筆劃會落在工具列上被當成點按 UI(導致切換工具、擦不掉字)。
	safeLeft   = 170
	safeRight  = screenW - 40
	safeTop    = 60
	safeBottom = screenH - 60
)

func fromWacom(wx, wy int32) (float64, float64) {
	sy := float64(screenH) - float64(wx)*float64(screenH)/float64(wacomXMx)
	sx := float64(wy) * float64(screenW) / float64(wacomYMx)
	return sx, sy
}

// injCtl 串行化所有注入(畫/擦),並在注入期間靜音 reader,
// 避免注入事件被讀回。所有筆操作都必須經過它。
type injCtl struct {
	dev    *pen.Device
	reader *input.Reader
	mu     sync.Mutex
	busy   atomic.Bool
}

// Busy 回報目前是否正在注入;網頁介面要求重新啟動時會先等它結束,
// 以免在落筆狀態下結束程式,讓 xochitl 卡在「筆還按著」。
func (c *injCtl) Busy() bool { return c.busy.Load() }

func (c *injCtl) draw(strokes [][]pen.Point, stepPx float64, dt, gap time.Duration) {
	c.run(func() {
		for _, st := range strokes {
			if len(st) < 2 {
				continue
			}
			if err := c.dev.DrawStroke(st, stepPx, dt); err != nil {
				log.Printf("注入(畫)失敗: %v", err)
				return
			}
			time.Sleep(gap)
		}
	})
}

func (c *injCtl) erase(strokes [][]pen.Point, stepPx float64, dt time.Duration) {
	c.run(func() {
		for _, st := range strokes {
			if len(st) < 2 {
				continue
			}
			if err := c.dev.EraseStroke(st, stepPx, dt); err != nil {
				log.Printf("注入(擦)失敗: %v", err)
				return
			}
		}
	})
}

func (c *injCtl) run(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.busy.Store(true)
	defer c.busy.Store(false)
	c.reader.Mute()
	fn()
	time.Sleep(300 * time.Millisecond) // 讓最後的注入事件流盡再解除靜音
	c.reader.Unmute()
}

func main() {
	cfgPath := flag.String("config", "/home/root/.config/rm2-scribe/config.toml", "設定檔路徑")
	once := flag.Bool("once", false, "只處理一次觸發即結束(除錯用)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("載入設定失敗: %v", err)
	}
	notebookDesc := fmt.Sprintf("%q", cfg.Trigger.Notebook)
	if cfg.Trigger.Notebook == "" {
		notebookDesc = "(未指定 → 停用)"
	}
	log.Printf("rm2-scribe 啟動:model=%s idle=%.0fs notebook=%s fadeout=%.0fs min_stroke=%.0fpx now=%s",
		cfg.LLM.Model, cfg.Trigger.IdleSeconds, notebookDesc, cfg.Animation.LLMFadeout,
		cfg.Trigger.MinStrokePx, time.Now().Format("2006-01-02 15:04 MST"))

	provider, err := llm.New(llm.Config{
		Provider:     cfg.LLM.Provider,
		APIKey:       cfg.LLM.APIKey,
		Model:        cfg.LLM.Model,
		SystemPrompt: cfg.LLM.SystemPrompt,
		MaxTokens:    cfg.LLM.MaxTokens,
	})
	if err != nil {
		log.Fatalf("建立 LLM provider 失敗: %v", err)
	}

	reader, err := input.NewReader(device, time.Duration(cfg.Trigger.IdleSeconds*float64(time.Second)))
	if err != nil {
		log.Fatalf("開啟輸入裝置失敗: %v", err)
	}
	defer reader.Close()
	reader.SetMinStrokePx(cfg.Trigger.MinStrokePx)

	dev, err := pen.OpenDirect(device)
	if err != nil {
		log.Fatalf("開啟注入端失敗: %v", err)
	}
	defer dev.Close()
	inj := &injCtl{dev: dev, reader: reader}

	// 網頁設定介面(config.toml 的 [web].enabled 決定);
	// 起不來只記錄不中止——手寫助理本身比設定介面重要。
	if err := web.Start(web.Options{ConfigPath: *cfgPath, Config: cfg, Busy: inj.Busy}); err != nil {
		log.Printf("網頁設定介面未啟動: %v", err)
	}

	stop := make(chan struct{})
	batches := make(chan input.Batch, 1)
	go func() {
		if err := reader.Run(stop, batches); err != nil {
			log.Printf("監聽結束: %v", err)
		}
	}()
	go watchNotebook(cfg, reader, stop)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	log.Printf("等待手寫(停筆 %.0f 秒後送出)…", cfg.Trigger.IdleSeconds)
	for {
		select {
		case <-sig:
			close(stop)
			log.Println("結束。")
			return
		case b := <-batches:
			handle(cfg, provider, inj, b)
			if *once {
				close(stop)
				return
			}
		}
	}
}

// watchNotebook 監看目前打開的筆記本;非指定筆記本時關閉閘門(不累積),
// 當「從指定筆記本切走/關閉」時,清除緩衝、暫存與筆記本內容。
//
// notebook 留空 = 停用:閘門永遠關著,任何筆記本都不偵測、不回應。
// (刻意不採「留空 = 全部筆記本」,避免在任何一本筆記上寫字都被吸走。)
func watchNotebook(cfg config.Config, reader *input.Reader, stop <-chan struct{}) {
	target := cfg.Trigger.Notebook
	if target == "" {
		reader.Gate(true)
		log.Printf("設定未指定筆記本(notebook 留空),服務停用——不會偵測任何筆記本")
		return
	}
	prevOpen := false
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			open := xochitl.CurrentName() == target
			reader.Gate(!open) // 非指定筆記本 → 閘門關閉,丟棄事件
			if !prevOpen && open {
				log.Printf("進入筆記本 %q,開始接收手寫", target)
			}
			if prevOpen && !open {
				log.Printf("偵測到離開/關閉筆記本 %q,清除記錄與內容", target)
				reader.ClearBuffer()
				clearRecords()
				go wipeNotebook(target)
			}
			prevOpen = open
		}
	}
}

func handle(cfg config.Config, provider llm.Provider, inj *injCtl, b input.Batch) {
	// 雙重確認:目前確實在指定筆記本(閘門已把關,這裡再防邊界情況)。
	// 未指定筆記本時服務停用,一律略過。
	if cfg.Trigger.Notebook == "" || xochitl.CurrentName() != cfg.Trigger.Notebook {
		log.Printf("非指定筆記本,略過此次觸發")
		return
	}

	n := 0
	for _, s := range b.Strokes {
		n += len(s)
	}
	if b.Dropped > 0 {
		log.Printf("擷取到 %d 筆劃 / %d 點(另丟棄 %d 筆點擊誤觸)", len(b.Strokes), n, b.Dropped)
	} else {
		log.Printf("擷取到 %d 筆劃 / %d 點", len(b.Strokes), n)
	}

	// 先從記憶體中的筆劃渲染 PNG(擦除頁面不影響此資料)
	tmp := filepath.Join("/home/root/rm2-scribe", "current.png")
	if err := render.StrokesToPNG(b.Strokes, tmp); err != nil {
		log.Printf("渲染失敗: %v", err)
		return
	}
	pngData, err := os.ReadFile(tmp)
	if err != nil {
		log.Printf("讀取 PNG 失敗: %v", err)
		return
	}

	// 吸收(fade out)使用者手寫,呈現「已被讀取」的互動——用密集掃描線完整清除
	userScreen := userStrokesScreen(b)
	log.Printf("吸收使用者手寫…")
	clearContent(inj, userScreen, cfg.Animation.ClearMode)

	// 呼叫 LLM
	log.Printf("送 LLM 辨識+回覆…")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	reply, err := provider.Recognize(ctx, pngData)
	cancel()
	if err != nil {
		log.Printf("LLM 失敗: %v", err)
		return
	}
	log.Printf("LLM 回覆: %q", reply)

	// 版面:回覆寫在使用者筆跡下方(此時已擦除,但保留原座標當定位參考)
	startY := replyStartY(b)
	respStrokes := font.Layout(reply, font.LayoutOpts{
		StartX:      safeLeft,
		StartY:      startY,
		FontPx:      cfg.Animation.FontSizePx,
		LineSpacing: cfg.Animation.LineSpacing,
		MaxX:        safeRight,
	})
	log.Printf("寫出回覆(%d 筆)…", len(respStrokes))
	dt := time.Duration(float64(4*time.Millisecond) / clampSpeed(cfg.Animation.WriteSpeed))
	inj.draw(respStrokes, 6, dt, 40*time.Millisecond)
	log.Printf("回覆已寫出。")

	// 經 llm_fadeout 秒後擦除回覆(仍在指定筆記本時才擦)
	if cfg.Animation.LLMFadeout > 0 {
		go func() {
			time.Sleep(time.Duration(cfg.Animation.LLMFadeout * float64(time.Second)))
			if cfg.Trigger.Notebook != "" && xochitl.CurrentName() != cfg.Trigger.Notebook {
				return // 已離開筆記本,交由關閉清除處理
			}
			log.Printf("llm_fadeout 到,擦除回覆")
			clearContent(inj, respStrokes, cfg.Animation.ClearMode)
		}()
	}
}

// clearContent 完整清除一組筆劃佔用的區域:在其範圍框(或整頁)內
// 以密集橫向掃描線用橡皮擦擦除,避免沿路徑走的殘留。
// mode="page" 時清整頁;否則(region)只清內容範圍框加邊界。
func clearContent(inj *injCtl, strokes [][]pen.Point, mode string) {
	var x0, y0, x1, y1 float64
	if mode == "page" {
		x0, y0, x1, y1 = 0, 0, screenW, screenH
	} else {
		bx0, by0, bx1, by1, ok := bbox(strokes)
		if !ok {
			return
		}
		const pad = 35
		x0, y0, x1, y1 = bx0-pad, by0-pad, bx1+pad, by1+pad
	}
	// 夾限在安全區內,避開工具列(否則擦除筆劃被當成點工具列)
	x0 = clampf(x0, safeLeft, safeRight)
	x1 = clampf(x1, safeLeft, safeRight)
	y0 = clampf(y0, safeTop, safeBottom)
	y1 = clampf(y1, safeTop, safeBottom)
	if x1-x0 < 2 || y1-y0 < 2 {
		return
	}

	// 以「單一連續蛇形筆劃」擦除整個區域:一次落筆、逐列交替方向掃完、一次提筆,
	// 全程不切換工具,從根本杜絕逐條掃描線切換造成的黑線競態。
	// 列距 4px 密集,確保連 LLM 的細單線字都能被覆蓋。
	const spacing = 4.0
	var path []pen.Point
	dir := 1.0
	rows := 0
	for y := y0; y <= y1; y += spacing {
		if dir > 0 {
			path = append(path, pen.Point{X: x0, Y: y}, pen.Point{X: x1, Y: y})
		} else {
			path = append(path, pen.Point{X: x1, Y: y}, pen.Point{X: x0, Y: y})
		}
		dir = -dir
		rows++
	}
	if len(path) < 2 {
		return
	}
	log.Printf("擦除區域 x[%.0f..%.0f] y[%.0f..%.0f] %d 列(單一連續筆劃)", x0, x1, y0, y1, rows)
	inj.erase([][]pen.Point{path}, 24, time.Millisecond)
}

// bbox 回傳一組筆劃的螢幕座標範圍框。
func bbox(strokes [][]pen.Point) (x0, y0, x1, y1 float64, ok bool) {
	x0, y0 = 1e18, 1e18
	x1, y1 = -1e18, -1e18
	for _, s := range strokes {
		for _, p := range s {
			if p.X < x0 {
				x0 = p.X
			}
			if p.Y < y0 {
				y0 = p.Y
			}
			if p.X > x1 {
				x1 = p.X
			}
			if p.Y > y1 {
				y1 = p.Y
			}
			ok = true
		}
	}
	return
}

func clampf(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// userStrokesScreen 把使用者筆劃(Wacom 座標)轉為螢幕座標的筆劃,供擦除。
func userStrokesScreen(b input.Batch) [][]pen.Point {
	var out [][]pen.Point
	for _, s := range b.Strokes {
		var ps []pen.Point
		for _, p := range s {
			sx, sy := fromWacom(p.X, p.Y)
			ps = append(ps, pen.Point{X: sx, Y: sy})
		}
		if len(ps) >= 2 {
			out = append(out, ps)
		}
	}
	return out
}

// replyStartY 取使用者筆跡最低點(螢幕 y 最大),回覆從其下方一段距離開始。
func replyStartY(b input.Batch) float64 {
	maxY := 0.0
	found := false
	for _, s := range b.Strokes {
		for _, p := range s {
			_, sy := fromWacom(p.X, p.Y)
			if sy > maxY {
				maxY = sy
			}
			found = true
		}
	}
	if !found {
		return 400
	}
	y := maxY + 120
	if y > screenH-200 {
		y = screenH - 200
	}
	return y
}

// clearRecords 清除偵測程式的暫存筆劃檔。
func clearRecords() {
	os.Remove("/home/root/rm2-scribe/current.png")
}

// wipeNotebook 完全清空指定筆記本內容:刪除其所有頁面 .rm 檔(保留頁面結構)。
// 延遲以等 xochitl 關閉時寫回完成;若期間又重新打開則放棄(避免與 xochitl 衝突)。
func wipeNotebook(name string) {
	time.Sleep(1500 * time.Millisecond)
	if xochitl.CurrentName() == name {
		return // 又被打開,略過
	}
	uuid := xochitl.UUIDByName(name)
	if uuid == "" {
		return
	}
	dir := xochitl.PageDir(uuid)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cnt := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".rm") {
			if os.Remove(filepath.Join(dir, e.Name())) == nil {
				cnt++
			}
		}
	}
	log.Printf("已清空筆記本 %q 內容(刪除 %d 個頁面筆劃檔)", name, cnt)
}

func clampSpeed(s float64) float64 {
	if s < 0.1 {
		return 0.1
	}
	if s > 10 {
		return 10
	}
	return s
}
