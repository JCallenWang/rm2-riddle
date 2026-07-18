// P5 本機驗證:把一段文字用單線字型排版,渲染成 PNG 供目視檢查字型是否清楚。
// 於 Mac 原生執行(非 armv7),不接觸裝置。
package main

import (
	"flag"
	"image"
	"image/color"
	"image/png"
	"os"

	"rm2scribe/internal/font"
	"rm2scribe/internal/pen"
)

func main() {
	out := flag.String("out", "font-preview.png", "輸出 PNG")
	flag.Parse()

	text := "Hello! How are you?\nI am doing great today.\nThanks for asking. 12345"
	strokes := font.Layout(text, font.LayoutOpts{
		StartX:      40,
		StartY:      40,
		FontPx:      44,
		LineSpacing: 1.5,
		MaxX:        900,
	})

	const w, h = 960, 320
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	set := func(x, y int) {
		if x >= 0 && x < w && y >= 0 && y < h {
			img.SetGray(x, y, color.Gray{Y: 0})
		}
	}
	line := func(a, b pen.Point) {
		dx, dy := b.X-a.X, b.Y-a.Y
		n := int(absf(dx))
		if int(absf(dy)) > n {
			n = int(absf(dy))
		}
		n++
		for i := 0; i <= n; i++ {
			t := float64(i) / float64(n)
			px, py := int(a.X+dx*t), int(a.Y+dy*t)
			for oy := -1; oy <= 1; oy++ {
				for ox := -1; ox <= 1; ox++ {
					set(px+ox, py+oy)
				}
			}
		}
	}
	for _, st := range strokes {
		for i := 1; i < len(st); i++ {
			line(st[i-1], st[i])
		}
	}

	f, _ := os.Create(*out)
	defer f.Close()
	png.Encode(f, img)
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
