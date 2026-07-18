package font

import (
	"strings"

	"rm2scribe/internal/pen"
)

// LayoutOpts 控制排版。座標單位為螢幕像素。
type LayoutOpts struct {
	StartX, StartY float64 // 起始位置(第一行基線左端上緣)
	FontPx         float64 // 字高(像素),對應設計格線的 glyphH
	LineSpacing    float64 // 行距倍率(相對字高)
	MaxX           float64 // 換行右界(超過則自動換行)
}

// Layout 將文字排版成一串筆劃(每筆為螢幕像素座標的點序列)。
// 小寫字以 0.72 倍縮小並對齊基線,近似手寫大小寫差異。
func Layout(text string, o LayoutOpts) [][]pen.Point {
	scale := o.FontPx / glyphH
	space := o.FontPx * (o.LineSpacing)
	if space == 0 {
		space = o.FontPx * 1.4
	}

	var out [][]pen.Point
	penX, penY := o.StartX, o.StartY

	for _, word := range splitKeepSpaces(text) {
		if word == "\n" {
			penX = o.StartX
			penY += space
			continue
		}
		// 估算單字寬度以決定是否換行(空白字不換行)
		w := wordWidth(word, scale)
		if word != " " && penX+w > o.MaxX && penX > o.StartX {
			penX = o.StartX
			penY += space
		}
		for _, r := range word {
			g, _ := glyphFor(r)
			gs := scale
			yOff := 0.0
			// 小寫回退大寫時縮小並下移對齊基線
			if r >= 'a' && r <= 'z' {
				gs = scale * 0.72
				yOff = o.FontPx * (1 - 0.72)
			}
			for _, st := range g.strokes {
				var ps []pen.Point
				for _, p := range st {
					ps = append(ps, pen.Point{
						X: penX + p.x*gs,
						Y: penY + p.y*gs + yOff,
					})
				}
				if len(ps) >= 2 {
					out = append(out, ps)
				} else if len(ps) == 1 {
					// 單點(如句點)畫一小段
					out = append(out, []pen.Point{ps[0], {X: ps[0].X + 1, Y: ps[0].Y + 1}})
				}
			}
			penX += g.advance*gs + o.FontPx*0.12 // 字距
		}
	}
	return out
}

// wordWidth 估算一個 token 的像素寬度。
func wordWidth(word string, scale float64) float64 {
	var w float64
	for _, r := range word {
		g, _ := glyphFor(r)
		gs := scale
		if r >= 'a' && r <= 'z' {
			gs = scale * 0.72
		}
		w += g.advance*gs + (scale*glyphH)*0.12
	}
	return w
}

// splitKeepSpaces 以空白與換行切詞,但保留分隔符作為獨立 token,
// 讓排版能據此換行與插入字距。
func splitKeepSpaces(text string) []string {
	var toks []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			toks = append(toks, b.String())
			b.Reset()
		}
	}
	for _, r := range text {
		switch r {
		case '\n':
			flush()
			toks = append(toks, "\n")
		case ' ', '\t':
			flush()
			toks = append(toks, " ")
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return toks
}
