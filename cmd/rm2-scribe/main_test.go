package main

import (
	"sync"
	"testing"
)

// fakeReader 記錄靜音呼叫,讓我們在沒有裝置的情況下驗證靜音區間。
type fakeReader struct {
	mu      sync.Mutex
	muted   bool
	unmutes int
	clears  int
}

func (f *fakeReader) Mute() {
	f.mu.Lock()
	f.muted = true
	f.mu.Unlock()
}

func (f *fakeReader) Unmute() {
	f.mu.Lock()
	f.muted = false
	f.unmutes++
	f.mu.Unlock()
}

func (f *fakeReader) ClearBuffer() {
	f.mu.Lock()
	f.clears++
	f.mu.Unlock()
}

func (f *fakeReader) isMuted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.muted
}

// TestHoldKeepsMuteAcrossInjections 是這個設計的重點:一次互動裡會注入很多次
// (吸收手寫、寫回覆、擦回覆),中間還夾著等 LLM 與等 fadeout 的空檔。
// 只要外層 hold 還在,這些空檔都不可以恢復接收,否則使用者在等待期間寫的字
// 會被夾進下一批送出。
func TestHoldKeepsMuteAcrossInjections(t *testing.T) {
	f := &fakeReader{}
	c := &injCtl{reader: f}

	release := c.hold() // handle() 一進來就取得的外層區間
	if !f.isMuted() {
		t.Fatal("取得 hold 後應該已靜音")
	}

	// 模擬三次注入,每次之間是「等 LLM」「等 fadeout」的空檔
	for i, stage := range []string{"吸收手寫", "寫回覆", "擦回覆"} {
		c.run(func() {})
		if !f.isMuted() {
			t.Errorf("第 %d 次注入(%s)結束後靜音被解除了", i+1, stage)
		}
	}
	if f.unmutes != 0 {
		t.Errorf("外層 hold 還在時不該解除靜音,卻解除了 %d 次", f.unmutes)
	}

	release()
	if f.isMuted() {
		t.Error("外層 hold 放掉後應恢復接收")
	}
	if f.clears != 1 {
		t.Errorf("恢復接收前應清掉靜音期間殘留的筆劃一次,got %d", f.clears)
	}

	// handle() 的 defer 可能與其他路徑重複呼叫,重複放掉不可以把計數壓成負的
	release()
	if f.unmutes != 1 {
		t.Errorf("重複 release 應無效果,unmute 次數 = %d", f.unmutes)
	}
}

// TestRunAloneMutes 確認即使沒有外層 hold(例如未來新增的呼叫路徑),
// 單獨注入也一定是靜音的——注入事件被自己讀回會造成無限迴圈。
func TestRunAloneMutes(t *testing.T) {
	f := &fakeReader{}
	c := &injCtl{reader: f}

	sawMute := false
	c.run(func() { sawMute = f.isMuted() })

	if !sawMute {
		t.Error("注入進行中必須是靜音狀態")
	}
	if f.isMuted() {
		t.Error("沒有外層 hold 時,注入結束應恢復接收")
	}
}

// TestHoldNestedConcurrently 確認計數在並行下不會提早歸零。
func TestHoldNestedConcurrently(t *testing.T) {
	f := &fakeReader{}
	c := &injCtl{reader: f}

	outer := c.hold()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.run(func() {})
		}()
	}
	wg.Wait()

	if !f.isMuted() || f.unmutes != 0 {
		t.Errorf("並行注入期間外層 hold 應維持靜音:muted=%v unmutes=%d", f.isMuted(), f.unmutes)
	}
	outer()
	if f.isMuted() {
		t.Error("全部放掉後應恢復接收")
	}
}
