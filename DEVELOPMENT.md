# rm2-scribe 開發文件

**專案目標:** 在 reMarkable 2 上,將使用者的手寫內容辨識成文字、送給遠端 LLM(Claude / Gemini),並將回覆以「筆跡逐漸浮現」的動畫寫回畫面。

**文件版本:** 2026-07-18(依實機掃描結果撰寫)
**開發語言:** Go(交叉編譯為 armv7 靜態執行檔,已於實機驗證可執行)

---

## 1. 目標裝置實測環境(2026-07-18 實機掃描)

| 項目 | 實測值 | 開發上的意義 |
|---|---|---|
| 機型 | reMarkable 2(i.MX7D, Cortex-A7 雙核) | — |
| CPU 架構 | **armv7l(32-bit)**,VFPv4 + NEON,armhf ABI | 編譯目標必須是 `GOARCH=arm GOARM=7`,**絕不能用 arm64** |
| 韌體 | 3.27.1.0(Codex Linux 5.7.121, Yocto scarthgap);2026-08 更新至 3.28.0.169(Codex 5.8.202)後複驗仍正常 | 超出 Toltec 支援範圍(≤3.3.2),**不可安裝 Toltec**;OS 更新為 A/B 分割區切換,`/etc` 下的 systemd unit 會被清除(見 §4) |
| RAM | 1GB(可用約 800MB) | 充足 |
| `/`(系統分割區) | 255.7MB,僅剩 **20.5MB** | **禁止寫入任何檔案**,全部部署到 `/home/root/` |
| `/home`(資料分割區) | 6.6GB,可用 5.6GB | 所有程式、設定、快取放這裡 |
| init 系統 | systemd | 可掛常駐服務 |
| Shell 環境 | BusyBox 1.36(注意:`head -c` 等 GNU 選項不可用,無 `ldd`) | 部署腳本需用 BusyBox 相容語法 |
| 網路 | wlan0 已連線(DHCP),USB 網卡 10.11.99.1 | 裝置可直接對外呼叫 HTTPS API |
| CA 憑證 | `/etc/ssl/certs/ca-certificates.crt`(301 張,2026-05 更新) | Go 的 TLS 可直接使用系統憑證 |
| 系統時鐘 | `chronyd` NTP 自動校時,時間正確;**時區為 UTC**(非當地時間) | TLS 握手無時鐘問題;送 LLM 的當下時間取自這裡,時區設定見 §10.1 |
| 可用工具 | tar / gzip / scp / systemctl | 部署走 scp 即可 |

### 1.1 輸入裝置(實測 `/proc/bus/input/devices`)

| 裝置 | 節點 | 用途 |
|---|---|---|
| 電源鍵 | `/dev/input/event0` | 不使用 |
| **Wacom I2C Digitizer(筆)** | `/dev/input/event1` | 讀取筆劃(EV_ABS: X/Y/壓力/傾斜) |
| pt_mt(電容觸控) | `/dev/input/event2` | 觸發手勢的候選來源 |
| `/dev/uinput` | 存在(10,223) | 可建立虛擬筆裝置(P1 實測 xochitl 不認執行中新增的裝置,最終改直寫 event1,見 §8) |

Wacom 數位板座標系與螢幕像素座標不同(數位板約 20967×15725,螢幕 1404×1872,且軸向旋轉),注入時需做座標轉換,轉換參數需實機校正。

### 1.2 顯示子系統(關鍵限制)

- `/dev/fb0` 是 `mxs-lcdif`,虛擬尺寸 **260×23936 @ 32bpp** —— 這不是普通像素緩衝區,而是 e-ink 波形資料的傳輸通道。**rM2 的 e-ink 時序控制器(SWTCON)是 xochitl 內部的軟體實作**,第三方程式直接寫 fb0 無法顯示畫面。
- xochitl(PID 常駐,Qt6.8 + 專有 `libepaper.so` QPA plugin)持有 fb0 與所有輸入裝置。
- 社群的 rm2fb shim 是為舊韌體(2.x–3.3)+ Qt5 時代設計,**在 3.27.1/Qt6 上相容性未經驗證,風險高**。

