// SPDX-License-Identifier: GPL-3.0-or-later

// Package config loads the instance configuration: defaults, then
// reposhelf.yaml in the data directory, then environment variables
// (KONZEPT 4.2: "Umgebungsvariablen überschreiben", Docker-friendly).
//
// The file holds no secrets. Tokens and keys come from the OS keychain or
// environment/secret files and are never read from or written to this file.
//
// Unknown keys are tolerated so a portable data folder written by a newer
// RepoShelf still opens with an older binary; they are reported as warnings
// so typos do not go unnoticed. A file with a newer schema number is rejected
// because its known keys may have changed meaning.
//
// Depends only on the standard library and go.yaml.in/yaml/v3.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

const (
	// FileName is the configuration file inside the data directory.
	FileName = "reposhelf.yaml"
	// CurrentSchema is the schema version this binary reads and writes.
	CurrentSchema = 1

	// DefaultPort is the web UI port (D-016; 7878 is taken by Radarr).
	DefaultPort = 7373
	// defaultHost keeps a desktop instance off the network unless asked.
	defaultHost = "127.0.0.1"

	// EnvListen overrides server.listen, e.g. ":7373" in Docker.
	EnvListen = "REPOSHELF_LISTEN"

	minPort = 1
	maxPort = 65535
)

// Errors returned by Load.
var (
	ErrNewerSchema = errors.New("config file was written by a newer RepoShelf")
	ErrInvalid     = errors.New("invalid configuration")
)

// Config is the instance configuration. New sections are additive: a file
// without them gets the defaults, so the schema number stays 1.
type Config struct {
	Schema    int       `yaml:"schema"`
	Instance  Instance  `yaml:"instance"`
	Server    Server    `yaml:"server"`
	Sync      Sync      `yaml:"sync"`
	Retention Retention `yaml:"retention"`
	Runner    Runner    `yaml:"runner"`
}

// Instance names this installation (shown in the UI and to paired peers).
type Instance struct {
	Name string `yaml:"name"`
}

// Server configures the HTTP listener (web UI, API, Git HTTP, MCP HTTP).
type Server struct {
	// Listen is host:port. An empty host means all interfaces.
	Listen string `yaml:"listen"`
	// PublicURL is how others reach this instance (clone URLs, MCP). Empty:
	// derived from each request.
	PublicURL string `yaml:"public_url"`
	// AnonymousRead allows reading (web, git clone) without a token when
	// the server is reachable from the network (KONZEPT 6.14).
	AnonymousRead bool `yaml:"anonymous_read"`
}

// Sync holds defaults of the mirror scheduler (KONZEPT 6.2).
type Sync struct {
	DefaultInterval  repometa.Duration `yaml:"default_interval"`
	ArchivedInterval repometa.Duration `yaml:"archived_interval"`
	Concurrency      int               `yaml:"concurrency"`
	// Paused stops automatic syncs (manual ones still work).
	Paused bool `yaml:"paused"`
}

// Retention holds the global retention rules (KONZEPT 6.10). Collections and
// repositories override them.
type Retention struct {
	ReleasesKeep int `yaml:"releases_keep"`
	// MaxAssetMB skips release files larger than this (0 = no limit), so a
	// project with many platform builds cannot fill a small disk (T-1.13).
	MaxAssetMB              int               `yaml:"max_asset_mb"`
	SnapshotsEvery          repometa.Duration `yaml:"snapshots_every"`
	SnapshotsKeepFor        repometa.Duration `yaml:"snapshots_keep_for"`
	ForcePushBackupsKeepFor repometa.Duration `yaml:"force_push_backups_keep_for"`
	CapsulesKeep            int               `yaml:"capsules_keep"`
	BuildsKeep              int               `yaml:"builds_keep"`
	TrashDays               int               `yaml:"trash_days"`
}

// Runner enables the build role of this instance (KONZEPT 6.12).
type Runner struct {
	Enabled      bool     `yaml:"enabled"`
	Capabilities []string `yaml:"capabilities"`
	MaxParallel  int      `yaml:"max_parallel"`
	// Confirm makes every build wait for a confirmation in the UI (weak
	// machines such as a laptop with little disk space).
	Confirm bool `yaml:"confirm"`
	// MinFreeGB refuses builds when less disk space is free.
	MinFreeGB int `yaml:"min_free_gb"`
	// MemoryLimit and CPULimit are passed to docker (e.g. "4g", "2").
	MemoryLimit string `yaml:"memory_limit"`
	CPULimit    string `yaml:"cpu_limit"`
}

