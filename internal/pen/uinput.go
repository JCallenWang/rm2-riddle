// Package pen 建立一支虛擬 Wacom 筆(經由 /dev/uinput),
// 讓 xochitl 把注入的筆劃當成真實手寫並渲染到 e-ink 畫面上。
//
// 硬性限制:rM2 為 armv7 32-bit;本檔案只用 raw syscall,無 cgo、無第三方相依,
// 以維持 CGO_ENABLED=0 的靜態交叉編譯。
package pen

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// evdev 事件型別/代碼(linux/input-event-codes.h)
const (
	evSyn = 0x00
	evKey = 0x01
	evAbs = 0x03

	synReport = 0

	btnToolPen    = 0x140
	btnToolRubber = 0x141
	btnTouch      = 0x14a
	btnStylus     = 0x14b
	btnStylus2    = 0x14c

	absX        = 0x00
	absY        = 0x01
	absPressure = 0x18
	absDistance = 0x19
	absTiltX    = 0x1a
	absTiltY    = 0x1b
)

// ioctl 編號(32-bit ARM,_IOC = dir<<30|size<<16|type<<8|nr)
const (
	uiSetEvBit   = 0x40045564 // _IOW('U',100,int)
	uiSetKeyBit  = 0x40045565 // _IOW('U',101,int)
	uiSetAbsBit  = 0x40045567 // _IOW('U',103,int)
	uiDevSetup   = 0x405c5503 // _IOW('U',3,struct uinput_setup)
	uiAbsSetup   = 0x401c5504 // _IOW('U',4,struct uinput_abs_setup)
	uiDevCreate  = 0x00005501 // _IO('U',1)
	uiDevDestroy = 0x00005502 // _IO('U',2)

	eviocgabsBase = 0x80184540 // EVIOCGABS(0) = _IOR('E',0x40,struct input_absinfo)
)

// 螢幕與數位板幾何。數位板範圍於執行期自真實裝置讀出,
// 此處僅定義螢幕像素尺寸(rM2 固定 1404x1872 直向)。
const (
	ScreenW = 1404
	ScreenH = 1872
)

// AbsInfo 對應 struct input_absinfo(6 個 int32)。
type AbsInfo struct {
	Value, Min, Max, Fuzz, Flat, Resolution int32
}

// Device 是一個筆事件注入端:
// uinput 模式(New)建立獨立虛擬裝置;direct 模式(OpenDirect)直接寫入真實
// 裝置節點,由 kernel input_inject_event 分發給所有讀取者(含 xochitl),
// 不受 xochitl 只在啟動時列舉裝置的限制。
type Device struct {
	f      *os.File
	direct bool
	// 自真實 Wacom 複製的軸範圍
	X, Y, Pressure, Distance, TiltX, TiltY AbsInfo
}

func ioctl(fd uintptr, req uintptr, arg uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg)
	if errno != 0 {
		return errno
	}
	return nil
}

// readAbs 以 EVIOCGABS 從真實裝置讀出某一軸的 input_absinfo。
func readAbs(f *os.File, code int) (AbsInfo, error) {
	var ai AbsInfo
	if err := ioctl(f.Fd(), uintptr(eviocgabsBase)+uintptr(code), uintptr(unsafe.Pointer(&ai))); err != nil {
		return ai, fmt.Errorf("EVIOCGABS(0x%x): %w", code, err)
	}
	return ai, nil
}

// OpenDirect 以寫入模式開啟真實 Wacom 節點,直接注入事件。
func OpenDirect(realWacom string) (*Device, error) {
	f, err := os.OpenFile(realWacom, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("開啟 %s 失敗: %w", realWacom, err)
	}
	d := &Device{f: f, direct: true}
	for _, a := range []struct {
		code int
		dst  *AbsInfo
	}{
		{absX, &d.X}, {absY, &d.Y}, {absPressure, &d.Pressure},
		{absDistance, &d.Distance}, {absTiltX, &d.TiltX}, {absTiltY, &d.TiltY},
	} {
		ai, err := readAbs(f, a.code)
		if err != nil {
			f.Close()
			return nil, err
		}
		*a.dst = ai
	}
	return d, nil
}

