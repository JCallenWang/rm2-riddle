// P5 本機驗證:把一段文字用字型排版,渲染成 PNG 供目視檢查字型是否清楚。
// 於 Mac 原生執行(非 armv7),不接觸裝置。
//
// 預設渲染連筆草寫(正式回覆用的字型);-print 可對照舊的印刷體單線字型。
// 調整字形時的工作流程就是改 internal/font/cursive.go → 跑這支 → 看圖。
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"

	"rm2scribe/internal/font"
	"rm2scribe/internal/pen"
)

func main() {
	out := flag.String("out", "font-preview.png", "輸出 PNG")
	text := flag.String("text", "the quick brown fox jumps over a lazy dog\nHello! How are you doing today?\nabcdefghijklmnopqrstuvwxyz", "要渲染的文字")
	fontPx := flag.Float64("size", 44, "字高(基線到上伸部頂)")
	spacing := flag.Float64("spacing", 1.5, "行距倍率")
	width := flag.Int("w", 1194, "畫布寬(預設為裝置安全區寬度)")
	usePrint := flag.Bool("print", false, "改用舊的印刷體單線字型")
	guides := flag.Bool("guides", false, "畫出基線/上伸下伸輔助線")
	track := flag.Float64("track", 0, "字母之間額外拉開的距離(px)")
	ink := flag.Float64("ink", 3, "模擬裝置的筆刷粗細(px)——本機細線看得清楚的字,裝置上可能被墨水填滿")
	flag.Parse()

	const margin = 30.0
	var strokes [][]pen.Point
	var lines []font.Line

	if *usePrint {
		strokes = font.Layout(*text, font.LayoutOpts{
			StartX: margin, StartY: margin, FontPx: *fontPx,
			LineSpacing: *spacing, MaxX: float64(*width) - margin,
		})
	} else {
		var dropped int
		lines, dropped = font.LayoutCursive(*text, font.CursiveOpts{
			StartX: margin, StartY: margin, FontPx: *fontPx,
			LineSpacing: *spacing, MaxX: float64(*width) - margin, LetterSpacing: *track,
		})
		if dropped > 0 {
			fmt.Fprintf(os.Stderr, "有 %d 個 token 超出 MaxY 被丟棄\n", dropped)
		}
		for _, ln := range lines {
			strokes = append(strokes, ln.Strokes...)
		}
	}

	// 依內容決定畫布高度
	h := int(margin)
	for _, st := range strokes {
		for _, p := range st {
			if int(p.Y)+int(margin) > h {
				h = int(p.Y) + int(margin)
			}
		}
	}
	w := *width
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	set := func(x, y int, v uint8) {
		if x >= 0 && x < w && y >= 0 && y < h {
			img.SetGray(x, y, color.Gray{Y: v})
		}
	}
	// 輔助線:上伸頂(淺)、腰線(淺)、基線(深)
	if *guides && !*usePrint {
		scale := *fontPx / 21
		for i := 0; ; i++ {
			top := margin + float64(i)*(*fontPx)*(*spacing)
			if top > float64(h) {
				break
			}
			for x := 0; x < w; x++ {
				set(x, int(top), 225)           // 上伸部頂
				set(x, int(top+3*scale), 225)   // 腰線
				set(x, int(top+7*scale), 195)   // 基線
				set(x, int(top+9.5*scale), 235) // 下伸部底
			}
		}
	}
	// 以 *ink 為直徑的圓筆刷沿路徑塗抹,盡量接近 xochitl 實際落筆的樣子
	rad := *ink / 2
	line := func(a, b pen.Point) {
		dx, dy := b.X-a.X, b.Y-a.Y
		n := int(absf(dx))
		if int(absf(dy)) > n {
			n = int(absf(dy))
		}
		n++
		ri := int(rad + 1)
		for i := 0; i <= n; i++ {
			t := float64(i) / float64(n)
			px, py := a.X+dx*t, a.Y+dy*t
			for oy := -ri; oy <= ri; oy++ {
				for ox := -ri; ox <= ri; ox++ {
					if float64(ox*ox+oy*oy) <= rad*rad {
						set(int(px)+ox, int(py)+oy, 0)
					}
				}
			}
		}
	}
	for _, st := range strokes {
		for i := 1; i < len(st); i++ {
			line(st[i-1], st[i])
		}
	}

	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if lines != nil {
		n := 0
		for _, ln := range lines {
			n += len(ln.Strokes)
		}
		fmt.Printf("%d 行 / %d 筆劃(平均每行 %.1f 筆)\n", len(lines), n, float64(n)/float64(len(lines)))
	}
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
