// 驗證:先畫內容,再用注入橡皮擦(BTN_TOOL_RUBBER)擦掉它。
// 先畫三條粗線(在 y=550..850),停 2 秒讓使用者看到,再擦除同一區域。
package main

import (
	"fmt"
	"os"
	"time"

	"rm2scribe/internal/pen"
)

func main() {
	d, err := pen.OpenDirect("/dev/input/event1")
	if err != nil {
		fmt.Fprintln(os.Stderr, "開啟失敗:", err)
		os.Exit(1)
	}
	defer d.Close()

	fmt.Println("2 秒後畫三條測試線…")
	time.Sleep(2 * time.Second)
	for _, y := range []float64{600, 700, 800} {
		line := []pen.Point{{X: 200, Y: y}, {X: 1200, Y: y}}
		if err := d.DrawStroke(line, 6, 3*time.Millisecond); err != nil {
			fmt.Fprintln(os.Stderr, "畫線失敗:", err)
			os.Exit(1)
		}
	}
	fmt.Println("已畫三條線,3 秒後擦除…")
	time.Sleep(3 * time.Second)

	// 以每 20px 一條水平線來回覆蓋 y=560..840,確保蓋過橡皮擦半徑
	for y := 560.0; y <= 840; y += 20 {
		line := []pen.Point{{X: 180, Y: y}, {X: 1220, Y: y}}
		if err := d.EraseStroke(line, 8, 2*time.Millisecond); err != nil {
			fmt.Fprintln(os.Stderr, "擦除失敗:", err)
			os.Exit(1)
		}
	}
	fmt.Println("擦除完成,請看三條線是否消失。")
	time.Sleep(1 * time.Second)
}