### 1.3 筆記資料

- 位置:`/home/root/.local/share/remarkable/xochitl/`(實測 750 個項目)
- 筆劃檔為 **`.rm` v6 格式**(實測檔頭 `reMarkable .lines file, version=6`),v6 是 3.x 韌體的新格式,結構複雜(CRDT 基礎),社群有 Python 解析器(rmscene),Go 生態支援度低。
- 每本筆記有 `.thumbnails/`(低解析度縮圖)與 `.metadata` / `.content`(JSON,含頁面清單與最後開啟頁)。
- xochitl 的 heap 約 55MB,`/proc/<pid>/mem` root 可讀 → 理論上可用 reSnap 式「記憶體截圖」(此路線最終未採用,改為筆劃自渲染,見 §3 模組 B)。

---

## 2. 系統架構

採用 **「與 xochitl 共生」** 架構(不接管畫面、不碰 SWTCON、不裝任何 shim),核心思路借鏡社群 ghostwriter 專案:

```
┌─────────────────────────── reMarkable 2 ───────────────────────────┐
│                                                                     │
│  ┌──────────┐   筆事件      ┌─────────────────────────────────┐    │
│  │ event1    │ ───────────▶ │        rm2-scribe (Go, 常駐)     │    │
│  │ (Wacom筆) │              │                                  │    │
│  └──────────┘              │ 1. 筆劃監聽 + 停筆逾時觸發       │    │
│       ▲                     │ 2. 筆劃自渲染成 PNG              │    │
│       │ 注入筆事件           │ 3. 呼叫 LLM(影像→辨識+回覆)   │────┼──▶ Claude API
│       │ (直寫 event1)       │ 4. 筆劃合成(文字→單線字型)    │    │    (HTTPS)
│       │                     │ 5. 直寫注入(逐劃回放)         │    │
│       └──────────────────── └──────────────────────────────────┘    │
│                                                                     │
│  xochitl(原廠 UI)把注入的事件當成真筆,一筆一劃畫進目前的筆記頁  │
│  → 天然的「慢慢浮現」動畫,回覆並隨筆記正常存檔                    │
└─────────────────────────────────────────────────────────────────────┘
```

### 為什麼選這條路(對照被否決的方案)

| 方案 | 判定 | 原因 |
|---|---|---|
| 直接寫 /dev/fb0 | ❌ | rM2 的 fb0 非像素緩衝區(§1.2 實測證實) |
| rm2fb shim | ❌ | 3.27.1/Qt6 未驗證,且安裝需改動系統面,風險高 |
| 接管模式(自製 SWTCON) | ❌ | 工程量極大,e-ink 波形時序極難,且韌體更新即壞 |
| 呼叫官方手寫辨識 | ❌ | 裝置上無本地辨識引擎(實機搜尋證實),官方走私有雲端協定 |
| 解析 .rm v6 檔案 | 備援 | v6 格式複雜、Go 支援度低;僅在記憶體截圖失敗時作為備案 |
| **筆劃自渲染 + LLM 視覺辨識 + event1 直寫注入** | ✅ | 全程不動系統分割區、不依賴韌體私有元件、動畫效果(逐劃浮現)由 xochitl 原生渲染,天然可靠。(原定「記憶體截圖 + uinput」分別於 P2/P1 實測後修正,見 §3B 與 §8) |

**附帶優點:** LLM 的回覆是以「真筆劃」寫進筆記檔,會被 xochitl 正常存檔、同步、匯出 —— 對話紀錄自動保存在筆記本裡。

---

## 3. 模組設計

