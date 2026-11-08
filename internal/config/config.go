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

	"go.yaml.in/yaml/v3"
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

// Config is the instance configuration.
type Config struct {
	Schema int    `yaml:"schema"`
	Server Server `yaml:"server"`
}

// Server configures the HTTP listener (web UI, API, Git HTTP, MCP HTTP).
type Server struct {
	// Listen is host:port. An empty host means all interfaces.
	Listen string `yaml:"listen"`
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Schema: CurrentSchema,
		Server: Server{Listen: net.JoinHostPort(defaultHost, strconv.Itoa(DefaultPort))},
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

func applyEnv(cfg *Config, lookupEnv func(string) (string, bool)) {
	if v, ok := lookupEnv(EnvListen); ok && v != "" {
		cfg.Server.Listen = v
	}
}

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
