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
//
// strokes 是字型原本的分段;body/marks/joins 是排版真正使用的「正規化」形式,
// 由 normalize 產生,目的是讓每個字母從同一個進筆點開始、收在同一個收筆點——
// 見 normalize 的說明。
type hGlyph struct {
	lb, rb  float64 // 左右 bearing;前進寬度 = rb - lb
	strokes [][]pt

	joins bool   // 走連筆規則(進出點都在 (lb,4)/(rb,4));只有小寫字母
	body  [][]pt // 字身:第一筆起於進筆點,最後一筆收於收筆點
	marks [][]pt // 與字身分離的附加筆劃(i j 的點、t 的橫槓、x 的交叉筆)
}

var hershey = parseJHF(hersheyData)

// normalize 把字符拆成「字身」與「附加筆劃」兩部分,並標記它是否走連筆慣例。
//
// 這套字型的小寫是為了「按自然前進寬度並排就會自己接上」而設計的:每個字母都
// 有一筆精確收在 (rb,4),而下一個字母的最左緣就落在自己的 (lb,4)——兩者在螢幕上
// 是同一個點、同一個高度,墨跡自然相接。所以這裡**不做**任何幾何調整:補引筆
// 會讓 a o c e 的引線橫穿過字碗,拉開字距則會把這個天然接合拆散。
//
// 真正要處理的是筆劃「順序」:i j 的點在第一筆、t 的橫槓與 x 的交叉筆在最後一筆,
// 夾在字身中間會把連筆鏈打斷。抽出來成為 marks,由排版在寫完一個單字後補上
// ——人寫草寫也是先把字寫完,再回頭點 i、劃 t。
func (g *hGlyph) normalize(r rune) {
	// 只有小寫字母遵循這套慣例。大寫、數字、標點的收筆點各在各的地方,維持原樣。
	g.body = g.strokes
	if r < 'a' || r > 'z' || len(g.strokes) == 0 {
		return
	}

	exit := -1
	for i, st := range g.strokes {
		if p := st[len(st)-1]; p.x == g.rb && p.y == hJoinY {
			exit = i
		}
	}
	if exit < 0 {
		return
	}

	// 進筆筆劃:第一個起於 (lb,4) 的筆劃。排在它前面的是附加筆劃(i j 的點)。
	// 字碗類字母(a c d e g o q w)沒有這種筆劃,entry 維持 0。
	entry := 0
	for i := 0; i <= exit; i++ {
		if p := g.strokes[i][0]; p.x == g.lb && p.y == hJoinY {
			entry = i
			break
		}
	}

	g.joins = true
	g.marks = append(append([][]pt{}, g.strokes[:entry]...), g.strokes[exit+1:]...)
	g.body = append([][]pt{}, g.strokes[entry:exit+1]...)
}

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
		r := rune(32 + i)
		g.normalize(r)
		out[r] = g
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