### 模組 A:觸發偵測(trigger)
- 監聽 `/dev/input/event1`(筆),採「停筆逾時」觸發:停筆 `idle_seconds`(預設 8 秒)後送出本次累積的筆劃。
- 可限定只在指定筆記本觸發(`notebook` 設定;讀 `xochitl.conf` 的 `LastOpen` 判斷目前筆記本)。
  **`notebook` 留空 = 停用服務**(閘門常閉,任何筆記本都不偵測)——刻意不採「留空 = 全部筆記本」,
  否則在任何一本筆記上寫字都會被吸走。
- 只做被動 read-only 監聽(不 grab),不干擾 xochitl 正常收筆劃。
- **點擊誤觸過濾**(`min_stroke_px`,預設 4 px):筆劃範圍框對角線小於門檻就丟棄,
  用來擋點工具列、手掌輕碰這類「碰一下就放開」的事件。被丟棄的筆劃**不重置停筆倒數**
  (否則想事情時碰一下螢幕就要重等一次 `idle_seconds`),丟棄數量隨批次回報進日誌。
  代價:門檻調太大會連句點、`i` 上面的點一起吃掉;`0` = 不過濾。
  註:這只能擋「碰一下」,**擋不掉選了橡皮擦或換一支筆之後在畫布上的動作**——
  xochitl 目前選用的工具沒有寫進任何我們讀得到的介面,原始事件與正常書寫完全相同。
- (原構想的「右下角手勢記號」觸發模式未實作,設定介面保留 `mode` 欄位。)

### 模組 B:筆劃擷取與渲染(capture / render)【2026-07-18 架構調整,已取代原記憶體截圖方案】
- **不截 xochitl 畫面**,改為即時監聽 `/dev/input/event1`,累積本次手寫的筆劃點(ABS_X/ABS_Y,以 BTN_TOUCH 分段成多筆 stroke)。
- 觸發後,把累積的筆劃(Wacom 座標)反轉換回螢幕像素座標,用 Go `image` + `image/png` 渲染成白底黑線 PNG。
- 只包含使用者「本次」手寫的內容,不含頁面舊筆跡/PDF 背景 → 送 LLM 更精準、更省 token。
- 決策理由:poc-read 已實證能完整讀到筆劃座標,零逆向、零 3.27.1 相依風險。
- **原記憶體截圖方案(掃 /proc/<pid>/mem 找 1404×1872 緩衝區)已否決**,僅在未來需要「辨識整頁既有內容(含 PDF)」時才作為選配重啟。

### 模組 C:LLM 呼叫(llm)
- 將 PNG 以 base64 附在多模態訊息中,一次完成「辨識手寫 + 生成回覆」(不需獨立 OCR 步驟)。
- 介面抽象為 `Provider`,依設定檔切換;**目前僅實作 Claude**(`POST https://api.anthropic.com/v1/messages`,model 預設 `claude-sonnet-5`),Gemini 保留介面未實作(設定會回報明確錯誤)。
- 純 `net/http` + 系統 CA,無第三方相依。
- 回覆長度上限、system prompt 皆為設定項。

### 模組 D:筆劃合成(strokes)【2026-08-20 改用 Hershey Script + 逐行動畫】
- **字型:Hershey Script**(`internal/font/hershey-script.jhf`,以 `go:embed` 內嵌後於啟動時解析)。
  先前自製過一套連筆草寫字型,實機辨識度不足而放棄——這套公有領域字型的字形品質有數十年的
  實際使用背書,大寫、數字、標點也一併涵蓋,不必再混用兩套字型。
  授權要求散布時附上出處聲明,見 `internal/font/HERSHEY-LICENSE.txt`。
- 度量(實測自資料,y 向下):x 字身 0..9、字母上伸部到 -12、下伸部到 21、字母間的連接高度 y=4。
  **字級以「字母與數字」為準(hAsc..hBase),行距與邊界則用涵蓋全部 96 個字符的 hTop(-16)..hDsc**
  ——`[ ( { | / #` 這些比字母高,只量小寫會讓它們戳進上一行(單元測試會擋)。
