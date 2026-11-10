// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !darwin && !linux && !windows

package sources

// SystemKeychain returns nil: no supported secret store.
func SystemKeychain() Keychain { return nil }
