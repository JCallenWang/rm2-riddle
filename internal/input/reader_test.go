package input

import (
	"math"
	"testing"
	"time"
)

// TestExtent 檢查點擊誤觸與真實筆畫在範圍上的差距是否足以區分。
// 座標是 Wacom 單位(1 螢幕 px ≈ 11.2 單位)。
func TestExtent(t *testing.T) {
	cases := []struct {
		name string
		s    Stroke
		want float64
	}{
		{"空筆劃", Stroke{}, 0},
		{"單點(點一下就放開)", Stroke{{X: 5000, Y: 5000}}, 0},
		{"原地抖動的點擊", Stroke{{X: 5000, Y: 5000}, {X: 5003, Y: 4998}, {X: 5001, Y: 5000}}, 3.6}, // hypot(3,2)
		{"水平短畫 100 單位", Stroke{{X: 5000, Y: 5000}, {X: 5100, Y: 5000}}, 100},
		{"對角線 3-4-5", Stroke{{X: 0, Y: 0}, {X: 300, Y: 400}}, 500},
	}
	for _, c := range cases {
		if got := extent(c.s); math.Abs(got-c.want) > 1 {
			t.Errorf("%s: extent = %.1f, 期望 ≈ %.1f", c.name, got, c.want)
		}
	}
}

// TestMinStrokeFilter 走真實的事件路徑:餵進 evdev 事件,確認點擊被丟棄、
// 真實筆畫被保留,且被丟棄的點擊不會重置停筆倒數。
func TestMinStrokeFilter(t *testing.T) {
	r := &Reader{}
	r.SetMinStrokePx(4) // 門檻 = 4 px ≈ 44.8 Wacom 單位

	// 先寫一筆真的(範圍 500 單位),記下當時的 lastAt
	feedStroke(r, [][2]int32{{5000, 5000}, {5300, 5400}})
	if len(r.strokes) != 1 {
		t.Fatalf("真實筆畫應被保留,got %d 筆", len(r.strokes))
	}
	afterReal := r.lastAt

	// 再點一下(範圍 3 單位)——應該被丟棄,且 lastAt 不能被推後,
	// 否則想事情時碰一下螢幕就要重等一次 idle_seconds。
	time.Sleep(2 * time.Millisecond)
	feedStroke(r, [][2]int32{{8000, 8000}, {8003, 8001}})
	if len(r.strokes) != 1 {
		t.Errorf("點擊誤觸不該被記錄,strokes = %d", len(r.strokes))
	}
	if r.dropped != 1 {
		t.Errorf("dropped = %d, 期望 1", r.dropped)
	}
	if !r.lastAt.Equal(afterReal) {
		t.Errorf("被丟棄的點擊不該重置停筆倒數:lastAt 從 %v 變成 %v", afterReal, r.lastAt)
	}

	// 門檻 0 = 不過濾,同樣的點擊要被收下
	r2 := &Reader{}
	r2.SetMinStrokePx(0)
	feedStroke(r2, [][2]int32{{8000, 8000}, {8003, 8001}})
	if len(r2.strokes) != 1 || r2.dropped != 0 {
		t.Errorf("min_stroke_px=0 應不過濾,strokes = %d dropped = %d", len(r2.strokes), r2.dropped)
	}
}

// feedStroke 依真筆的事件序列送入一筆完整筆劃:BTN_TOUCH=1 → (ABS_X/Y + SYN)… → BTN_TOUCH=0。
func feedStroke(r *Reader, pts [][2]int32) {
	r.handle(ev(evKey, btnTouch, 1))
	for _, p := range pts {
		r.handle(ev(evAbs, absX, p[0]))
		r.handle(ev(evAbs, absY, p[1]))
		r.handle(ev(evSyn, synReport, 0))
	}
	r.handle(ev(evKey, btnTouch, 0))
}

// ev 組出一個 16-byte input_event(只有 type/code/value 有意義,時間戳不影響解析)。
func ev(typ, code uint16, val int32) [16]byte {
	var e [16]byte
	e[8] = byte(typ)
	e[9] = byte(typ >> 8)
	e[10] = byte(code)
	e[11] = byte(code >> 8)
	u := uint32(val)
	e[12] = byte(u)
	e[13] = byte(u >> 8)
	e[14] = byte(u >> 16)
	e[15] = byte(u >> 24)
	return e
}
