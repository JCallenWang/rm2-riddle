package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Save 把設定寫回 TOML 檔。
//
// 這個檔案裝著使用者的 API key 與手寫的註解,所以不做「重新序列化整份檔案」——
// 既有的 key 只換掉值(保留註解與對齊),缺少的 key 追加到對應 section 末尾,
// section 不存在才新增。寫入採「暫存檔 + rename」以避免中途失敗留下半份設定,
// 並先留一份 .bak。
func Save(path string, c Config) error {
	orig, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	pending := fields(c)
	lines := strings.Split(string(orig), "\n")
	section := ""
	// sectionEnd 記錄每個 section 最後一行有內容的位置(用於追加缺少的 key)
	sectionEnd := map[string]int{}
	order := []string{}

	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			section = strings.ToLower(strings.TrimSpace(t[1 : len(t)-1]))
			if _, ok := sectionEnd[section]; !ok {
				order = append(order, section)
			}
			sectionEnd[section] = i
			continue
		}
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		sectionEnd[section] = i
		key := strings.TrimSpace(line[:eq])
		for j := range pending {
			if pending[j].done || pending[j].section != section || pending[j].key != key {
				continue
			}
			pending[j].done = true
			// 值沒變就整行不動:保留使用者原本的寫法(1.0 不會被改成 1)、
			// 對齊與空白,讓每次存檔的 diff 只剩真正改動的項目。
			if !sameValue(line[eq+1:], pending[j].val) {
				lines[i] = replaceValue(line, pending[j].val)
			}
			break
		}
	}

	// 追加原檔沒有的 key(由後往前插入,避免行號位移)
	type insert struct {
		at    int
		lines []string
	}
	var inserts []insert
	for _, sec := range order {
		var add []string
		for j := range pending {
			if !pending[j].done && pending[j].section == sec {
				add = append(add, fmt.Sprintf("%s = %s", pending[j].key, pending[j].val))
				pending[j].done = true
			}
		}
		if len(add) > 0 {
			inserts = append(inserts, insert{at: sectionEnd[sec] + 1, lines: add})
		}
	}
	for i := len(inserts) - 1; i >= 0; i-- {
		in := inserts[i]
		lines = append(lines[:in.at], append(append([]string{}, in.lines...), lines[in.at:]...)...)
	}

	// 仍未寫出的 → 該 section 不存在,追加到檔尾
	var tail []string
	lastSec := ""
	for j := range pending {
		if pending[j].done {
			continue
		}
		if pending[j].section != lastSec {
			tail = append(tail, "", "["+pending[j].section+"]")
			lastSec = pending[j].section
		}
		tail = append(tail, fmt.Sprintf("%s = %s", pending[j].key, pending[j].val))
	}
	if len(tail) > 0 {
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, tail...)
		lines = append(lines, "")
	}

	out := strings.Join(lines, "\n")
	if len(orig) > 0 {
		_ = os.WriteFile(path+".bak", orig, 0o600)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type field struct {
	section, key, val string
	done              bool
}

// fields 列出所有會寫回檔案的 key。新增設定項時這裡和 Load 的 switch 都要加,
// 漏一邊會變成「網頁按了儲存卻沒生效」——TestSaveCoversEveryConfigField 會擋下這種漏改。
func fields(c Config) []field {
	return []field{
		{section: "llm", key: "provider", val: quote(c.LLM.Provider)},
		{section: "llm", key: "api_key", val: quote(c.LLM.APIKey)},
		{section: "llm", key: "model", val: quote(c.LLM.Model)},
		{section: "llm", key: "system_prompt", val: quote(c.LLM.SystemPrompt)},
		{section: "llm", key: "max_tokens", val: strconv.Itoa(c.LLM.MaxTokens)},
		{section: "trigger", key: "mode", val: quote(c.Trigger.Mode)},
		{section: "trigger", key: "idle_seconds", val: num(c.Trigger.IdleSeconds)},
		{section: "trigger", key: "notebook", val: quote(c.Trigger.Notebook)},
		{section: "trigger", key: "min_stroke_px", val: num(c.Trigger.MinStrokePx)},
		{section: "animation", key: "write_speed", val: num(c.Animation.WriteSpeed)},
		{section: "animation", key: "font_size_px", val: num(c.Animation.FontSizePx)},
		{section: "animation", key: "line_spacing", val: num(c.Animation.LineSpacing)},
		{section: "animation", key: "llm_fadeout", val: num(c.Animation.LLMFadeout)},
		{section: "animation", key: "clear_mode", val: quote(c.Animation.ClearMode)},
		{section: "animation", key: "line_pause", val: num(c.Animation.LinePause)},
		{section: "animation", key: "pen_pressure", val: num(c.Animation.PenPressure)},
		{section: "animation", key: "letter_spacing", val: num(c.Animation.LetterSpacing)},
		{section: "capture", key: "method", val: quote(c.Capture.Method)},
		{section: "web", key: "enabled", val: strconv.FormatBool(c.Web.Enabled)},
		{section: "web", key: "listen", val: quote(c.Web.Listen)},
		{section: "web", key: "password", val: quote(c.Web.Password)},
		{section: "web", key: "cert_file", val: quote(c.Web.CertFile)},
		{section: "web", key: "key_file", val: quote(c.Web.KeyFile)},
	}
}

// sameValue 判斷原本那行的值與要寫入的值是否等價(數字比數值,其餘比去引號後的字串)。
func sameValue(oldRaw, newVal string) bool {
	o := strings.TrimSpace(stripComment(oldRaw))
	n := strings.TrimSpace(newVal)
	if o == n {
		return true
	}
	of, err1 := strconv.ParseFloat(strings.TrimSpace(unquote(o)), 64)
	nf, err2 := strconv.ParseFloat(strings.TrimSpace(unquote(n)), 64)
	if err1 == nil && err2 == nil {
		return of == nf
	}
	return unquote(o) == unquote(n)
}

// replaceValue 換掉一行的值,保留 key 側的排版與行尾註解(盡量維持註解原本的欄位)。
func replaceValue(line, newVal string) string {
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return line
	}
	head := line[:eq+1]
	rest := line[eq+1:]
	ci := commentIndex(rest)
	out := head + " " + newVal
	if ci < 0 {
		return out
	}
	comment := strings.TrimRight(rest[ci:], " \t")
	col := len(head) + ci // 註解原本的起始欄
	if len(out) < col {
		out += strings.Repeat(" ", col-len(out))
	} else {
		out += "  "
	}
	return out + comment
}

// commentIndex 回傳引號外第一個 '#' 的位置;沒有則 -1。跳過 \\" 這類跳脫序列。
func commentIndex(s string) int {
	inQ := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if inQ && i+1 < len(s) {
				i++
			}
		case '"':
			inQ = !inQ
		case '#':
			if !inQ {
				return i
			}
		}
	}
	return -1
}

func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}

func num(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }
