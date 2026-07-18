#!/bin/sh
# 部署 rm2-scribe 到 reMarkable 2(於 Mac 執行;裝置 USB IP 10.11.99.1)。
# 一切檔案落在 /home/root,不碰系統分割區。BusyBox 相容。
set -e
DEV=root@10.11.99.1
HERE=$(dirname "$0")
ROOT=$(cd "$HERE/.." && pwd)

echo "== 交叉編譯 armv7 =="
cd "$ROOT"
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o build/rm2-scribe ./cmd/rm2-scribe

echo "== 部署到 /home/root =="
ssh "$DEV" 'mkdir -p /home/root/rm2-scribe /home/root/.config/rm2-scribe'
scp build/rm2-scribe "$DEV":/home/root/rm2-scribe/
scp "$ROOT/deploy/rm2-scribe.service" "$DEV":/etc/systemd/system/

# 只在設定檔不存在時才複製,避免覆蓋使用者填好的 api_key
ssh "$DEV" '[ -f /home/root/.config/rm2-scribe/config.toml ] || echo NEED_CONFIG'
if ssh "$DEV" '[ ! -f /home/root/.config/rm2-scribe/config.toml ]'; then
  scp "$ROOT/deploy/config.toml" "$DEV":/home/root/.config/rm2-scribe/config.toml
  echo "!! 已放置設定範本,請編輯 /home/root/.config/rm2-scribe/config.toml 填入 api_key"
fi

echo "== 啟用 systemd 服務 =="
ssh "$DEV" 'chmod +x /home/root/rm2-scribe/rm2-scribe && systemctl daemon-reload && systemctl enable --now rm2-scribe && systemctl --no-pager status rm2-scribe | head -n 5'
echo "== 完成 =="
