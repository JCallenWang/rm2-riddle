#!/bin/sh
# 部署 rm2-scribe 到 reMarkable 2(於 Mac 執行)。
# 預設走 USB 連線 root@10.11.99.1;WiFi 或 ssh alias 可用 RM2_HOST 覆寫,例如:
#   RM2_HOST=rm2 ./deploy/install.sh
# 一切檔案落在 /home/root,唯一碰到系統分割區的是 systemd unit 一檔——
# reMarkable OS 更新(A/B 分割區切換)會清掉它,更新後重跑本腳本即可。BusyBox 相容。
set -e
DEV="${RM2_HOST:-root@10.11.99.1}"
HERE=$(dirname "$0")
ROOT=$(cd "$HERE/.." && pwd)

echo "== 交叉編譯 armv7 =="
cd "$ROOT"
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o build/rm2-scribe ./cmd/rm2-scribe

echo "== 停止舊程式(Linux 不可覆蓋執行中的執行檔)=="
ssh "$DEV" 'systemctl stop rm2-scribe 2>/dev/null; killall rm2-scribe 2>/dev/null; true'

echo "== 部署到 /home/root =="
ssh "$DEV" 'mkdir -p /home/root/rm2-scribe /home/root/.config/rm2-scribe'
scp build/rm2-scribe "$DEV":/home/root/rm2-scribe/
scp "$ROOT/deploy/rm2-scribe.service" "$DEV":/etc/systemd/system/

# 確認裝置上的檔案與本機一致
LOCAL_MD5=$(md5 -q build/rm2-scribe 2>/dev/null || md5sum build/rm2-scribe | cut -d' ' -f1)
REMOTE_MD5=$(ssh "$DEV" 'md5sum /home/root/rm2-scribe/rm2-scribe' | cut -d' ' -f1)
if [ "$LOCAL_MD5" != "$REMOTE_MD5" ]; then
  echo "!! 部署失敗:裝置上的執行檔與本機不一致(md5 不符)" >&2
  exit 1
fi

# 只在設定檔不存在時才複製,避免覆蓋使用者填好的 api_key
if ssh "$DEV" '[ ! -f /home/root/.config/rm2-scribe/config.toml ]'; then
  scp "$ROOT/deploy/config.toml" "$DEV":/home/root/.config/rm2-scribe/config.toml
  echo "!! 已放置設定範本,請編輯 /home/root/.config/rm2-scribe/config.toml 填入 api_key"
fi

echo "== 啟用 systemd 服務 =="
ssh "$DEV" 'chmod +x /home/root/rm2-scribe/rm2-scribe && systemctl daemon-reload && systemctl enable --now rm2-scribe && systemctl --no-pager status rm2-scribe | head -n 5'
echo "== 完成 =="
