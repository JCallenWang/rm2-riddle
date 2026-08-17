# CLAUDE.md

rm2-scribe:在 reMarkable 2 上讀取手寫、送 Claude 視覺辨識+回覆、以筆跡注入讓回覆逐字浮現。詳細架構與實機驗證紀錄見 `DEVELOPMENT.md`。

## 開發約定

- **Commit 訊息一律用英文**。
- 不 commit `build/`(二進位會內嵌本機路徑)。

## 建置與部署

- 交叉編譯 + 部署到裝置:`./deploy/install.sh`(預設 USB `root@10.11.99.1`;WiFi 用 `RM2_HOST=rm2 ./deploy/install.sh`,`rm2` 為 `~/.ssh/config` alias → `remarkable.local`)
- OS 更新(A/B 分割區切換)會清掉 `/etc/systemd/system/rm2-scribe.service`,程式因此「失效」;`/home/root` 不受影響,重跑 `install.sh` 即恢復。
- 手動編譯:`GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o build/rm2-scribe ./cmd/rm2-scribe`
- 零外部相依(標準庫 only),必須保持靜態連結——裝置是 armv7l 32-bit、無套件管理。

## 裝置硬約束(reMarkable 2, 韌體 3.27.1 / 3.28.0 實測)

- **不可裝 Toltec**(韌體超出支援範圍,軟磚風險);一切檔案放 `/home/root/`,禁碰系統分割區。
- BusyBox shell:無 `pkill`(用 `killall`)、`head -c` 不支援(用 `dd`)。
- 不寫 fb0、不裝 rm2fb——與 xochitl 共生,回覆靠注入筆事件到 `/dev/input/event1` 讓 xochitl 自己渲染。
- uinput 虛擬裝置 xochitl 不認,注入必須直寫真實節點 event1。
- reader 與 injector 共用 event1:注入前必須 `input.Reader.Mute()`,否則回覆被讀回成新手寫造成無限迴圈。

## 模組地圖

- `internal/input` 筆事件監聽(含 Mute/Unmute)
- `internal/render` 筆劃自渲染成 PNG(送 LLM 用)
- `internal/llm` Claude API(net/http 手刻,不用 SDK)
- `internal/font` 單線字型+排版(回寫用)
- `internal/pen` event1 筆事件注入
- `cmd/rm2-scribe` 主程式;`cmd/poc-*` 各階段實機驗證用 POC
- `deploy/` config 範本、systemd unit、部署腳本