// New 建立虛擬筆:先從 realWacom(通常為 /dev/input/event1)複製各軸範圍,
// 再於 /dev/uinput 建立具相同 capabilities 的裝置。
func New(realWacom string) (*Device, error) {
	rf, err := os.Open(realWacom)
	if err != nil {
		return nil, fmt.Errorf("開啟真實 Wacom 失敗: %w", err)
	}
	defer rf.Close()

	d := &Device{}
	for _, a := range []struct {
		code int
		dst  *AbsInfo
	}{
		{absX, &d.X}, {absY, &d.Y}, {absPressure, &d.Pressure},
		{absDistance, &d.Distance}, {absTiltX, &d.TiltX}, {absTiltY, &d.TiltY},
	} {
		ai, err := readAbs(rf, a.code)
		if err != nil {
			return nil, err
		}
		*a.dst = ai
	}

	uf, err := os.OpenFile("/dev/uinput", os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("開啟 /dev/uinput 失敗: %w", err)
	}
	d.f = uf
	fd := uf.Fd()

	for _, ev := range []int{evSyn, evKey, evAbs} {
		if err := ioctl(fd, uiSetEvBit, uintptr(ev)); err != nil {
			uf.Close()
			return nil, fmt.Errorf("UI_SET_EVBIT %d: %w", ev, err)
		}
	}
	for _, k := range []int{btnToolPen, btnToolRubber, btnTouch, btnStylus, btnStylus2} {
		if err := ioctl(fd, uiSetKeyBit, uintptr(k)); err != nil {
			uf.Close()
			return nil, fmt.Errorf("UI_SET_KEYBIT 0x%x: %w", k, err)
		}
	}

	type absSetup struct {
		Code uint16
		_    uint16 // padding
		Info AbsInfo
	}
	for _, a := range []struct {
		code int
		info AbsInfo
	}{
		{absX, d.X}, {absY, d.Y}, {absPressure, d.Pressure},
		{absDistance, d.Distance}, {absTiltX, d.TiltX}, {absTiltY, d.TiltY},
	} {
		if err := ioctl(fd, uiSetAbsBit, uintptr(a.code)); err != nil {
			uf.Close()
			return nil, fmt.Errorf("UI_SET_ABSBIT 0x%x: %w", a.code, err)
		}
		s := absSetup{Code: uint16(a.code), Info: a.info}
		if err := ioctl(fd, uiAbsSetup, uintptr(unsafe.Pointer(&s))); err != nil {
			uf.Close()
			return nil, fmt.Errorf("UI_ABS_SETUP 0x%x: %w", a.code, err)
		}
	}

	// struct uinput_setup:input_id(4×u16)+ name[80] + ff_effects_max(u32)
	var setup [92]byte
	binary.LittleEndian.PutUint16(setup[0:], 0x0018) // BUS_I2C(與真筆一致)
	binary.LittleEndian.PutUint16(setup[2:], 0x2d1f) // vendor(與真筆一致)
	binary.LittleEndian.PutUint16(setup[4:], 0x0095) // product
	binary.LittleEndian.PutUint16(setup[6:], 0x1231) // version
	copy(setup[8:], "rm2-scribe virtual pen")
	if err := ioctl(fd, uiDevSetup, uintptr(unsafe.Pointer(&setup[0]))); err != nil {
		uf.Close()
		return nil, fmt.Errorf("UI_DEV_SETUP: %w", err)
	}
	if err := ioctl(fd, uiDevCreate, 0); err != nil {
		uf.Close()
		return nil, fmt.Errorf("UI_DEV_CREATE: %w", err)
	}
	return d, nil
}

// Close 銷毀虛擬裝置(direct 模式僅關閉檔案)。
func (d *Device) Close() error {
	if !d.direct {
		ioctl(d.f.Fd(), uiDevDestroy, 0)
	}
	return d.f.Close()
}

// emit 寫入一筆 input_event(32-bit:timeval 8 bytes + type/code/value 8 bytes)。
func (d *Device) emit(typ uint16, code uint16, value int32) error {
	var ev [16]byte
	binary.LittleEndian.PutUint16(ev[8:], typ)
	binary.LittleEndian.PutUint16(ev[10:], code)
	binary.LittleEndian.PutUint32(ev[12:], uint32(value))
	_, err := d.f.Write(ev[:])
	return err
}

func (d *Device) sync() error { return d.emit(evSyn, synReport, 0) }

// toWacom 將螢幕像素座標轉為 Wacom 座標。
// 軸向假設(rmkit 慣例,由 P1 的「F」圖形實測驗證):
// Wacom X 沿螢幕縱向且反向;Wacom Y 沿螢幕橫向。
func (d *Device) toWacom(sx, sy float64) (int32, int32) {
	wx := (float64(ScreenH) - sy) * float64(d.X.Max-d.X.Min) / float64(ScreenH)
	wy := sx * float64(d.Y.Max-d.Y.Min) / float64(ScreenW)
	return clamp(int32(wx)+d.X.Min, d.X.Min, d.X.Max), clamp(int32(wy)+d.Y.Min, d.Y.Min, d.Y.Max)
}

