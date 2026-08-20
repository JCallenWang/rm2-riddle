package font

import (
	"strings"
	"testing"
)

// TestHersheyDataParses 檢查內嵌的 .jhf 有被完整解析。資料檔是外部來源,
// 解析錯了不會編譯失敗,只會畫出一團亂——所以要對已知值做定錨。
func TestHersheyDataParses(t *testing.T) {
	if len(hershey) < 90 {
		t.Fatalf("字符數 %d 太少,.jhf 可能沒讀完整", len(hershey))
	}
	// 'n' 是實測過的定錨:bearing -8..10,起訖點都在連接高度 y=4
	g, ok := hersheyFor('n')
	if !ok {
		t.Fatal("找不到 'n'")
	}
	if g.lb != -8 || g.rb != 10 {
		t.Errorf("'n' 的 bearing = %v..%v,期望 -8..10", g.lb, g.rb)
	}
	first := g.strokes[0][0]
	last := g.strokes[len(g.strokes)-1]
	end := last[len(last)-1]
	if first.x != g.lb || first.y != 4 {
		t.Errorf("'n' 起筆 = (%v,%v),期望 (%v,4)", first.x, first.y, g.lb)
	}
	if end.x != g.rb || end.y != 4 {
		t.Errorf("'n' 收筆 = (%v,%v),期望 (%v,4)", end.x, end.y, g.rb)
	}
}

// TestHersheyVerticalBounds 檢查沒有字符超出宣告的 hTop..hDsc 範圍——
// 這組常數決定行距下限,量錯了上下行就會相碰。先前只量小寫,漏掉 [ ( { | # 這些
// 到 -16 的字符,就是這個測試抓出來的。
func TestHersheyVerticalBounds(t *testing.T) {
	for r, g := range hershey {
		for _, st := range g.strokes {
			for _, p := range st {
				if p.y < hTop || p.y > hDsc {
					t.Errorf("%q 有點超出 %.0f..%.0f: y=%v", r, hTop, hDsc, p.y)
				}
			}
		}
	}
}

// TestLinesNeverOverlap 是「句子維持固定行高」的保證:即使把 line_spacing
// 設得太小,排版也必須把行距夾到不相碰為止,而不是照做讓上下行穿插。
func TestLinesNeverOverlap(t *testing.T) {
	for _, spacing := range []float64{0.5, 1.0, 1.5, 1.7, 2.5} {
		lines, _ := LayoutCursive("gjpqy [#] foggy\nbdfhklt {|}\ngjpqy (again)", CursiveOpts{
			StartX: 170, StartY: 200, FontPx: 44, LineSpacing: spacing, MaxX: 1364,
		})
		if len(lines) != 3 {
			t.Fatalf("line_spacing=%.1f: 應為 3 行,got %d", spacing, len(lines))
		}
		for i := 1; i < len(lines); i++ {
			// 夾限會讓上下行剛好貼齊,浮點誤差可能差個零頭——留半像素容差
			if lines[i].Top < lines[i-1].Bottom-0.5 {
				t.Errorf("line_spacing=%.1f: 第 %d 行頂端 %.1f 侵入前一行底部 %.1f",
					spacing, i+1, lines[i].Top, lines[i-1].Bottom)
			}
		}
	}
}

// TestLineGrouping 檢查換行後每一行各自成組(逐行動畫要靠這個分界)。
func TestLineGrouping(t *testing.T) {
	lines, _ := LayoutCursive("one\ntwo\nthree", CursiveOpts{
		StartX: 0, StartY: 0, FontPx: 44, LineSpacing: 1.7, MaxX: 2000,
	})
	if len(lines) != 3 {
		t.Fatalf("應為 3 行,got %d", len(lines))
	}
	for i := 1; i < len(lines); i++ {
		if lines[i].Top <= lines[i-1].Top {
			t.Errorf("第 %d 行沒有往下排:Top %.1f <= 前一行 %.1f", i+1, lines[i].Top, lines[i-1].Top)
		}
	}
}

// TestJoinReducesStrokes 檢查相鄰字母有被併成連筆。這不只是美觀:
// 注入端每筆劃有約 23ms 的固定開銷,筆劃數直接決定一行要寫多久。
func TestJoinReducesStrokes(t *testing.T) {
	const word = "minimum" // 全是收在右 bearing、起於左 bearing 的字母
	lines, _ := LayoutCursive(word, CursiveOpts{
		StartX: 0, StartY: 0, FontPx: 44, LineSpacing: 1.7, MaxX: 2000,
	})
	got := len(lines[0].Strokes)

	// 未合併時的筆劃數 = 各字符的筆劃數總和
	raw := 0
	for _, r := range word {
		g, _ := hersheyFor(r)
		raw += len(g.strokes)
	}
	if got >= raw {
		t.Errorf("連筆沒有生效:合併後 %d 筆,未合併為 %d 筆", got, raw)
	}
}

// TestMaxYDropsAndReports 檢查放不下時會回報而不是無聲寫到畫面外。
func TestMaxYDropsAndReports(t *testing.T) {
	long := strings.Repeat("the quick brown fox jumps over a lazy dog ", 20)
	lines, dropped := LayoutCursive(long, CursiveOpts{
		StartX: 170, StartY: 60, FontPx: 44, LineSpacing: 1.7,
		MaxX: 1364, MaxY: 500,
	})
	if dropped == 0 {
		t.Error("內容遠超出 MaxY,dropped 應大於 0")
	}
	for _, ln := range lines {
		if ln.Bottom > 500+1 {
			t.Errorf("有行超出 MaxY:Bottom=%.1f", ln.Bottom)
		}
	}
}

// TestUnknownRuneDoesNotPanic 確保非 ASCII(例如中文)不會讓排版炸掉,
// 只是不畫出來——LLM 偶爾會回非英文字元。
func TestUnknownRuneDoesNotPanic(t *testing.T) {
	lines, _ := LayoutCursive("hello 中文 world", CursiveOpts{
		StartX: 0, StartY: 0, FontPx: 44, LineSpacing: 1.7, MaxX: 2000,
	})
	if len(lines) == 0 {
		t.Error("應該還是要排出英文的部分")
	}
}
