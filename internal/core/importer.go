// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/sources"
)

// ImportResult reports a mass import.
type ImportResult struct {
	Added, Skipped, Failed []string
}

// MassImport adds all repositories of the token owner (or the starred ones)
// from host (T-9.6). Syncs run in the background; dryRun only lists.
func (l *Library) MassImport(ctx context.Context, host string, starred bool, kind repometa.Kind, tags []string, dryRun bool, actor string) (ImportResult, error) {
	var res ImportResult
	api, err := l.Sources.API(host)
	if err != nil {
		return res, fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	if l.Sources.Tokens(host) == "" {
		return res, fmt.Errorf("%w: für den Massenimport wird ein Token für %s gebraucht", sources.ErrAuth, host)
	}
	urls, err := api.ListOwn(ctx, starred)
	if err != nil {
		return res, err
	}
	for _, u := range urls {
		if dryRun {
			res.Added = append(res.Added, u)
			continue
		}
		_, err := l.Add(ctx, AddRequest{URL: u, Kind: kind, Tags: tags, Actor: actor})
		switch {
		case err == nil:
			res.Added = append(res.Added, u)
		case errors.Is(err, ErrExists):
			res.Skipped = append(res.Skipped, u)
		default:
			res.Failed = append(res.Failed, u+": "+err.Error())
		}
	}
	return res, nil
}