- **連筆是字型自己接的,排版不要插手**【2026-08-22 修正】。這套字型的小寫設計成
  「按自然前進寬度並排就會自己接上」:每個小寫都有一筆精確收在 `(rb, 4)`,而每個小寫的
  墨跡最左緣就落在自己的 `(lb, 4)`——放好之後前一個字的收筆點與後一個字的左緣**是同一個點**,
  高度自動一致。所以排版只做座標換算,不補引筆、不拉字距。
  - **`letter_spacing` 必須是 0**(預設值)。>0 會把上面那個接點拆開,而且拆開後每個字母
    露出各自不同的起筆高度(`a` 起於 (3,3)、`o` 起於 (0,0)、`e` 起於 (-3,7)、多數字母起於 (lb,4)),
    看起來就是「起筆點不同、基線不同、下緣未切齊」。這是實機回報過的症狀,不要再調大。
  - **不要替 `a c d e g o q w` 補引筆**。它們的第一筆確實起在字身內部,看起來「少了引筆」,
    但它們的左 bearing 就是字碗的最左緣、本來就沒有引筆的空間——補一條直線過去會**橫穿字碗**,
    在字中間畫出一道槓(試過,更難讀)。
  - **併筆只在座標完全重合時做**(`coincide`,容差 1e-6 px),不要用「夠近就接」的容差。
    容差一放寬,`n` `m` 字幹內部本來要提筆的地方(`(-5,9)` → `(-4,6)`,約 6.6px)會被硬接成
    一道回頭的斜線,小字級下就糊成一團。這是「黏在一起」的另一半原因。
  - **筆劃順序要重排**:`i` `j` 的點在第一筆、`t` 的橫槓與 `x` 的交叉筆在最後一筆,夾在字身
    中間會把連筆鏈打斷。`hGlyph.normalize` 把它們抽成 `marks`,由排版在**寫完一個單字後**才補
    ——人寫草寫也是先把字寫完再回頭點 `i`、劃 `t`。
  - 這些性質由 `TestLowercaseAbuts` / `TestLettersMeetOnDefaultTracking` 鎖住。
- 版面:**固定從頁面上方開始、往下逐行**,並在頂端留 `replyTopPad` 的空白。
  `LayoutCursive` 回傳「一行一組筆劃」,超出 `MaxY` 的部分不排並回報 `dropped`,由主程式記進日誌
  ——否則內容會無聲被截掉。
- **行距有下限**:`FontPx` 量的是字母上伸部頂到基線(21 單位),但字符實際佔 hTop..hDsc(37 單位),
  所以 `line_spacing` 低於 37/21≈1.76 時上下行會相碰。排版會把行距夾到不相碰為止而不是照做
  ——「句子維持固定行高」本來就要求行與行不互相穿插。
- **為什麼筆劃數是效能問題**:注入端每筆劃有約 23ms 的落筆/提筆固定開銷
  (見 `pen.strokeWithTool` 的懸浮漸近與離紙序列),所以一行要寫多久由筆劃數量決定,
  跟 `write_speed` 幾乎無關。
- 逐行動畫:一整行以最快速度注入(行內不留筆間間隔),行與行之間停 `line_pause` 秒。
- 字型檢視:`go run ./cmd/poc-font`(`-text` / `-size` / `-guides`)。

### 模組 E:筆事件注入(inject)【P1 修正:直寫真實節點】
- 以 `O_RDWR` 開啟 `/dev/input/event1` 直接 `write()` input_event,kernel 分發給 xochitl(`pen.OpenDirect()`,P1 實機驗證)。
- 原定 `/dev/uinput` 虛擬 Wacom 裝置方案已否決:xochitl 不列舉執行中新增的輸入裝置(`pen.New()` 保留供實驗)。
- 依模組 D 的序列逐事件回放;**回放速度即動畫速度**(可設定「書寫速度」,慢速回放 = 文字一筆一劃慢慢浮現)。
- 座標轉換(螢幕像素 → Wacom 座標系)公式已實機驗證,見 §8。

