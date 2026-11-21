// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"mime"
	"testing"
)

func TestAttachment(t *testing.T) {
	for _, name := range []string{"app.zip", `evil".exe`, "a\r\nSet-Cookie: x", "Grüße & Co.tar.gz", "../../etc/passwd"} {
		v := attachment(name)
		disp, params, err := mime.ParseMediaType(v)
		if err != nil || disp != "attachment" {
			t.Errorf("%q → %q: %v", name, v, err)
			continue
		}
		got := params["filename"]
		for _, bad := range []string{"\r", "\n", "/"} {
			if contains(got, bad) {
				t.Errorf("%q → filename %q contains %q", name, got, bad)
			}
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
