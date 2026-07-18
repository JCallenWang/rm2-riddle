// 診斷:畫一段真實文字,再用超密集(3px)蛇形 + 最大壓力橡皮擦擦除同一塊,
// 判斷殘留是「橡皮擦密度/半徑」還是「橡皮擦模式」問題。
package main

import (
	"fmt"
	"os"
	"time"

	"rm2scribe/internal/font"
	"rm2scribe/internal/pen"
)

func main() {
	d, err := pen.OpenDirect("/dev/input/event1")
	if err != nil {
		fmt.Fprintln(os.Stderr, "開啟失敗:", err)
		os.Exit(1)
	}
	defer d.Close()

	// 畫兩行文字
	strokes := font.Layout("Hello World Test 123\nThe quick brown fox jumps.", font.LayoutOpts{
		StartX: 150, StartY: 500, FontPx: 44, LineSpacing: 1.6, MaxX: 1250,
	})
	fmt.Println("2 秒後畫文字…")
	time.Sleep(2 * time.Second)
	for _, st := range strokes {
		if len(st) >= 2 {
			d.DrawStroke(st, 6, 3*time.Millisecond)
			time.Sleep(30 * time.Millisecond)
		}
	}
	fmt.Println("已畫文字,3 秒後以 3px 超密集蛇形擦除…")
	time.Sleep(3 * time.Second)

	// 求 bbox
	x0, y0, x1, y1 := 1e18, 1e18, -1e18, -1e18
	for _, st := range strokes {
		for _, p := range st {
			if p.X < x0 {
				x0 = p.X
			}
			if p.Y < y0 {
				y0 = p.Y
			}
			if p.X > x1 {
				x1 = p.X
			}
			if p.Y > y1 {
				y1 = p.Y
			}
		}
	}
	x0 -= 30
	y0 -= 30
	x1 += 30
	y1 += 30

	// 3px 蛇形單筆
	var path []pen.Point
	dir := 1.0
	for y := y0; y <= y1; y += 3 {
		if dir > 0 {
			path = append(path, pen.Point{X: x0, Y: y}, pen.Point{X: x1, Y: y})
		} else {
			path = append(path, pen.Point{X: x1, Y: y}, pen.Point{X: x0, Y: y})
		}
		dir = -dir
	}
	d.EraseStroke(path, 40, time.Millisecond)
	fmt.Println("擦除完成,請看兩行文字是否完全消失。")
	time.Sleep(1 * time.Second)
}