### 模組 F:主程式與設定(main/config)
- 常駐方式:systemd service(`/etc/systemd/system/rm2-scribe.service` 為唯一碰到 `/etc` 的檔案,單一小文字檔,可隨時 `disable + rm` 完全移除;若要做到 100% 不碰系統分割區,可改為手動 SSH 啟動——見待確認事項)。
- 設定檔:`/home/root/.config/rm2-scribe/config.toml`

- 設定範本以 `deploy/config.toml` 為準(provider / api_key / model / system_prompt / max_tokens、idle_seconds / notebook、write_speed / font_size_px / line_spacing / llm_fadeout / clear_mode、capture.method = "strokes")。

---

## 4. 部署規範(硬性約束)

1. **編譯:** `GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -ldflags="-s -w"` —— 32-bit armv7 靜態連結,已實機驗證。
2. **檔案位置:** 一律 `/home/root/` 之下:
   - 執行檔:`/home/root/rm2-scribe/rm2-scribe`
   - 設定:`/home/root/.config/rm2-scribe/config.toml`
   - 日誌:`/home/root/rm2-scribe/log/`(輪替,上限 10MB)
3. **絕不寫入** `/`、`/usr`、`/opt`(系統分割區僅剩 20.5MB;唯一例外是可選的 systemd unit 單檔)。
4. **不安裝** Toltec/opkg/任何套件管理器(韌體 3.27.1 超出支援範圍,有軟磚風險)。
5. **不修改、不重啟、不注入** xochitl(僅 read-only 讀其記憶體 + 透過 uinput 給它餵事件)。
6. 部署工具:`scp`(裝置端已有);解除安裝 = 刪除 `/home/root/rm2-scribe/` 即可。
7. **OS 更新後需重新部署 unit:** reMarkable OS 更新採 A/B 分割區切換,新系統分割區不含我們放的 `/etc/systemd/system/rm2-scribe.service`,服務會「消失」(2026-08-17 於 3.27.1 → 3.28.0 實證:unit 不見、`/home/root` 下執行檔/設定/筆記本皆完好、event1 仍為 Wacom、執行檔可直接啟動)。修復 = 重跑 `deploy/install.sh`。無法放進 `/home` 讓 systemd 自動找到,故此步驟無法免除。

## 5. 開發順序與風險驗證點(依風險由高至低)

| 階段 | 內容 | 驗證方式 | 風險 |
|---|---|---|---|
| P1 | **uinput 注入 PoC**:虛擬筆畫一條線,確認 xochitl 收到且螢幕顯示 | 實機看螢幕出現線條 | 中——若 xochitl 不認虛擬裝置,整個顯示方案要重議 |
| P2 | **筆劃擷取+自渲染 PoC**(原定記憶體截圖,實作時改道,見 §3B) | 存 PNG 傳回 Mac 目視比對 | 中 |
| P3 | 觸發偵測(讀 event1 判定手勢) | 實機畫記號觀察日誌 | 低 |
| P4 | LLM API 呼叫(影像→文字回覆) | 裝置上 curl 等價測試 + Go 實作 | 低 |
| P5 | 筆劃合成(文字→Hershey 路徑→排版) | 先在 Mac 上渲染預覽 | 低(CJK 除外) |
| P6 | 整合 + 設定檔 + 動畫調速 + 常駐 | 端到端實測 | 低 |

P1、P2 是本專案僅存的「可行性」風險,**先做這兩個 PoC,任一失敗即停下重新討論方案**。(結果:P1 於改用直寫 event1 後通過,見 §8;P2 於改用筆劃自渲染後通過,見 §3B。)

## 6. 使用者決議(2026-07-18 確認)

1. **回覆文字:英文**——使用 Hershey 單線字型;system prompt 要求 LLM 以英文回覆(LLM 仍可辨識中文手寫輸入)。
2. **觸發手勢:筆靜止逾時**——停筆 `idle_seconds`(預設 8 秒)後自動送出;僅在上次觸發後有新筆劃才會再次觸發,降低思考時誤觸發的影響。
3. **啟動方式:systemd 常駐**——接受在 `/etc/systemd/system/` 放置單一 unit 檔(約 300 bytes,可完全移除)。
4. **LLM:先接 Claude**(Anthropic Messages API),架構保留 Gemini 切換介面。

