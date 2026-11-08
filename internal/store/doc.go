// SPDX-License-Identifier: GPL-3.0-or-later

// Package store persists metadata. YAML files under meta/ are the source of
// truth; the SQLite database is only a derived index that can always be
// rebuilt from those files (D-004). Unknown YAML fields must survive a
// read/write round trip.
package store
