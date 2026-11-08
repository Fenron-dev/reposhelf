// SPDX-License-Identifier: GPL-3.0-or-later

package buildinfo

import "testing"

func TestString(t *testing.T) {
	origVersion, origCommit := Version, Commit
	t.Cleanup(func() { Version, Commit = origVersion, origCommit })

	tests := []struct {
		name, version, commit, want string
	}{
		{"dev build", devVersion, "", "dev"},
		{"release with commit", "v0.1.0", "1d00f8b", "v0.1.0 (1d00f8b)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version, Commit = tt.version, tt.commit
			if got := String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
