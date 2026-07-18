// P2 PoC:監聽真實手寫筆劃,停筆逾時後渲染成 PNG。
// 驗證模組 A(筆劃監聽 + 觸發)與模組 B(筆劃→PNG)。
//
// 用法:./poc-capture -idle 8 -out /home/root/rm2-scribe/capture.png
// 寫完字停筆 idle 秒後,自動輸出 PNG 並結束。
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"rm2scribe/internal/input"
	"rm2scribe/internal/render"
)

func main() {
	dev := flag.String("dev", "/dev/input/event1", "evdev 裝置")
	idleSec := flag.Float64("idle", 8, "停筆逾時秒數")
	out := flag.String("out", "/home/root/rm2-scribe/capture.png", "輸出 PNG 路徑")
	flag.Parse()

	r, err := input.NewReader(*dev, time.Duration(*idleSec*float64(time.Second)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "開啟裝置失敗:", err)
		os.Exit(1)
	}
	defer r.Close()

	fmt.Printf("開始監聽手寫(停筆 %.0f 秒後輸出)…\n", *idleSec)
	stop := make(chan struct{})
	batches := make(chan input.Batch, 1)
	go r.Run(stop, batches)

	b := <-batches
	close(stop)

	n := 0
	for _, s := range b.Strokes {
		n += len(s)
	}
	fmt.Printf("擷取到 %d 筆劃、共 %d 點,渲染中…\n", len(b.Strokes), n)
	if err := render.StrokesToPNG(b.Strokes, *out); err != nil {
		fmt.Fprintln(os.Stderr, "渲染失敗:", err)
		os.Exit(1)
	}
	fi, _ := os.Stat(*out)
	fmt.Printf("已輸出 %s(%d bytes)\n", *out, fi.Size())
}
