// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Fenron-dev/reposhelf/internal/auth"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/datadir"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/logx"
	"github.com/Fenron-dev/reposhelf/internal/mcp"
	"github.com/Fenron-dev/reposhelf/internal/peer"
	"github.com/Fenron-dev/reposhelf/internal/runner"
	"github.com/Fenron-dev/reposhelf/internal/server"
)

func init() {
	extraCommands["serve"] = runServe
	extraCommands["mcp"] = runMCP
	extraCommands["token"] = runToken
}

// Hooks lets other packages (desktop, runner, peer) add to the server.
var Hooks struct {
	// Server is called before the server starts; it may set extensions.
	Server func(lib *core.Library, o *server.Options)
}

func runServe(args []string, sys system) int {
	fset, data := dataFlagSet("serve", sys)
	listen := fset.String("listen", "", "")
	if _, code, ok := parseArgs(fset, args, 0, 0, sys); !ok {
		return code
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	loc, code, ok := resolveDataDir(*data, sys)
	if !ok {
		return code
	}
	if _, err := layout.Init(loc.Path); err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	log, closer, err := logx.Open(filepath.Join(loc.Path, layout.DirInternal, layout.DirLogs), slog.LevelInfo, sys.stderr)
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	defer closer.Close()
	exeDir := ""
	if exe, err := sys.executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	lib, err := core.Open(ctx, core.Options{DataDir: loc.Path, ExeDir: exeDir, LookupEnv: sys.lookupEnv, Logger: log, Git: sys.git})
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	defer lib.Close()
	if removed, err := lib.Layout.CleanTemp(lib.Now()); err == nil && len(removed) > 0 {
		log.Info("removed stale temporary files", "count", len(removed))
	}
	opts := server.Options{Log: log, LookupEnv: sys.lookupEnv}
	builds := runner.New(lib, log)
	builds.LookupEnv = sys.lookupEnv
	builds.Start(ctx)
	go everyHour(ctx, func() { builds.AutoCapsules(ctx) })
	opts.Ext.Builds, opts.MCPBuilds = builds, runner.MCP{M: builds}
	peers, err := peer.New(lib, log)
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	peers.PublicURL = func() string { return publicURL(lib) }
	peers.SetBuilds(builds)
	peers.RegisterRemotes(builds)
	go peers.Schedule(ctx)
	opts.Ext.Peers = peers
	opts.ExtraAuth = peers.CheckTemp
	opts.Extra = map[string]http.Handler{"/peer/": peers.Handler()}
	if Hooks.Server != nil {
		Hooks.Server(lib, &opts)
	}
	srv, err := server.New(lib, opts)
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	addr := *listen
	if addr == "" {
		addr = lib.Config().Server.Listen
	}
	err = srv.Run(ctx, addr, func(a string) {
		host := a
		if strings.HasPrefix(host, "[::]") || strings.HasPrefix(host, "0.0.0.0") {
			host = "localhost" + host[strings.LastIndex(host, ":"):]
		}
		fmt.Fprintf(sys.stdout, msgServing, host, loc.Path)
		log.Info("serving", "addr", a, "data", loc.Path, "source", loc.Source.String())
	})
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	return ExitOK
}

// runMCP serves MCP over stdio for locally started agents (KONZEPT 4.3).
// The local user started the process, so read, write and build are
// allowed; delete tools do not exist (D-011). --read-only limits it.
func runMCP(args []string, sys system) int {
	fset, data := dataFlagSet("mcp", sys)
	readOnly := fset.Bool("read-only", false, "")
	client := fset.String("client", "stdio", "")
	if _, code, ok := parseArgs(fset, args, 0, 0, sys); !ok {
		return code
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	loc, code, ok := resolveDataDir(*data, sys)
	if !ok {
		return code
	}
	// stdout belongs to the protocol: logs go to the log file only.
	log, closer, err := logx.Open(filepath.Join(loc.Path, layout.DirInternal, layout.DirLogs), slog.LevelInfo, nil)
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	defer closer.Close()
	lib, err := core.Open(ctx, core.Options{DataDir: loc.Path, LookupEnv: sys.lookupEnv, Logger: log, Git: sys.git})
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	defer lib.Close()
	rights := []string{auth.Read, auth.Write, auth.Build}
	if *readOnly {
		rights = []string{auth.Read}
	}
	cfg := lib.Config()
	base := cfg.Server.PublicURL
	if base == "" {
		base = "http://" + strings.Replace(cfg.Server.Listen, "0.0.0.0", "127.0.0.1", 1)
	}
	// Builds started here are executed by the running server (it picks up
	// queued builds from the data folder).
	builds := runner.New(lib, log)
	srv := mcp.New(lib, mcp.Options{Rights: rights, Client: *client, CloneBase: func() string { return base }, Builds: runner.MCP{M: builds}})
	if err := srv.Run(ctx, &sdk.StdioTransport{}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	return ExitOK
}

func runToken(args []string, sys system) int {
	fset, data := dataFlagSet("token", sys)
	name := fset.String("name", "", "")
	rights := fset.String("rights", "read", "")
	pos, code, ok := parseArgs(fset, args, 1, 2, sys)
	if !ok {
		return code
	}
	loc, code, ok := resolveDataDir(*data, sys)
	if !ok {
		return code
	}
	store, err := auth.Open(filepath.Join(loc.Path, layout.DirInternal, server.TokenFile), "", time.Now)
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	switch pos[0] {
	case "create":
		tok, secret, err := store.Create(*name, splitList(*rights))
		if err != nil {
			fmt.Fprintln(sys.stderr, errText(err))
			return ExitUsage
		}
		fmt.Fprintf(sys.stdout, msgTokenCreated, tok.Name, strings.Join(tok.Rights, ", "), secret)
	case "list":
		tw := tabwriter.NewWriter(sys.stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tRECHTE\tANGELEGT\tZULETZT BENUTZT")
		for _, t := range store.List() {
			last := "–"
			if t.LastUsed != nil {
				last = t.LastUsed.Local().Format("02.01.2006 15:04")
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Name, strings.Join(t.Rights, ","), t.Created.Local().Format("02.01.2006"), last)
		}
		tw.Flush()
	case "revoke":
		if len(pos) < 2 {
			fmt.Fprint(sys.stderr, msgUsage)
			return ExitUsage
		}
		if err := store.Revoke(pos[1]); err != nil {
			fmt.Fprintln(sys.stderr, errText(err))
			return ExitError
		}
		fmt.Fprintln(sys.stdout, "Widerrufen.")
	default:
		fmt.Fprint(sys.stderr, msgUsage)
		return ExitUsage
	}
	return ExitOK
}

var _ = datadir.EnvVar

func everyHour(ctx context.Context, f func()) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f()
		}
	}
}

// publicURL is how other instances reach this one.
func publicURL(lib *core.Library) string {
	cfg := lib.Config().Server
	if cfg.PublicURL != "" {
		return strings.TrimSuffix(cfg.PublicURL, "/")
	}
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return "http://" + cfg.Listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		if h, err := os.Hostname(); err == nil {
			host = h
		}
	}
	return "http://" + net.JoinHostPort(host, port)
}
