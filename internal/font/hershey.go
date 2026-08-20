// Hershey "Script" 單線字型。
//
// 取代先前自製的草寫字型——自製字形在實機上辨識度不足,改用這套經過數十年
// 實際使用驗證的公有領域字型。資料檔 hershey-script.jhf 原樣收錄未修改。
//
// 出處聲明(字型授權要求隨資料一併散布,全文見 HERSHEY-LICENSE.txt):
//   - The Hershey Fonts were originally created by Dr. A. V. Hershey while
//     working at the U. S. National Bureau of Standards.
//   - The format of the Font data in this distribution was originally created
//     by James Hurt, Cognition, Inc., 900 Technology Park Drive,
//     Billerica, MA 01821 (mit-eddie!ci-dandelion!hurt).
//
// .jhf 格式:每行一個字符,第 1–5 欄為字號、第 6–8 欄為頂點數,第 9 欄起是
// 成對的字元,每個字元減去 'R' 就是座標值。第一對是左右 bearing(前進寬度),
// 之後每一對是一個點,而 " R" 這一對代表提筆。行的順序對應 ASCII 32 起。
package font

import (
	_ "embed"
	"strings"
)

//go:embed hershey-script.jhf
var hersheyData string

// Hershey 的座標系(y 向下)。實測本字型的分佈:
// x 字身(a c e m n o u v w x)佔 0..9、上伸部(b d f h k l t)到 -12、
// 下伸部(f g j p q y z)到 21;字母之間的連接點在 y=4。
//
// 字級(hAsc..hBase)刻意只以「字母與數字」為準——若拿最高的字符來定,
// 一段普通文字會因為偶爾出現的括號而整體變小。行距與邊界則要用 hTop/hDsc
// 這組涵蓋全部 96 個字符的真實極值,否則 [ ( { | / # 這些會戳進上一行。
const (
	hAsc   = -12.0 // 字母的上伸部頂(字級參考)
	hBase  = 9.0   // 基線
	hTop   = -16.0 // 全部字符的最高點(# 之類)
	hDsc   = 21.0  // 全部字符的最低點
	hJoinY = 4.0   // 字母之間的連接高度(多數小寫的起訖點都在這裡)
)

// hGlyph 是一個 Hershey 字符。座標為原始格線單位,尚未縮放。
type hGlyph struct {
	lb, rb  float64 // 左右 bearing;前進寬度 = rb - lb
	strokes [][]pt
}

var hershey = parseJHF(hersheyData)

// parseJHF 解析 .jhf,回傳 rune → 字符。
func parseJHF(data string) map[rune]hGlyph {
	out := make(map[rune]hGlyph)
	for i, line := range strings.Split(data, "\n") {
		if len(line) < 10 {
			continue
		}
		body := line[8:]
		g := hGlyph{
			lb: float64(body[0]) - 'R',
			rb: float64(body[1]) - 'R',
		}
		var cur []pt
		for j := 2; j+1 < len(body)+1 && j+2 <= len(body); j += 2 {
			pair := body[j : j+2]
			if pair == " R" { // 提筆
				if len(cur) > 0 {
					g.strokes = append(g.strokes, cur)
					cur = nil
				}
				continue
			}
			cur = append(cur, pt{float64(pair[0]) - 'R', float64(pair[1]) - 'R'})
		}
		if len(cur) > 0 {
			g.strokes = append(g.strokes, cur)
		}
		out[rune(32+i)] = g
	}
	return out
}

// hLeftBleed 是所有字符中「畫到左 bearing 左邊」最多的量(格線單位)。
// j f p 的下伸迴圈會往左甩(j 甩 8 單位,比它自己的前進寬度還大),排版時
// 整行右移這麼多才不會畫到 StartX 左邊——那個方向是 xochitl 的左側工具列,
// 注入落在上面會被當成點按 UI 而切換工具。
var hLeftBleed = maxLeftBleed()

func maxLeftBleed() float64 {
	worst := 0.0
	for _, g := range hershey {
		for _, st := range g.strokes {
			for _, p := range st {
				if b := g.lb - p.x; b > worst {
					worst = b
				}
			}
		}
	}
	return worst
}

// hersheyFor 取字符;沒收錄的字元回傳空白(只前進不畫)。
func hersheyFor(r rune) (hGlyph, bool) {
	if g, ok := hershey[r]; ok {
		return g, true
	}
	return hershey[' '], false
}
