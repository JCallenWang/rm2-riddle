// P1 PoC:建立虛擬筆並在畫面中央畫一個大寫「F」。
// 「F」在兩軸皆不對稱,一次目視即可判定座標轉換的旋轉與鏡像是否正確。
//
// 用法(於 rM2 上以 root 執行):
//
//	./poc-inject            # 立即模式:建立虛擬筆 → 3 秒後畫 F → 銷毀
//	./poc-inject -serve     # 常駐模式:建立虛擬筆後持續存活;
//	                        # 每次偵測到觸發檔(/home/root/rm2-scribe/draw)即畫一個 F 並刪除觸發檔。
//	                        # 供「先建筆 → 重啟 xochitl → 再觸發」的流程使用。
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"rm2scribe/internal/pen"
)

const triggerFile = "/home/root/rm2-scribe/draw"

func drawF(d *pen.Device) error {
	// 大寫 F,畫面中央附近(螢幕座標:1404 寬 × 1872 高)
	strokes := [][]pen.Point{
		{{X: 550, Y: 600}, {X: 550, Y: 1250}}, // 直豎(上→下)
		{{X: 550, Y: 600}, {X: 950, Y: 600}},  // 上橫
		{{X: 550, Y: 900}, {X: 870, Y: 900}},  // 中橫
	}
	for i, s := range strokes {
		fmt.Printf("畫第 %d/3 劃…\n", i+1)
		if err := d.DrawStroke(s, 6, 5*time.Millisecond); err != nil {
			return err
		}
		time.Sleep(150 * time.Millisecond)
	}
	fmt.Println("注入完成。")
	return nil
}

func main() {
	serve := flag.Bool("serve", false, "常駐模式:等待觸發檔再畫,可重複觸發")
	uinputMode := flag.Bool("uinput", false, "改用 uinput 虛擬裝置(預設為直接寫入真實節點)")
	flag.Parse()

	var d *pen.Device
	var err error
	if *uinputMode {
		d, err = pen.New("/dev/input/event1")
	} else {
		d, err = pen.OpenDirect("/dev/input/event1")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "建立注入端失敗:", err)
		os.Exit(1)
	}
	defer d.Close()
	fmt.Printf("注入端就緒(X:%d..%d Y:%d..%d P:%d..%d)\n",
		d.X.Min, d.X.Max, d.Y.Min, d.Y.Max, d.Pressure.Min, d.Pressure.Max)

	if !*serve {
		time.Sleep(1 * time.Second)
		if err := drawF(d); err != nil {
			fmt.Fprintln(os.Stderr, "注入失敗:", err)
			os.Exit(1)
		}
		time.Sleep(2 * time.Second)
		return
	}

	fmt.Println("常駐模式:等待觸發檔", triggerFile)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-sig:
			fmt.Println("收到中斷,銷毀虛擬筆。")
			return
		case <-tick.C:
			if _, err := os.Stat(triggerFile); err == nil {
				os.Remove(triggerFile)
				if err := drawF(d); err != nil {
					fmt.Fprintln(os.Stderr, "注入失敗:", err)
				}
			}
		}
	}
}