const (
	day                     = 24 * time.Hour
	defaultSyncInterval     = 6 * time.Hour
	defaultArchivedInterval = 30 * day
	defaultConcurrency      = 2
	defaultReleasesKeep     = 5
	defaultSnapshotsEvery   = 30 * day
	defaultKeepFor          = 180 * day
	defaultCapsulesKeep     = 3
	defaultBuildsKeep       = 10
	defaultTrashDays        = 30
	defaultMinFreeGB        = 5
)

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Schema:   CurrentSchema,
		Instance: Instance{Name: "RepoShelf"},
		Server:   Server{Listen: net.JoinHostPort(defaultHost, strconv.Itoa(DefaultPort))},
		Sync: Sync{
			DefaultInterval:  repometa.Duration(defaultSyncInterval),
			ArchivedInterval: repometa.Duration(defaultArchivedInterval),
			Concurrency:      defaultConcurrency,
		},
		Retention: Retention{
			ReleasesKeep:            defaultReleasesKeep,
			SnapshotsEvery:          repometa.Duration(defaultSnapshotsEvery),
			SnapshotsKeepFor:        repometa.Duration(defaultKeepFor),
			ForcePushBackupsKeepFor: repometa.Duration(defaultKeepFor),
			CapsulesKeep:            defaultCapsulesKeep,
			BuildsKeep:              defaultBuildsKeep,
			TrashDays:               defaultTrashDays,
		},
		Runner: Runner{MaxParallel: 1, Confirm: true, MinFreeGB: defaultMinFreeGB, MemoryLimit: "4g", CPULimit: "2"},
	}
}

// Loaded is the result of Load.
type Loaded struct {
	Config Config
	// Path is the configuration file that was looked at.
	Path string
	// FileExists is false when only defaults and environment were used.
	FileExists bool
	// Warnings lists tolerated problems such as unknown keys.
	Warnings []string
}

// Load builds the configuration for the given data directory. lookupEnv is
// usually os.LookupEnv.
func Load(dataDir string, lookupEnv func(string) (string, bool)) (*Loaded, error) {
	res := &Loaded{Config: Default(), Path: filepath.Join(dataDir, FileName)}

	raw, err := os.ReadFile(res.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// No file is fine: defaults apply until the user saves settings.
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", res.Path, err)
	default:
		res.FileExists = true
		warnings, err := decode(raw, &res.Config)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", res.Path, err)
		}
		res.Warnings = warnings
	}

	applyEnv(&res.Config, lookupEnv)

	if err := validate(res.Config); err != nil {
		return nil, fmt.Errorf("%s: %w", res.Path, err)
	}
	return res, nil
}

// decode fills cfg from YAML on top of its current (default) values and
// returns unknown keys as warnings instead of failing.
func decode(raw []byte, cfg *Config) ([]string, error) {
	// A key without value ("schema:") decodes to the zero value, so remember
	// whether schema was given at all via the zero value of a fresh decode.
	parsed := *cfg
	parsed.Schema = 0
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	if parsed.Schema == 0 {
		// Hand-written minimal files may omit the schema; they are read as
		// the current version.
		parsed.Schema = CurrentSchema
	}

	// Second, strict pass only to collect unknown keys.
	var warnings []string
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var strict Config
	var typeErr *yaml.TypeError
	if err := dec.Decode(&strict); errors.As(err, &typeErr) {
		warnings = append(warnings, typeErr.Errors...)
	}

	*cfg = parsed
	return warnings, nil
}

// EnvPublicURL overrides server.public_url.
const EnvPublicURL = "REPOSHELF_PUBLIC_URL"

func applyEnv(cfg *Config, lookupEnv func(string) (string, bool)) {
	if v, ok := lookupEnv(EnvListen); ok && v != "" {
		cfg.Server.Listen = v
	}
	if v, ok := lookupEnv(EnvPublicURL); ok && v != "" {
		cfg.Server.PublicURL = v
	}
}

// Save writes the configuration file atomically, keeping unknown keys and
// comments of an existing file.
func Save(dataDir string, cfg Config) error {
	path := filepath.Join(dataDir, FileName)
	f := repometa.NewFile(cfg)
	if raw, err := os.ReadFile(path); err == nil {
		parsed, err := repometa.ParseFile(raw, Default())
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		parsed.Value = cfg
		f = parsed
	}
	if err := validate(cfg); err != nil {
		return err
	}
	out, err := f.Marshal()
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, out, filePerm)
}

const filePerm = 0o644

func validate(cfg Config) error {
	switch {
	case cfg.Schema > CurrentSchema:
		return fmt.Errorf("%w (schema %d, this version supports %d)", ErrNewerSchema, cfg.Schema, CurrentSchema)
	case cfg.Schema < 0:
		return fmt.Errorf("%w: schema %d", ErrInvalid, cfg.Schema)
	}
	if err := validateListen(cfg.Server.Listen); err != nil {
		return fmt.Errorf("%w: server.listen: %w", ErrInvalid, err)
	}
	if cfg.Sync.Concurrency < 1 {
		return fmt.Errorf("%w: sync.concurrency must be at least 1", ErrInvalid)
	}
	if cfg.Retention.ReleasesKeep < 0 || cfg.Retention.TrashDays < 1 || cfg.Retention.CapsulesKeep < 0 || cfg.Retention.MaxAssetMB < 0 {
		return fmt.Errorf("%w: retention values out of range", ErrInvalid)
	}
	if cfg.Sync.DefaultInterval < repometa.Duration(5*time.Minute) {
		return fmt.Errorf("%w: sync.default_interval must be at least 5m", ErrInvalid)
	}
	return nil
}

func validateListen(addr string) error {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("port %q is not a number", portStr)
	}
	if port < minPort || port > maxPort {
		return fmt.Errorf("port %d out of range %d-%d", port, minPort, maxPort)
	}
	return nil
}