## 7. P1 PoC 設計附註(記錄於實測前;實測結論見 §8)

- 虛擬裝置以 `/dev/uinput` 建立,capabilities 於執行期從真實 Wacom(`/dev/input/event1`)以 `EVIOCGABS` 複製,不寫死數值。
- 座標假設(rmkit 慣例,待 P1 實測驗證):Wacom X 軸沿螢幕縱向反向、Y 軸沿螢幕橫向:
  `wacom_x = (1872 - screen_y) × 20967⁄1872`、`wacom_y = screen_x × 15725⁄1404`
- 測試圖形採用大寫字母「F」(兩軸皆不對稱),一次目視即可同時判定旋轉與鏡像是否正確。
- 已知風險:xochitl(Qt6 evdev)可能只在啟動時列舉輸入裝置。若注入無反應,備案為 `systemctl restart xochitl`(僅重啟原廠 UI,無資料風險)後保持虛擬裝置存活再測。

## 8. P1 結果(2026-07-18 實機通過 ✅)

- **關鍵決策:注入採「直接寫入真實節點」而非 uinput 虛擬裝置。**
  - uinput 建立的熱插拔虛擬筆,xochitl **不會**在執行中列舉(即使重啟 xochitl 也未成功顯示)。
  - 改用 `os.OpenFile("/dev/input/event1", O_RDWR)` 直接 `write()` input_event,kernel 經 `input_inject_event` 分發給所有讀取者(含 xochitl)→ **一次成功,F 方向完全正確**。
  - `pen.OpenDirect()` 為正式路徑;`pen.New()`(uinput)保留但預設不用。
- **座標公式驗證正確**(無需修改):
  `wacom_x = (1872 - screen_y) × 20966⁄1872`、`wacom_y = screen_x × 15725⁄1404`。
- **真筆事件實錄重點**(poc-read 錄得 16943 筆,pen.raw):
  - 落筆:`BTN_TOOL_PEN=1` → DISTANCE 由 ~86 遞減 → `BTN_TOUCH=1` 同時 PRESSURE 跳到 ~1600 → 續寫壓力 1600–3040。
  - 每個移動封包含 ABS_X/ABS_Y(+ 偶爾 DISTANCE)後接一個 SYN,事件間隔約 1.8ms(~550Hz)。
  - 提筆:PRESSURE→0、`BTN_TOUCH=0`、DISTANCE 回升、`BTN_TOOL_PEN=0`。
  - DrawStroke 已據此重寫落筆/提筆序列與壓力(改用實測級距 ~2200)。
- Wacom 軸範圍實測:X 0–20966、Y 0–15725、PRESSURE 0–4095。

## 9. 實作進度(2026-07-18)

| 模組 | 檔案 | 狀態 |
|---|---|---|
| A 筆劃監聽+停筆觸發 | `internal/input/reader.go` | ✅ P2 實機驗證 |
| B 筆劃→PNG 渲染 | `internal/render/render.go` | ✅ P2 實機(「Hi How are you?」正確) |
| C Claude LLM(net/http 零相依) | `internal/llm/claude.go` | ✅ 實機端到端驗證 |
| D 單線字型+排版 | `internal/font/{font,layout}.go` | ✅ 本機渲染驗證清楚 |
| E 直寫注入(畫筆/橡皮擦) | `internal/pen/uinput.go` | ✅ P1 實機驗證 |
| F 主程式+設定+systemd | `cmd/rm2-scribe/`, `deploy/` | ✅ 實機常駐運作 |
| 筆記本偵測(name↔uuid、頁面路徑) | `internal/xochitl/current.go` | ✅ 實機驗證 |
| G 網頁設定介面(HTTPS) | `internal/web/`, `internal/config/save.go` | ✅ 實機驗證(2026-08-18) |

