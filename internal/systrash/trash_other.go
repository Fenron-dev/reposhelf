// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !darwin && !windows

package systrash

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Linux desktops: gio (GLib) follows the freedesktop.org trash spec.
// Servers and Docker have no trash: moving is not offered there.
func available(string) bool {
	_, err := exec.LookPath("gio")
	return err == nil
}

func move(path string) error {
	p, err := exec.LookPath("gio")
	if err != nil {
		return ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, p, "trash", "--", path).CombinedOutput(); err != nil {
		return fmt.Errorf("papierkorb: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
