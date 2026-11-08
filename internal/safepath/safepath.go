// SPDX-License-Identifier: GPL-3.0-or-later

// Package safepath turns names that come from outside – host, owner and
// repository names, tags, release and asset names – into file system paths
// (AGENTS.md invariant 4, KONZEPT 5.1). A name is never used as a path before
// it has passed CheckSegment.
//
// The rules are the same on every operating system: the data folder is
// portable and may move between macOS, Linux and Windows (e.g. on a USB
// drive), so a name that only Windows rejects is rejected everywhere.
//
// Symbolic links inside the data folder are followed deliberately: the user
// owns that folder and may move parts of it to another disk. Network names
// can never create such links, because they cannot contain separators.
//
// The package depends only on the standard library and atomicfile.
package safepath

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
)

// MaxSegmentBytes is the common file name limit of file systems (ext4, APFS;
// NTFS counts UTF-16 units, which is never stricter for the same name).
const MaxSegmentBytes = 255

// windowsForbidden are characters Windows does not allow in file names, in
// addition to separators and control characters.
const windowsForbidden = `<>:"|?*`

// ErrInvalid is wrapped by every error of this package.
var ErrInvalid = errors.New("invalid path segment")

// SegmentError describes why a name cannot be used as a path segment.
type SegmentError struct {
	Segment string
	Problem string
}

func (e *SegmentError) Error() string {
	return fmt.Sprintf("path segment %q: %s", e.Segment, e.Problem)
}

func (e *SegmentError) Unwrap() error { return ErrInvalid }

// windowsReserved are device names Windows refuses as file names, also with
// any extension ("con.txt") and in any case. COM and LPT include the
// superscript digits that Windows treats like 1–3.
var windowsReserved = func() map[string]bool {
	m := map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true,
		"CONIN$": true, "CONOUT$": true}
	for _, d := range []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³"} {
		m["COM"+d] = true
		m["LPT"+d] = true
	}
	return m
}()

// CheckSegment returns a *SegmentError if s cannot safely be used as one
// file or directory name, and nil otherwise.
func CheckSegment(s string) error {
	if p := problem(s); p != "" {
		return &SegmentError{Segment: s, Problem: p}
	}
	return nil
}

func problem(s string) string {
	switch {
	case s == "":
		return "must not be empty"
	case s == "." || s == "..":
		return "must not be . or .."
	case len(s) > MaxSegmentBytes:
		return fmt.Sprintf("must not be longer than %d bytes", MaxSegmentBytes)
	case !utf8.ValidString(s):
		return "must be valid UTF-8"
	case strings.ContainsFunc(s, unicode.IsControl):
		return "must not contain control characters"
	case strings.ContainsAny(s, `/\`):
		return `must not contain / or \`
	case strings.ContainsAny(s, windowsForbidden):
		return "must not contain any of " + windowsForbidden
	case strings.TrimFunc(s, unicode.IsSpace) != s:
		return "must not start or end with spaces"
	case strings.HasSuffix(s, "."):
		// Windows drops a trailing dot, so "x." and "x" would be one file.
		return "must not end with a dot"
	case isWindowsReserved(s):
		return "is a reserved device name on Windows"
	case atomicfile.IsTemp(s):
		// Leftover temporary files are cleaned up by name; a downloaded
		// file must never look like one.
		return "must not start with " + atomicfile.TempPrefix
	}
	return ""
}

func isWindowsReserved(s string) bool {
	stem, _, _ := strings.Cut(s, ".")
	// Windows also ignores spaces between the device name and the extension.
	stem = strings.TrimRight(stem, " ")
	return windowsReserved[strings.ToUpper(stem)]
}

// Split checks a slash-separated name such as a nested GitLab owner
// ("group/subgroup") and returns its segments.
func Split(name string) ([]string, error) {
	segs := strings.Split(name, "/")
	for _, s := range segs {
		if err := CheckSegment(s); err != nil {
			return nil, fmt.Errorf("%q: %w", name, err)
		}
	}
	return segs, nil
}

// Join checks every segment and joins them below base. base is trusted (it
// is the data folder or a path built from it); segments are not. Because no
// checked segment can contain a separator or be "..", the result always lies
// inside base.
func Join(base string, segments ...string) (string, error) {
	if base == "" {
		return "", fmt.Errorf("join: %w: empty base", ErrInvalid)
	}
	for _, s := range segments {
		if err := CheckSegment(s); err != nil {
			return "", fmt.Errorf("join: %w", err)
		}
	}
	return filepath.Join(append([]string{base}, segments...)...), nil
}
