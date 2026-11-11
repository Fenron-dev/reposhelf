// SPDX-License-Identifier: GPL-3.0-or-later

package systrash

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// trashDir is ~/.Trash for the home volume, <volume>/.Trashes/<uid> for
// others – where Finder looks, so "Zurücklegen" works from there.
func trashDir(path string) (string, error) {
	dev := func(p string) (uint64, bool) {
		var st syscall.Stat_t
		if syscall.Lstat(p, &st) != nil {
			return 0, false
		}
		return uint64(st.Dev), true
	}
	d, ok := dev(path)
	if !ok {
		return "", fmt.Errorf("%s: nicht gefunden", path)
	}
	if home, err := os.UserHomeDir(); err == nil {
		t := filepath.Join(home, ".Trash")
		if td, ok := dev(t); ok && td == d {
			return t, nil
		}
	}
	// Walk up to the volume root.
	root := filepath.Clean(path)
	for {
		parent := filepath.Dir(root)
		if pd, ok := dev(parent); parent == root || !ok || pd != d {
			break
		}
		root = parent
	}
	t := filepath.Join(root, ".Trashes", fmt.Sprint(os.Getuid()))
	if info, err := os.Stat(t); err == nil && info.IsDir() {
		return t, nil
	}
	return "", ErrUnsupported
}

func available(path string) bool {
	_, err := trashDir(path)
	return err == nil
}

func move(path string) error {
	t, err := trashDir(path)
	if err != nil {
		return err
	}
	name := filepath.Base(path)
	dst := filepath.Join(t, name)
	if _, err := os.Lstat(dst); err == nil {
		dst = filepath.Join(t, fmt.Sprintf("%s %s", name, time.Now().Format("15.04.05")))
	}
	return os.Rename(path, dst)
}