**重要修正 — 讀寫共用節點的回授迴圈:** reader 與 injector 同開 `/dev/input/event1`,注入的回覆筆劃會被自己讀回、當成新手寫 → 無限迴圈。解法:`input.Reader.Mute()/Unmute()`,在讀取源頭(goroutine 內)丟棄靜音期間事件並清空累積,注入後留 300ms 讓事件流盡再解除。

**靜音範圍 — 整段互動連續靜音(2026-08-19 修正):** 原本只有「實際在注入」的那幾段靜音,等 LLM 回應(最長 90s)與等 `llm_fadeout` 這兩段空檔是開著的,使用者在等待期間寫的字會被記錄、成為下一批送出。改為**從吸收手寫開始一路靜音到擦除回覆結束**:

- `injCtl` 用巢狀計數持有靜音(`hold()` 回傳 release 函式,只有最外層放掉時才真的 `Unmute`)。`handle()` 一進來就取得外層 hold 並 `defer` 放掉,所以任何中途失敗的路徑都會恢復接收。
- `run()`(唯一的注入入口)自己也 hold 一次,因此不存在「忘了靜音就注入」的路徑。
- `llm_fadeout` 的等待從 goroutine 改為同步:靜音要持續到擦完,期間本來就收不到筆劃,主迴圈沒有別的事可做。
- 解除靜音前會 `ClearBuffer()`——靜音擋得住「讀回」,擋不住真實硬體,使用者這段期間寫的字仍會出現在畫面上,但刻意不算數。

代價要知道:那些字既不會被送出、也不會被吸收,就留在頁面上;除非剛好落在回覆的擦除範圍框內,會被 fadeout 的區域擦除一併擦掉。

**零相依決策:** LLM 呼叫用 `net/http` 直打 Anthropic Messages API,不引官方 SDK(維持 armv7 + CGO_ENABLED=0 靜態、最小體積)。

**3.28 韌體相容修正(2026-08-17):** OS 更新後程式「無反應且零日誌」。除了 systemd unit 被清除(§4-7),真正的靜默失效點是 `xochitl.conf` 的 `LastOpen` 值格式從純 uuid 變成 Qt QSettings 的 `@ByteArray(<uuid>)`,舊解析把整串當 uuid → 找不到 `.metadata` → 筆記本名稱為空 → 閘門常閉、筆劃全被丟棄。修正:`xochitl.parseLastOpen()` 剝除 `@Type(...)` 包裝(附單元測試),並在進入指定筆記本時記錄日誌,讓閘門狀態可觀測。

**網頁設定介面(2026-08-18 新增):** 裝置上開一個 HTTPS 服務讓瀏覽器改 `config.toml`。設計取捨:

- **認證與曝險:** 設定檔含 API key,所以對外綁定(非 127.0.0.1)而未設密碼時直接拒絕啟動;
  API key 只進不出(表單留空 = 不變)。憑證預設自簽(ECDSA P-256,armv7 上產生近乎瞬間;
  RSA-2048 要數秒),SAN 涵蓋 `remarkable.local` 與當下 IP,DHCP 換位址會自動重簽。
- **CSRF:** 所有寫入端點要求 `Content-Type: application/json`,瀏覽器對跨站的 JSON POST 會先送
  preflight,而服務不回任何 CORS 標頭 → 擋掉外部網頁偷送表單。
- **套用方式:** 寫檔後 `os.Exit(0)`,由 systemd `Restart=always` 帶新設定重啟(`RestartSec` 調成 2 秒)。
  比熱重載簡單可靠(LLM client、reader 逾時都會重新初始化)。**重啟前必須等 `injCtl.Busy()` 結束**,
  否則會在筆「按下」的狀態中途結束,xochitl 會卡住。
