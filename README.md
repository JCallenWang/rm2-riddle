# rm2-scribe

reMarkable 2 上的手寫助理:在指定筆記本裡手寫問題,停筆數秒後,你的字會被「吸收」(擦除),送給遠端 LLM(Claude)辨識並回覆,回覆以手寫筆跡逐劃浮現在頁面上,經設定秒數後自動消失。

全程與官方 `xochitl` 共生——不改畫面驅動、不裝第三方套件管理器、不接管顯示,只透過注入筆事件與讀取設定達成。

> **緣起:** 本專案根據 [MaximeRivest/riddle](https://github.com/MaximeRivest/riddle) 透過 AI 改編而成。原專案僅支援 reMarkable Paper Pro(aarch64,以插入 vendor 函式庫實作);本專案是 reMarkable 2(armv7)版本,以完全不同的架構重新實作(讀取/注入 `/dev/input/event1` 筆事件,不觸碰任何私有函式庫)。

## 運作流程

```
在指定筆記本手寫 → 停筆 idle_seconds 秒
  → 吸收(擦除)你的手寫,表示已讀取
  → 渲染成 PNG,送 Claude 視覺辨識 + 回覆(英文)
  → 以單線字型把回覆逐劃寫回頁面下方
  → 經 llm_fadeout 秒後自動擦除回覆
關閉指定筆記本 → 清除記錄並刪除頁面內容(徹底清空)
```

## 硬體 / 環境需求

- **reMarkable 2**(armv7 32-bit;本專案不支援 Paper Pro 的 aarch64)
- 韌體 3.x(實測 3.27.1、3.28.0;**勿安裝 Toltec**,超出其支援範圍有軟磚風險)
- 可 SSH 連上裝置:USB(`root@10.11.99.1`)或 WiFi(mDNS 名稱 `remarkable.local`,建議在 `~/.ssh/config` 設一個 `rm2` alias)
- 開發端:Go(交叉編譯,`GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0`)
- 一組 Anthropic API key

所有檔案部署於 `/home/root`,與系統分割區分離。

## 建置與部署

```sh
# 一鍵:交叉編譯 → 停舊程式 → scp 到 /home/root → 啟用 systemd 服務
./deploy/install.sh                 # 預設 USB 連線 root@10.11.99.1
RM2_HOST=rm2 ./deploy/install.sh    # 走 WiFi / ssh alias
```

> **OS 更新後程式失效?** reMarkable 的 OS 更新是 A/B 分割區切換,會清掉系統分割區上的 systemd unit(`/etc/systemd/system/rm2-scribe.service`),服務因此消失;`/home/root` 下的執行檔與設定(含 api_key)不受影響。**重跑一次 `install.sh` 即可恢復**(已於 3.27.1 → 3.28.0 更新實測)。

或手動:

```sh
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o build/rm2-scribe ./cmd/rm2-scribe
ssh root@10.11.99.1 'killall -9 rm2-scribe 2>/dev/null; sleep 2'   # 先停舊程式(Linux 不可覆蓋執行中的檔案)
scp build/rm2-scribe root@10.11.99.1:/home/root/rm2-scribe/
scp deploy/rm2-scribe.service root@10.11.99.1:/etc/systemd/system/
ssh root@10.11.99.1 'systemctl daemon-reload && systemctl enable --now rm2-scribe'
```

> ⚠️ 部署務必「先停程式、再 scp」。Linux 不允許覆蓋執行中的執行檔(ETXTBSY),
> 否則 scp 會靜默失敗、裝置仍跑舊版。可用 md5 比對本機與裝置確認。

## 設定

`/home/root/.config/rm2-scribe/config.toml`(範本:`deploy/config.toml`):

```toml
[llm]
provider = "claude"          # 目前實作 claude;架構保留 gemini
api_key  = ""                # ← 填入你的 Anthropic API key
model    = "claude-sonnet-5"
system_prompt = "..."        # 建議要求 LLM 用英文簡短回覆
max_tokens = 500

[trigger]
mode = "idle_timeout"        # 停筆逾時觸發
idle_seconds = 8             # 停筆 N 秒後送出
notebook = "Riddle"          # 只在這本筆記本回應;留空 "" = 所有筆記本

[animation]
write_speed = 1.0            # 回放速度倍率,越小寫得越慢(浮現越慢)
font_size_px = 44
line_spacing = 1.5
llm_fadeout = 30             # 回覆顯示 N 秒後自動擦除;0 = 不消失
clear_mode = "region"        # region=只清內容範圍(快、乾淨) | page=整頁(過慢會失效)
```

改完設定重啟:`ssh root@10.11.99.1 'systemctl restart rm2-scribe'`

## 網頁設定介面(選用)

不想每次都 SSH 進去改設定的話,可以開啟裝置上的網頁介面:用瀏覽器(手機也行)編輯設定、
測試 API key、看即時日誌、重啟服務。介面走 HTTPS,預設關閉。

編輯 `config.toml` 的 `[web]` 區塊後重啟服務:

```toml
[web]
enabled   = true
listen    = "0.0.0.0:8443"   # 同網段可連;改成 127.0.0.1:8443 則只能本機
password  = "自己設一組"      # 帳號固定 admin;對區網開放時必填
cert_file = ""               # 留空 = 自動產生自簽憑證
key_file  = ""
```

接著開 `https://remarkable.local:8443/`。

- **設定檔裡有 API key**,所以對外開放(非 127.0.0.1)卻沒設密碼時,介面會拒絕啟動並在日誌說明。
- API key 永遠不會回傳給瀏覽器;表單留空 = 維持原值。
- 憑證是自動產生的自簽憑證(SAN 涵蓋 `remarkable.local` 與裝置當下的 IP,DHCP 換位址會自動重簽),
  瀏覽器第一次會跳安全警告,點「進階 → 繼續前往」即可;想要沒有警告就用 `cert_file`/`key_file` 自備憑證。
- 想完全不對區網開放,就綁 `127.0.0.1:8443` 再用 SSH 轉發:

  ```sh
  ssh -L 8443:localhost:8443 rm2
  ```

  然後開 `https://localhost:8443/`,加密與認證都由 SSH 負責。
- 按「儲存並套用」= 寫回 `config.toml` 後結束程式,由 systemd 重新拉起載入新設定
  (正在寫回覆時會等筆劃寫完才重啟,避免筆停在落下狀態)。寫檔前會留一份 `config.toml.bak`。

本機調版面用:`go run ./cmd/poc-web`(不需要裝置)。

## 使用

1. 在 reMarkable 建立(或指定)一本筆記本,名稱與 `config.toml` 的 `notebook` 一致
2. 進入該筆記本,手寫一個英文問題,停筆等 `idle_seconds` 秒
3. 你的字被擦除 → 回覆逐劃浮現 → `llm_fadeout` 秒後消失

## 專案結構

```
cmd/rm2-scribe/      主程式(串接全流程)
cmd/poc-*/           各階段驗證用 PoC(注入、讀取、擷取、字型、擦除、網頁介面本機預覽)
internal/input/      監聽筆劃、停筆逾時聚合、mute/gate/clear
internal/render/     筆劃 → PNG
internal/llm/        Anthropic Messages API(net/http 零相依)
internal/font/       單線向量字型 + 排版
internal/pen/        直寫 /dev/input/event1 注入(畫筆 / 橡皮擦)
internal/xochitl/    偵測目前筆記本、name↔uuid、頁面路徑、筆記本清單
internal/web/        網頁設定介面(HTTPS + 自簽憑證,零相依 net/http)
internal/config/     零相依 TOML 子集解析
deploy/              config 範本、systemd unit、install.sh
```

## 實作要點(踩過的坑)

- **注入須直寫真實節點** `/dev/input/event1`。uinput 建立的熱插拔虛擬筆,xochitl 只在啟動時列舉,執行中不認。
- **注入須避開左側工具列**(x ≥ 170)。落在工具列上會被當成點按 UI,導致切換工具、擦不掉字。
- **擦除用單一連續蛇形筆劃**。逐條掃描線各自落筆/提筆會有工具切換競態,偶爾把某條擦除線畫成黑線。
- **橡皮擦壓力用 ~2200**。軸最大值 4095 超出真筆範圍,會被 xochitl 忽略。
- **座標轉換**(螢幕 1404×1872 → Wacom):`wacom_x = (1872 − screen_y) × 20966⁄1872`、`wacom_y = screen_x × 15725⁄1404`。
- **目前筆記本判斷**:讀 `xochitl.conf` 的 `LastOpen`(即時更新);`.metadata` 的 `lastOpened` 不即時,不可靠。值的格式隨韌體而異:3.27 是純 uuid,3.28 起是 `@ByteArray(<uuid>)`(Qt QSettings 序列化),程式兩者皆相容——3.28 更新後「完全沒反應、日誌只有啟動行」就是這個格式改變造成閘門常閉。
- **關閉筆記本徹底清空**:刪除該筆記本的頁面 `.rm` 檔(保留 `.content` 結構),須在筆記本關閉後進行。

更完整的架構決策與各階段驗證記錄見 [`DEVELOPMENT.md`](DEVELOPMENT.md)。

## 移除

```sh
ssh root@10.11.99.1 'systemctl disable --now rm2-scribe; rm -rf /home/root/rm2-scribe /home/root/.config/rm2-scribe /etc/systemd/system/rm2-scribe.service; systemctl daemon-reload'
```

## 授權

MIT。本專案為 [MaximeRivest/riddle](https://github.com/MaximeRivest/riddle)(MIT)的 reMarkable 2 改編版,`LICENSE` 保留原作者的著作權宣告。
