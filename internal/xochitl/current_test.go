package xochitl

import "testing"

func TestParseLastOpen(t *testing.T) {
	const uuid = "641c1adb-a36b-474d-b2da-72f05c11e9db"
	cases := []struct {
		name, raw, want string
	}{
		{"3.27 plain uuid", uuid, uuid},
		{"3.27 empty", "", ""},
		{"3.28 ByteArray wrapper", "@ByteArray(" + uuid + ")", uuid},
		{"3.28 ByteArray empty (file list)", "@ByteArray()", ""},
		{"surrounding whitespace", "  @ByteArray(" + uuid + ")  ", uuid},
		{"quoted", `"` + uuid + `"`, uuid},
		{"other Qt wrapper", "@Variant(" + uuid + ")", uuid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseLastOpen(c.raw); got != c.want {
				t.Errorf("parseLastOpen(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}