- **寫回設定檔:** `config.Save` 不重新序列化整份檔案,而是只換掉有變動的那一行(保留註解、對齊與
  `1.0` 這類寫法),並先留 `.bak`、以暫存檔 + rename 寫入。實機驗證:原值回存後檔案 md5 不變。

**端到端狀態:** 已於實機完成閉環驗證(手寫→停筆觸發→擦除吸收→LLM 回覆逐劃浮現→逾時自動擦除)。部署:`deploy/install.sh`(交叉編譯→scp→啟用 systemd,全落 /home/root)。

## 10. 日誌與疑難排解

程式的一切輸出都走 stdout/stderr,由 systemd 收進 journal。**最常用的一行:**

```sh
ssh rm2 'journalctl -u rm2-scribe -n 20 -f'
```

`-n 20` 先印最近 20 行、`-f` 接著即時跟隨(Ctrl-C 離開)。省略 `-f` 則只看歷史;`--no-pager` 適合用在腳本裡。

其他常用:

```sh
ssh rm2 'systemctl status rm2-scribe'          # 服務是否 enabled / active、目前 PID
ssh rm2 'journalctl -u rm2-scribe --since "10 min ago"'
ssh rm2 'systemctl restart rm2-scribe'         # 改完 config.toml 後套用
```

### 10.1 時間與時區

送給 LLM 的每個請求都會附上當下時間(取自裝置系統時鐘),所以裝置的時間設定會直接影響
「現在幾點/今天幾號」這類問題的答案。

**校時本身不用設定**:裝置以 `chronyd` 走 NTP 自動同步,連上 WiFi 就會校正
(`timedatectl` 顯示 `NTP service: active`、`System clock synchronized: yes`;
2026-08-19 實測與開發機逐秒相符)。

**但時區預設是 UTC**(`/etc/localtime → Universal`),不是當地時間 —— 時間點是對的,
只是差一個時區偏移。設定當地時區:

```sh
ssh rm2 'timedatectl set-timezone Asia/Taipei'
```

改完整台一致(本程式、`journalctl` 時間戳、xochitl 都會變成當地時間),不必重啟服務。

> ⚠️ `/etc/localtime` 在系統分割區,**OS 更新(A/B 切換)會把它打回 UTC**——症狀跟 §4-7
> 的 unit 消失一樣無聲,只是時間突然差幾個小時。OS 更新後要重下這行(和 `install.sh` 一起)。

一次正常的互動,日誌應依序出現:

```
rm2-scribe 啟動:model=… idle=8s notebook="…" fadeout=…s
等待手寫(停筆 8 秒後送出)…
進入筆記本 "…",開始接收手寫      ← 閘門已開(限定筆記本時才有)
擷取到 N 筆劃 / M 點               ← 停筆逾時已觸發(此後全程靜音)
吸收使用者手寫…
LLM 回覆:…
寫出回覆(N 筆)…
llm_fadeout 到,擦除回覆
本次互動結束,恢復接收手寫          ← 靜音解除,這之前寫的字都不算數
```

依「日誌卡在哪一行」對照:

| 症狀 | 判讀 | 處理 |
|---|---|---|
| `Unit rm2-scribe.service could not be found` | OS 更新換了系統分割區,unit 被清除(§4-7) | 重跑 `deploy/install.sh` |
| 只有前兩行,手寫無反應 | 閘門常閉:`config.toml` 的 `notebook` 名稱不符,或 `LastOpen` 格式又變(§9 的 3.28 修正) | 比對 `grep LastOpen /home/root/.config/remarkable/xochitl.conf` 與 `xochitl.parseLastOpen()` |
| 有「擷取到…」但無「LLM 回覆」 | API 呼叫失敗(key 未填/額度/連線) | 看該行後的錯誤訊息;確認裝置能連外 |
| 回覆文字寫出後又立刻被擦掉 | `llm_fadeout` 太短 | 調大或設 `0`(不消失) |
| 注入的字被當成新手寫、無限迴圈 | Mute/Unmute 失效(§9) | 檢查 `input.Reader` 的靜音期與注入時序 |
