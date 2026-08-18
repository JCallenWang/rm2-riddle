// Package xochitl 讀取 xochitl 的即時狀態。
// 目前用途:判斷「現在打開的是哪一本筆記本」,以便把助理限定在專屬筆記本。
//
// 訊號來源:/home/root/.config/remarkable/xochitl.conf 的 LastOpen。
// 實測(2026-07-18)此值在「打開筆記本當下」即時更新,回到檔案清單時清空;
// 比 .metadata 的 lastOpened 可靠(後者不會即時更新)。
//
// 值的格式隨韌體版本不同:3.27 為純 uuid(LastOpen=<uuid>),
// 3.28 起以 Qt QSettings 的 QByteArray 形式儲存(LastOpen=@ByteArray(<uuid>),
// 清空時為 LastOpen=@ByteArray())。parseLastOpen 兩者皆相容。
package xochitl

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	confPath = "/home/root/.config/remarkable/xochitl.conf"
	dataDir  = "/home/root/.local/share/remarkable/xochitl"
)

var (
	visibleNameRe = regexp.MustCompile(`"visibleName"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	// Qt QSettings 型別包裝,例如 @ByteArray(...)、@Variant(...)。
	qtWrapperRe = regexp.MustCompile(`^@[A-Za-z]+\((.*)\)$`)
)

// CurrentUUID 回傳目前打開的文件 UUID;無(在檔案清單)時回傳空字串。
func CurrentUUID() string {
	f, err := os.Open(confPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "LastOpen=") {
			return parseLastOpen(strings.TrimPrefix(line, "LastOpen="))
		}
	}
	return ""
}

// parseLastOpen 把 xochitl.conf 中 LastOpen 的原始值正規化為 uuid:
// 剝除 Qt 型別包裝(@ByteArray(...))與引號、去除空白;無內容回傳空字串。
func parseLastOpen(raw string) string {
	v := strings.TrimSpace(raw)
	if m := qtWrapperRe.FindStringSubmatch(v); m != nil {
		v = m[1]
	}
	return strings.TrimSpace(strings.Trim(v, `"`))
}

// CurrentName 回傳目前打開筆記本的可見名稱;無則回傳空字串。
func CurrentName() string {
	uuid := CurrentUUID()
	if uuid == "" {
		return ""
	}
	return nameOf(uuid)
}

// UUIDByName 掃描所有 .metadata,回傳 visibleName 相符且未刪除的文件 UUID;
// 找不到回傳空字串。用於在筆記本已關閉(LastOpen 空)時仍能定位其檔案。
func UUIDByName(name string) string {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".metadata") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dataDir, n))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), `"deleted": true`) {
			continue
		}
		m := visibleNameRe.FindSubmatch(data)
		if m != nil && strings.ReplaceAll(string(m[1]), `\"`, `"`) == name {
			return strings.TrimSuffix(n, ".metadata")
		}
	}
	return ""
}

// ListNotebooks 回傳裝置上所有可見筆記本(DocumentType、未刪除)的名稱,已排序去重。
// 供設定介面列出可選的筆記本,避免手動輸入打錯名稱。
func ListNotebooks() []string {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".metadata") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dataDir, n))
		if err != nil {
			continue
		}
		s := string(data)
		if strings.Contains(s, `"deleted": true`) || !strings.Contains(s, `"type": "DocumentType"`) {
			continue
		}
		// 在垃圾桶裡的不列出
		if strings.Contains(s, `"parent": "trash"`) {
			continue
		}
		m := visibleNameRe.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		name := strings.ReplaceAll(m[1], `\"`, `"`)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// PageDir 回傳某文件的頁面筆劃檔目錄。
func PageDir(uuid string) string { return filepath.Join(dataDir, uuid) }

// nameOf 由 UUID 讀取 .metadata 的 visibleName。
func nameOf(uuid string) string {
	data, err := os.ReadFile(filepath.Join(dataDir, uuid+".metadata"))
	if err != nil {
		return ""
	}
	m := visibleNameRe.FindSubmatch(data)
	if m == nil {
		return ""
	}
	return strings.ReplaceAll(string(m[1]), `\"`, `"`)
}
