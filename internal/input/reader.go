// Package input 監聽真實 Wacom 筆事件(/dev/input/event1),
// 把筆劃依 BTN_TOUCH 分段累積,並在停筆逾時後回報一批筆劃。
package input

import (
	"encoding/binary"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// evdev 常數(與 internal/pen 對應)
const (
	evSyn = 0x00
	evKey = 0x01
	evAbs = 0x03

	synReport = 0
	btnTouch  = 0x14a

	absX        = 0x00
	absY        = 0x01
	absPressure = 0x18
)

// Point 為 Wacom 座標系中的取樣點(與 pen.toWacom 同座標系,便於反轉換)。
type Point struct {
	X, Y     int32
	Pressure int32
}

// Stroke 是一段連續落筆(BTN_TOUCH=1 到 =0)的點序列。
type Stroke []Point

// wacomPerPx 是 Wacom 軸單位對螢幕 px 的比例:
// X 軸 20966/1872、Y 軸 15725/1404,兩軸都約 11.2(與 main.go 的座標公式同源)。
const wacomPerPx = 11.2

// Reader 監聽筆事件並依停筆逾時聚合成批。
type Reader struct {
	f       *os.File
	idle    time.Duration
	mu      sync.Mutex // 保護以下累積狀態(read goroutine 與 tick 迴圈並行存取)
	strokes []Stroke
	cur     Stroke
	touch   bool
	curX    int32
	curY    int32
	curP    int32
	lastAt  time.Time
	preAt   time.Time // 本次落筆前的 lastAt;整筆被當成誤觸丟棄時還原,不讓誤觸重置停筆倒數
	dropped int       // 本批已丟棄的點擊筆劃數(隨批次一起回報後歸零)
	haveXY  bool
	muted   atomic.Bool
	gated   atomic.Bool // 閘門:非指定筆記本開啟時關閉,丟棄所有事件

	// minExtent 是「算數的筆劃」最小範圍(Wacom 單位):範圍框對角線小於它的
	// 筆劃視為點擊誤觸(點工具列、手掌輕碰)而丟棄。0 = 不過濾。
	minExtent float64
}

// SetMinStrokePx 設定點擊誤觸的過濾門檻(螢幕 px);0 = 不過濾。
// 需在 Run 之前呼叫。
func (r *Reader) SetMinStrokePx(px float64) {
	r.mu.Lock()
	r.minExtent = px * wacomPerPx
	r.mu.Unlock()
}

// Mute/Unmute 在我們自己注入回覆筆劃時暫停累積,避免注入的事件被讀回、
// 被當成新手寫而形成無限迴圈(reader 與 injector 共用同一個 event 節點)。
func (r *Reader) Mute()   { r.muted.Store(true) }
func (r *Reader) Unmute() { r.muted.Store(false) }

// Gate 關閉時(gated=true)丟棄所有事件——用於「只在指定筆記本開啟時才累積」。
func (r *Reader) Gate(closed bool) { r.gated.Store(closed) }

// ClearBuffer 清空目前累積的筆劃(關閉筆記本或切換時呼叫)。
func (r *Reader) ClearBuffer() {
	r.mu.Lock()
	r.strokes = nil
	r.cur = nil
	r.touch = false
	r.mu.Unlock()
}

// NewReader 開啟裝置(唯讀,不 grab,不干擾 xochitl)。
func NewReader(dev string, idle time.Duration) (*Reader, error) {
	f, err := os.Open(dev)
	if err != nil {
		return nil, err
	}
	return &Reader{f: f, idle: idle}, nil
}

func (r *Reader) Close() error { return r.f.Close() }

// Batch 是一次觸發回報的內容。
type Batch struct {
	Strokes []Stroke
	Dropped int // 這批期間被當成點擊誤觸而丟棄的筆劃數(供日誌診斷門檻是否過嚴)
}

// Run 阻塞讀取事件;每當「已有筆劃且停筆超過 idle」時,
// 透過 out 送出一批筆劃並清空累積。ctx 取消時結束。
// 為了停筆逾時判斷,讀取採用非阻塞 + 短輪詢。
func (r *Reader) Run(stop <-chan struct{}, out chan<- Batch) error {
	// 阻塞讀取的 goroutine 直接處理事件(在讀取源頭檢查 mute),
	// 讓被靜音期間注入的事件立即丟棄、不進緩衝,避免解除靜音後外洩。
	errc := make(chan error, 1)
	go func() {
		buf := make([]byte, 16)
		for {
			if _, err := readFull(r.f, buf); err != nil {
				errc <- err
				return
			}
			var ev [16]byte
			copy(ev[:], buf)
			// 我方注入期間(muted)或非指定筆記本(gated)一律丟棄,並保持狀態乾淨
			if r.muted.Load() || r.gated.Load() {
				r.mu.Lock()
				r.touch = false
				r.cur = nil
				r.mu.Unlock()
				continue
			}
			r.handle(ev)
		}
	}()

	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return nil
		case err := <-errc:
			return err
		case <-tick.C:
			if r.muted.Load() || r.gated.Load() {
				continue
			}
			r.mu.Lock()
			ready := !r.touch && len(r.strokes) > 0 && time.Since(r.lastAt) >= r.idle
			var batch Batch
			if ready {
				batch = Batch{Strokes: r.strokes, Dropped: r.dropped}
				r.strokes = nil
				r.dropped = 0
			}
			r.mu.Unlock()
			if ready {
				select {
				case out <- batch:
				case <-stop:
					return nil
				}
			}
		}
	}
}

