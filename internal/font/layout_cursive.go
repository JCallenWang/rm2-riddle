package font

import (
	"math"

	"rm2scribe/internal/pen"
)

// Line 是排版後的一行,Strokes 依書寫順序排列。
// Top/Bottom 是這行實際佔用的垂直範圍(含上伸部與下伸部),供逐行擦除使用。
type Line struct {
	Strokes [][]pen.Point
	Top     float64
	Bottom  float64
}

// CursiveOpts 控制草寫排版。座標單位為螢幕像素。
type CursiveOpts struct {
	StartX, StartY float64 // 第一行的左上角(StartY 為上伸部頂,不是基線)
	FontPx         float64 // 上伸部頂到基線的高度
	LineSpacing    float64 // 行距倍率(相對 FontPx)
	MaxX           float64 // 換行右界
	MaxY           float64 // 可用下界;0 = 不限。超過就不再排,由 dropped 回報
	LetterSpacing  float64 // 字母之間額外拉開的距離(px);0 = 用字型原本的字距
}

// LayoutCursive 把文字排版成「一行一組筆劃」。
//
// 第二個回傳值是因為超出 MaxY 而沒排進去的字詞數;呼叫端應該把它記進日誌,
// 否則內容會無聲地被截掉。
func LayoutCursive(text string, o CursiveOpts) (lines []Line, dropped int) {
	scale := o.FontPx / (hBase - hAsc)
	space := o.FontPx * o.LineSpacing
	if space == 0 {
		space = o.FontPx * 1.5
	}
	// 行距下限:FontPx 量的是「字母上伸部頂到基線」(21 單位),但字符實際佔到
	// hTop..hDsc(共 37 單位),所以 line_spacing 低於 37/21≈1.76 時上下行會相碰。
	// 夾住而不是照做——「句子維持固定行高」本來就要求行與行不互相穿插。
	if minSpace := (hDsc - hTop) * scale; space < minSpace {
		space = minSpace
	}

	track := o.LetterSpacing

	var cur Line
	var poly []pen.Point    // 目前正在連筆的折線
	var marks [][]pen.Point // 待補的附加筆劃(i j 的點、t 的橫槓),寫完單字才補
	// 行首右移 hLeftBleed:j f p 的下伸迴圈會往左甩出自己的前進寬度外,
	// 不留這段的話行首那個字會畫到 StartX 左邊。
	startX := o.StartX + hLeftBleed*scale
	penX, penY := startX, o.StartY

	// sp 把字符格線座標換算成螢幕座標。lb 讓字符的左 bearing 對齊目前的筆位置;
	// 垂直方向以上伸部頂(hAsc)為 0,所以 StartY 就是這一行的最高點。
	sp := func(g pt, lb float64) pen.Point {
		return pen.Point{
			X: penX + (g.x-lb)*scale,
			Y: penY + (g.y-hAsc)*scale,
		}
	}

	// emit 收下一條筆劃。單點(句點之類)要撐成一小段,否則注入端會拒收、
	// 句點就不見了。
	emit := func(ps []pen.Point) {
		switch {
		case len(ps) >= 2:
			cur.Strokes = append(cur.Strokes, ps)
		case len(ps) == 1:
			cur.Strokes = append(cur.Strokes, []pen.Point{ps[0], {X: ps[0].X + 1, Y: ps[0].Y + 1}})
		}
	}
	flush := func() {
		emit(poly)
		poly = nil
	}
	// dropMarks 補上 i 的點、t 的橫槓這類附加筆劃。刻意等到單字寫完才補,
	// 夾在字身中間會把連筆鏈打斷。
	dropMarks := func() {
		for _, m := range marks {
			emit(m)
		}
		marks = nil
	}
	endLine := func() {
		flush()
		dropMarks()
		if len(cur.Strokes) > 0 {
			cur.Top, cur.Bottom = strokesBounds(cur.Strokes)
			lines = append(lines, cur)
		}
		cur = Line{}
	}

	overflow := false
	newline := func() {
		endLine()
		penX = startX
		penY += space
		if o.MaxY > 0 && penY+(hDsc-hAsc)*scale > o.MaxY {
			overflow = true
		}
	}

	for _, tok := range splitKeepSpaces(text) {
		if overflow {
			if tok != " " {
				dropped++
			}
			continue
		}
		switch tok {
		case "\n":
			newline()
			continue
		case " ":
			flush() // 單字之間提筆
			dropMarks()
			penX += cursiveWordWidth(" ", scale) + track
			continue
		}
		// 放不下就換行(整個單字一起搬,避免拆字)
		if w := cursiveWordWidth(tok, scale) + track*float64(len([]rune(tok))); penX+w > o.MaxX && penX > startX {
			newline()
			if overflow {
				dropped++
				continue
			}
		}
		for _, r := range tok {
			g, _ := hersheyFor(r)
			toScreen := func(st []pt) []pen.Point {
				ps := make([]pen.Point, 0, len(st))
				for _, q := range st {
					ps = append(ps, sp(q, g.lb))
				}
				return ps
			}
			if !g.joins {
				flush() // 大寫、數字、標點不走連筆慣例
			}
			for _, st := range g.body {
				ps := toScreen(st)
				// 只在「完全重合」時併筆——字型讓相鄰字母在 (rb,4)=(lb,4) 這個點
				// 交會,兩邊算出來的螢幕座標是同一個值,所以合得起來的本來就該合。
				// 不用容差去猜:容差一放寬,n m 字幹內部本來要提筆的地方會被硬接成
				// 一道回頭的斜線,小字級下就糊成一團。
				if len(poly) > 0 && len(ps) > 0 && coincide(poly[len(poly)-1], ps[0]) {
					poly = append(poly, ps[1:]...)
					continue
				}
				flush()
				poly = ps
			}
			for _, st := range g.marks {
				marks = append(marks, toScreen(st))
			}
			penX += (g.rb-g.lb)*scale + track
		}
	}
	endLine()
	return lines, dropped
}

// coincide 判斷兩點是不是同一點。容差刻意訂得極小(遠小於一個像素):
// 相接的字母兩邊算出來的是同一個算式,只需要吸收浮點誤差,不是拿來猜「夠不夠近」。
func coincide(a, b pen.Point) bool {
	const eps = 1e-6
	return math.Abs(a.X-b.X) < eps && math.Abs(a.Y-b.Y) < eps
}

// cursiveWordWidth 估算一個 token 的像素寬度,用來決定要不要換行。
func cursiveWordWidth(word string, scale float64) float64 {
	var w float64
	for _, r := range word {
		g, _ := hersheyFor(r)
		w += (g.rb - g.lb) * scale
	}
	return w
}

// strokesBounds 回傳一組筆劃的垂直範圍。
func strokesBounds(strokes [][]pen.Point) (top, bottom float64) {
	top, bottom = 1e18, -1e18
	for _, s := range strokes {
		for _, p := range s {
			if p.Y < top {
				top = p.Y
			}
			if p.Y > bottom {
				bottom = p.Y
			}
		}
	}
	return
}
