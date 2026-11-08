// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !darwin && !linux && !windows

package fsinfo

func fsType(string) string { return "" }