func (r *Reader) handle(ev [16]byte) {
	typ := binary.LittleEndian.Uint16(ev[8:])
	code := binary.LittleEndian.Uint16(ev[10:])
	val := int32(binary.LittleEndian.Uint32(ev[12:]))

	r.mu.Lock()
	defer r.mu.Unlock()
	switch typ {
	case evAbs:
		switch code {
		case absX:
			r.curX = val
			r.haveXY = true
		case absY:
			r.curY = val
			r.haveXY = true
		case absPressure:
			r.curP = val
		}
	case evKey:
		if code == btnTouch {
			if val == 1 {
				r.touch = true
				r.cur = Stroke{}
				r.preAt = r.lastAt
			} else {
				r.touch = false
				if len(r.cur) > 0 {
					if extent(r.cur) >= r.minExtent {
						r.strokes = append(r.strokes, r.cur)
						r.lastAt = time.Now()
					} else {
						// 點擊誤觸(點工具列、手掌輕碰):不記錄,且把停筆倒數還原到
						// 落筆前——否則想事情時碰一下螢幕就要重等 idle_seconds。
						r.dropped++
						r.lastAt = r.preAt
					}
				}
				r.cur = nil
			}
		}
	case evSyn:
		if code == synReport && r.touch && r.haveXY {
			r.cur = append(r.cur, Point{X: r.curX, Y: r.curY, Pressure: r.curP})
			r.lastAt = time.Now()
		}
	}
}

// extent 回傳一筆筆劃範圍框的對角線長度(Wacom 單位)。
// 點擊誤觸幾乎為 0,真正寫出來的筆畫則遠大於門檻。
func extent(s Stroke) float64 {
	if len(s) < 2 {
		return 0
	}
	x0, y0 := s[0].X, s[0].Y
	x1, y1 := x0, y0
	for _, p := range s[1:] {
		if p.X < x0 {
			x0 = p.X
		}
		if p.X > x1 {
			x1 = p.X
		}
		if p.Y < y0 {
			y0 = p.Y
		}
		if p.Y > y1 {
			y1 = p.Y
		}
	}
	return math.Hypot(float64(x1-x0), float64(y1-y0))
}

func readFull(f *os.File, buf []byte) (int, error) {
	got := 0
	for got < len(buf) {
		n, err := f.Read(buf[got:])
		if err != nil {
			return got, err
		}
		got += n
	}
	return got, nil
}