func clamp(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Point 是螢幕像素座標系中的一個筆劃取樣點。
type Point struct{ X, Y float64 }

// DrawStroke 以虛擬筆畫出一條折線筆劃。
// stepPx 為相鄰事件的內插距離(像素),dt 為事件間隔——dt 越大,浮現越慢。
func (d *Device) DrawStroke(pts []Point, stepPx float64, dt time.Duration) error {
	press := int32(2200)
	if press > d.Pressure.Max {
		press = d.Pressure.Max
	}
	return d.strokeWithTool(pts, stepPx, dt, btnToolPen, press)
}

// EraseStroke 以虛擬橡皮擦(BTN_TOOL_RUBBER)沿路徑擦除,用於吸收使用者輸入
// 與讓 LLM 回應在 llm_fadeout 後消失。壓力用接近真筆的中高值(2200);
// 實測最大壓力 4095 超出真筆範圍會被 xochitl 忽略,故不使用。
func (d *Device) EraseStroke(pts []Point, stepPx float64, dt time.Duration) error {
	press := int32(2200)
	if press > d.Pressure.Max {
		press = d.Pressure.Max
	}
	return d.strokeWithTool(pts, stepPx, dt, btnToolRubber, press)
}

func (d *Device) strokeWithTool(pts []Point, stepPx float64, dt time.Duration, tool uint16, press int32) error {
	if len(pts) < 2 {
		return fmt.Errorf("筆劃至少需要兩點")
	}

	// 明確清掉「另一支工具」的狀態,確保同時只有一支工具生效——
	// 否則畫筆←→橡皮擦快速切換時,殘留的畫筆狀態會讓某些擦除掃描線
	// 被當成畫筆畫出黑線。
	other := uint16(btnToolPen)
	if tool == btnToolPen {
		other = btnToolRubber
	}
	d.emit(evKey, other, 0)
	d.emit(evKey, btnTouch, 0)
	d.sync()

	// 進筆:懸浮(distance 漸近)→ 落筆(壓力爬升)
	wx, wy := d.toWacom(pts[0].X, pts[0].Y)
	d.emit(evKey, tool, 1)
	d.emit(evAbs, absX, wx)
	d.emit(evAbs, absY, wy)
	d.emit(evAbs, absDistance, 86)
	d.emit(evAbs, absTiltX, -1000)
	d.emit(evAbs, absTiltY, 2000)
	d.sync()
	time.Sleep(3 * time.Millisecond) // 讓 xochitl 註冊工具切換
	for _, dist := range []int32{60, 40, 25, 18} {
		time.Sleep(2 * time.Millisecond)
		d.emit(evAbs, absDistance, dist)
		d.sync()
	}

	time.Sleep(2 * time.Millisecond)
	d.emit(evKey, btnTouch, 1)
	d.emit(evAbs, absPressure, press*7/10)
	d.emit(evAbs, absDistance, 13)
	d.sync()
	time.Sleep(2 * time.Millisecond)
	d.emit(evAbs, absPressure, press*85/100)
	d.emit(evAbs, absDistance, 2)
	d.sync()

	// 沿折線內插移動
	for i := 1; i < len(pts); i++ {
		x0, y0, x1, y1 := pts[i-1].X, pts[i-1].Y, pts[i].X, pts[i].Y
		dist := math.Hypot(x1-x0, y1-y0)
		steps := int(dist/stepPx) + 1
		for s := 1; s <= steps; s++ {
			t := float64(s) / float64(steps)
			wx, wy := d.toWacom(x0+(x1-x0)*t, y0+(y1-y0)*t)
			d.emit(evAbs, absX, wx)
			d.emit(evAbs, absY, wy)
			d.emit(evAbs, absPressure, press)
			d.sync()
			time.Sleep(dt)
		}
	}

	// 提筆:離紙(壓力歸零)→ 懸浮遠離 → 離開偵測範圍
	d.emit(evAbs, absPressure, 0)
	d.emit(evKey, btnTouch, 0)
	d.emit(evAbs, absDistance, 20)
	d.sync()
	for _, dist := range []int32{40, 60, 86} {
		time.Sleep(2 * time.Millisecond)
		d.emit(evAbs, absDistance, dist)
		d.sync()
	}
	time.Sleep(2 * time.Millisecond)
	d.emit(evKey, tool, 0)
	return d.sync()
}
