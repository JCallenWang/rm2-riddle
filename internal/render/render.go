// Package render 把 Wacom 座標系的筆劃渲染成螢幕座標的白底黑線 PNG。
package render

import (
	"image"
	"image/color"
	"image/png"
	"os"

	"rm2scribe/internal/input"
)

// Wacom / 螢幕幾何(與 internal/pen 一致)。
const (
	screenW  = 1404
	screenH  = 1872
	wacomXMx = 20966
	wacomYMx = 15725
)

// fromWacom 是 pen.toWacom 的反轉換:Wacom 座標 → 螢幕像素。
func fromWacom(wx, wy int32) (float64, float64) {
	sy := float64(screenH) - float64(wx)*float64(screenH)/float64(wacomXMx)
	sx := float64(wy) * float64(screenW) / float64(wacomYMx)
	return sx, sy
}

// StrokesToPNG 將筆劃渲染成 PNG 檔。
// 影像裁切到筆劃的邊界框並加白邊,讓手寫內容在圖中置中且夠大,提升辨識率。
func StrokesToPNG(strokes []input.Stroke, path string) error {
	// 先轉螢幕座標並求邊界框
	type pt struct{ x, y float64 }
	var pss [][]pt
	minX, minY := 1e18, 1e18
	maxX, maxY := -1e18, -1e18
	for _, s := range strokes {
		var ps []pt
		for _, p := range s {
			x, y := fromWacom(p.X, p.Y)
			ps = append(ps, pt{x, y})
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x > maxX {
				maxX = x
			}
			if y > maxY {
				maxY = y
			}
		}
		if len(ps) > 0 {
			pss = append(pss, ps)
		}
	}
	if len(pss) == 0 {
		return writePNG(image.NewGray(image.Rect(0, 0, 1, 1)), path)
	}

	const pad = 40.0
	w := int(maxX-minX) + int(2*pad)
	h := int(maxY-minY) + int(2*pad)
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 255 // 白底
	}

	ox, oy := minX-pad, minY-pad
	for _, ps := range pss {
		for i := 1; i < len(ps); i++ {
			drawLine(img,
				ps[i-1].x-ox, ps[i-1].y-oy,
				ps[i].x-ox, ps[i].y-oy)
		}
		if len(ps) == 1 { // 單點也點一下
			drawLine(img, ps[0].x-ox, ps[0].y-oy, ps[0].x-ox+1, ps[0].y-oy)
		}
	}
	return writePNG(img, path)
}

// drawLine 以 3px 筆寬畫黑線(Bresenham + 鄰域)。
func drawLine(img *image.Gray, x0, y0, x1, y1 float64) {
	dx := absf(x1 - x0)
	dy := absf(y1 - y0)
	steps := int(dx)
	if int(dy) > steps {
		steps = int(dy)
	}
	steps++
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		px := int(x0 + (x1-x0)*t)
		py := int(y0 + (y1-y0)*t)
		for ddy := -1; ddy <= 1; ddy++ {
			for ddx := -1; ddx <= 1; ddx++ {
				setPix(img, px+ddx, py+ddy)
			}
		}
	}
}

func setPix(img *image.Gray, x, y int) {
	b := img.Bounds()
	if x < b.Min.X || x >= b.Max.X || y < b.Min.Y || y >= b.Max.Y {
		return
	}
	img.SetGray(x, y, color.Gray{Y: 0})
}

func writePNG(img image.Image, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
