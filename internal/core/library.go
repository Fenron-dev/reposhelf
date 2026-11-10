// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/config"
	"github.com/Fenron-dev/reposhelf/internal/fsinfo"
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/logx"
	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// Errors the surfaces translate into messages.
var (
	ErrNotFound  = errors.New("repository not found")
	ErrExists    = errors.New("repository already exists")
	ErrInvalid   = errors.New("invalid input")
	ErrReadOnly  = errors.New("mirrors are read-only")
	ErrBusy      = errors.New("operation already running")
	ErrNoGit     = errors.New("git is not available")
	ErrForbidden = errors.New("not allowed")
)

// Options configure Open.
type Options struct {
	DataDir   string
	ExeDir    string // for a portable git next to the program (D-020)
	LookupEnv func(string) (string, bool)
	Logger    *slog.Logger
	Now       func() time.Time
	HTTP      *http.Client
	// SourceBaseURLs overrides API roots per host (tests).
	SourceBaseURLs map[string]string
	// SourceKinds sets the provider of self-hosted hosts.
	SourceKinds map[string]string
	// Git overrides the git executable (tests).
	Git *gitcli.Git
	// NoKeychain disables the OS keychain lookup for tokens (tests).
	NoKeychain bool
	// Keychain replaces the OS keychain (tests).
	Keychain sources.Keychain
}

// Library is an open data folder.
type Library struct {
	Layout  layout.Layout
	Git     *gitcli.Git
	Sources *sources.Registry
	Tokens  *sources.TokenStore
	Store   *store.Store
	Log     *slog.Logger

	now      func() time.Time
	cfgMu    sync.RWMutex
	cfg      config.Config
	warnings []string
	lfs      bool
	locks    keyedLocks
	activity *activityLog
	jobs     *jobs
	events   *events
}

// Open opens the data folder dir, creating missing subfolders, and loads the
// index (rebuilding it if needed).
func Open(ctx context.Context, opts Options) (*Library, error) {
	if opts.LookupEnv == nil {
		opts.LookupEnv = os.LookupEnv
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = logx.Discard()
	}
	lay, err := layout.Init(opts.DataDir)
	if err != nil {
		return nil, err
	}
	loaded, err := config.Load(opts.DataDir, opts.LookupEnv)
	if err != nil {
		return nil, err
	}
	g := opts.Git
	if g == nil {
		if g, err = gitcli.Find(opts.ExeDir); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNoGit, err)
		}
	}
	if _, err := g.Check(ctx); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoGit, err)
	}

	noWAL := !fsinfo.SupportsWAL(opts.DataDir)
	st, err := store.Open(lay.IndexPath(), store.Options{NoWAL: noWAL})
	if err != nil {
		return nil, err
	}
	kc := opts.Keychain
	if kc == nil && !opts.NoKeychain {
		kc = sources.SystemKeychain()
	}
	tokens := sources.NewTokenStore(opts.LookupEnv, kc)
	l := &Library{
		Layout: lay,
		Git:    g,
		Tokens: tokens,
		Sources: &sources.Registry{
			HTTP:     opts.HTTP,
			Tokens:   tokens.Token,
			BaseURLs: opts.SourceBaseURLs,
			Kinds:    opts.SourceKinds,
		},
		Store:    st,
		Log:      opts.Logger,
		now:      opts.Now,
		cfg:      loaded.Config,
		warnings: loaded.Warnings,
		lfs:      g.LFSAvailable(ctx),
		activity: &activityLog{path: lay.Path(layout.DirInternal, layout.DirLogs, activityFile)},
		events:   newEvents(),
	}
	if noWAL {
		l.Log.Info("index without WAL", "fs", fsinfo.Type(opts.DataDir))
	}
	l.jobs = newJobs(l)
	if st.NeedsRebuild() {
		if _, err := l.Reindex(ctx); err != nil {
			_ = st.Close()
			return nil, err
		}
	}
	return l, nil
}

// Close stops background work and closes the index.
func (l *Library) Close() error {
	l.jobs.stop()
	return l.Store.Close()
}

// Config returns the current configuration.
func (l *Library) Config() config.Config {
	l.cfgMu.RLock()
	defer l.cfgMu.RUnlock()
	return l.cfg
}

// ConfigWarnings returns warnings from loading reposhelf.yaml.
func (l *Library) ConfigWarnings() []string { return l.warnings }

// SaveConfig writes reposhelf.yaml and applies it.
func (l *Library) SaveConfig(cfg config.Config) error {
	if err := config.Save(l.Layout.Root, cfg); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	l.cfgMu.Lock()
	l.cfg = cfg
	l.cfgMu.Unlock()
	l.jobs.resize(cfg.Sync.Concurrency)
	return nil
}

// LFS reports whether git-lfs is installed.
func (l *Library) LFS() bool { return l.lfs }

// Now returns the library clock (overridable in tests).
func (l *Library) Now() time.Time { return l.now() }

// keyedLocks serialises work on one repository across goroutines.
type keyedLocks struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}

func (k *keyedLocks) lock(key string) func() {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]*sync.Mutex{}
	}
	m, ok := k.m[key]
	if !ok {
		m = &sync.Mutex{}
		k.m[key] = m
	}
	k.mu.Unlock()
	m.Lock()
	return m.Unlock
}
