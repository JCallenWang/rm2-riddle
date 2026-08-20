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

// joinTol 是「上一個字母的收筆點」與「下一個字母的起筆點」要多接近才併成一筆
// (單位:Hershey 格線)。這套字型的小寫有 24/26 收在右 bearing、16/26 起於
// 左 bearing 且都在 y=4,所以多數相鄰字母會自然接上,併筆後既是草寫的連接筆畫,
// 也把筆劃數壓下來——注入端每筆劃有約 23ms 的固定開銷,筆劃數就是一行要寫多久。
const joinTol = 1.5

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

	// 字距拉開時,連接筆畫也跟著變長,所以併筆的容差要一起放寬,
	// 否則字母會從「相連」變成「各自獨立」,失去草寫的樣子。
	track := o.LetterSpacing
	tol := joinTol*scale + track*1.2

	var cur Line
	var poly []pen.Point // 目前正在連筆的折線
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

	flush := func() {
		switch {
		case len(poly) >= 2:
			cur.Strokes = append(cur.Strokes, poly)
		case len(poly) == 1:
			// 單點(句點之類)畫一小段,否則注入端會拒收、句點就不見了
			cur.Strokes = append(cur.Strokes, []pen.Point{poly[0], {X: poly[0].X + 1, Y: poly[0].Y + 1}})
		}
		poly = nil
	}
	endLine := func() {
		flush()
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
			for _, st := range g.strokes {
				ps := make([]pen.Point, 0, len(st))
				for _, q := range st {
					ps = append(ps, sp(q, g.lb))
				}
				// 貪婪串接:只要這一筆的起點落在前一筆的收筆點上,就併成同一筆。
				// 刻意不去假設「哪一筆是字身、哪一筆是附加筆劃」——Hershey 會把一個
				// 字母拆成多筆(n 的第一筆是自左 bearing 進來的引筆、第二筆才是字身;
				// i 的第一筆卻是上面那個點),依筆劃順序猜角色會猜錯。座標自己會說話。
				if len(poly) > 0 && len(ps) > 0 &&
					math.Hypot(ps[0].X-poly[len(poly)-1].X, ps[0].Y-poly[len(poly)-1].Y) <= tol {
					poly = append(poly, ps...)
					continue
				}
				flush()
				poly = ps
			}
			penX += (g.rb-g.lb)*scale + track
		}
	}
	endLine()
	return lines, dropped
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
