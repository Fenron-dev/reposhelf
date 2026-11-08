// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // substring, empty = expect no output
		wantStderr string // substring, empty = expect no output
	}{
		{"no args shows usage", nil, ExitOK, "Aufruf:", ""},
		{"help", []string{"help"}, ExitOK, "Aufruf:", ""},
		{"--help", []string{"--help"}, ExitOK, "Aufruf:", ""},
		{"version", []string{"version"}, ExitOK, "reposhelf dev", ""},
		{"--version", []string{"--version"}, ExitOK, "reposhelf dev", ""},
		{"unknown command", []string{"frobnicate"}, ExitUsage, "", `Unbekannter Befehl: "frobnicate"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(tt.args, &stdout, &stderr)

			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			checkOutput(t, "stdout", stdout.String(), tt.wantStdout)
			checkOutput(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

func checkOutput(t *testing.T, stream, got, want string) {
	t.Helper()
	if want == "" {
		if got != "" {
			t.Errorf("%s = %q, want empty", stream, got)
		}
		return
	}
	if !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", stream, got, want)
	}
}
