// P1b PoC:讀取並錄製真實 Wacom 筆事件。
// 目的:(1) 驗證能從 /dev/input/event1 讀到手寫事件(模組 A 的基礎);
// (2) 錄下真筆完整事件序列(原始 16-byte dump),供注入模組完全模仿。
//
// 用法:./poc-read -raw /home/root/rm2-scribe/pen.raw
// 以 SIGINT/SIGTERM 結束;stdout 印出人類可讀的解碼摘要。
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

var codeNames = map[uint16]string{
	0x00: "ABS_X", 0x01: "ABS_Y", 0x18: "PRESSURE", 0x19: "DISTANCE",
	0x1a: "TILT_X", 0x1b: "TILT_Y",
}
var keyNames = map[uint16]string{
	0x140: "BTN_TOOL_PEN", 0x141: "BTN_TOOL_RUBBER", 0x14a: "BTN_TOUCH",
	0x14b: "BTN_STYLUS", 0x14c: "BTN_STYLUS2",
}

func main() {
	dev := flag.String("dev", "/dev/input/event1", "evdev 裝置路徑")
	raw := flag.String("raw", "", "原始事件 dump 輸出檔(可選)")
	flag.Parse()

	f, err := os.Open(*dev)
	if err != nil {
		fmt.Fprintln(os.Stderr, "開啟裝置失敗:", err)
		os.Exit(1)
	}
	defer f.Close()

	var rawOut *os.File
	if *raw != "" {
		rawOut, err = os.Create(*raw)
		if err != nil {
			fmt.Fprintln(os.Stderr, "建立 dump 檔失敗:", err)
			os.Exit(1)
		}
		defer rawOut.Close()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println("\n結束錄製。")
		if rawOut != nil {
			rawOut.Sync()
		}
		os.Exit(0)
	}()

	fmt.Printf("開始錄製 %s(Ctrl-C 結束)…\n", *dev)
	buf := make([]byte, 16)
	n := 0
	for {
		if _, err := readFull(f, buf); err != nil {
			fmt.Fprintln(os.Stderr, "讀取錯誤:", err)
			os.Exit(1)
		}
		if rawOut != nil {
			rawOut.Write(buf)
		}
		sec := binary.LittleEndian.Uint32(buf[0:])
		usec := binary.LittleEndian.Uint32(buf[4:])
		typ := binary.LittleEndian.Uint16(buf[8:])
		code := binary.LittleEndian.Uint16(buf[10:])
		val := int32(binary.LittleEndian.Uint32(buf[12:]))
		n++
		switch typ {
		case 0x00:
			fmt.Printf("%d.%06d SYN ------------------------ (#%d)\n", sec, usec, n)
		case 0x01:
			name := keyNames[code]
			if name == "" {
				name = fmt.Sprintf("KEY_0x%x", code)
			}
			fmt.Printf("%d.%06d KEY %-16s %d\n", sec, usec, name, val)
		case 0x03:
			name := codeNames[code]
			if name == "" {
				name = fmt.Sprintf("ABS_0x%x", code)
			}
			fmt.Printf("%d.%06d ABS %-16s %d\n", sec, usec, name, val)
		default:
			fmt.Printf("%d.%06d T%d C0x%x V%d\n", sec, usec, typ, code, val)
		}
	}
}

func readFull(f *os.File, buf []byte) (int, error) {
	got := 0
	for got < len(buf) {
		n, err := f.Read(buf[got:])
		if err != nil {
			return got, err
		}
		got += n
	}
	return got, nil
}
