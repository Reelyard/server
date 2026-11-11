// Package app wires Harbor's parts together and runs them.
package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/inluxc/harbor/internal/api"
	"github.com/inluxc/harbor/internal/auth"
	"github.com/inluxc/harbor/internal/config"
	"github.com/inluxc/harbor/internal/dlna"
	"github.com/inluxc/harbor/internal/engine"
	"github.com/inluxc/harbor/internal/events"
	"github.com/inluxc/harbor/internal/importer"
	"github.com/inluxc/harbor/internal/mediaserver"
	"github.com/inluxc/harbor/internal/metadata"
	"github.com/inluxc/harbor/internal/profile"
	"github.com/inluxc/harbor/internal/search"
	"github.com/inluxc/harbor/internal/store"
	"github.com/inluxc/harbor/internal/stremio"
	"github.com/inluxc/harbor/internal/suppliers"
	"github.com/inluxc/harbor/internal/tracker"
	"github.com/inluxc/harbor/internal/version"
)

// App is a running Harbor instance.
type App struct {
	Config *config.Config
	Log    *slog.Logger
	UI     fs.FS
	// OnRebind runs when the bound network interface changes address
	// (set by the service runner to restart Harbor).
	OnRebind func()
	// Restart, when set, stops Harbor so the service manager starts it
	// again (used after the folders change).
	Restart func()

	store  *store.Store
	engine *engine.Engine
	dlna   *dlna.Server
	http   *http.Server
	cancel context.CancelFunc
	done   chan struct{}
	addr   net.Addr
}

// Start opens the database and starts serving. It returns once the
// listener is bound, so callers can report the address.
func (a *App) Start(ctx context.Context) error {
	if a.Log == nil {
		a.Log = slog.Default()
	}
	if err := a.Config.EnsureDirs(); err != nil {
		return err
	}
	st, err := store.Open(ctx, a.Config.DBPath())
	if err != nil {
		return err
	}
	a.store = st

	bus := events.New()
	eng, err := engine.New(ctx, engine.Options{
		Store: st, Bus: bus, Log: a.Log,
		DataDir: a.Config.DataDir, DownloadDir: a.Config.DownloadDir, ListenPort: a.Config.TorrentPort,
		BindInterface: a.Config.BindInterface, OnRebind: a.OnRebind,
	})
	if err != nil {
		st.Close()
		return err
	}
	a.engine = eng

	client := stremio.New("Harbor/" + version.Version)
	sup := &suppliers.Service{Store: st, Client: client}
	meta := metadata.New(client)
	srch := &search.Service{Suppliers: sup, Client: client}
	if err := profile.EnsureDefaults(ctx, st); err != nil {
		eng.Close()
		st.Close()
		return fmt.Errorf("create default profiles: %w", err)
	}
	trk := tracker.New(tracker.Options{Store: st, Meta: meta, Search: srch, Downloads: eng, Bus: bus, Log: a.Log})
	eng.SetLimitsFor(trk.Limits)
	media := mediaserver.New(ctx, mediaserver.Options{Store: st, Log: a.Log})
	imp := importer.New(importer.Options{
		Store: st, Downloads: eng, Meta: meta, Suppliers: sup, Client: client, Bus: bus, Log: a.Log,
		LibraryDir: a.Config.LibraryDir, DownloadDir: a.Config.DownloadDir,
		SubtitleLangsFor: trk.SubtitleLangs, CheckAudio: trk.CheckAudio, AfterImport: trk.Kick,
		OnImported: media.Kick,
	})
	a.dlna = dlna.New(ctx, dlna.Options{
		Store: st, LibraryDir: a.Config.LibraryDir, Log: a.Log, ExcludeInterface: a.Config.BindInterface,
		SubtitleLangs: func() []string { return importer.LoadSettings(context.Background(), st).SubtitleLangs },
	})
	eng.OnComplete(imp.Enqueue)
	srv := &api.Server{
		Store: st, Auth: auth.New(st), Config: a.Config, UI: a.UI, Log: a.Log,
		Suppliers:    sup,
		Meta:         meta,
		Importer:     imp,
		Tracker:      trk,
		Search:       srch,
		Engine:       eng,
		Bus:          bus,
		DLNA:         a.dlna,
		MediaServers: media,
		Restart:      a.Restart,
	}
	a.http = &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	a.http.RegisterOnShutdown(srv.Shutdown)
	ln, err := net.Listen("tcp", a.Config.Listen)
	if err != nil {
		eng.Close()
		st.Close()
		return fmt.Errorf("listen on %s: %w", a.Config.Listen, err)
	}
	a.addr = ln.Addr()

	runCtx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.done = make(chan struct{})
	go func() {
		defer close(a.done)
		if err := a.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.Log.Error("http server stopped", "err", err)
		}
	}()
	a.dlna.Start()
	go a.housekeeping(runCtx)
	go imp.Run(runCtx)
	go trk.Run(runCtx)

	a.Log.Info("harbor started", "version", version.Version, "listen", a.addr.String(),
		"data", a.Config.DataDir, "downloads", a.Config.DownloadDir, "library", a.Config.LibraryDir)
	return nil
}

// Addr is the bound listen address (valid after Start).
func (a *App) Addr() net.Addr { return a.addr }

// Stop shuts down gracefully, waiting up to timeout for requests to finish.
func (a *App) Stop(timeout time.Duration) error {
	if a.cancel != nil {
		a.cancel()
	}
	var err error
	if a.http != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		err = a.http.Shutdown(ctx)
		<-a.done
	}
	if a.dlna != nil {
		a.dlna.Close()
	}
	if a.engine != nil {
		if cerr := a.engine.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	if a.store != nil {
		if cerr := a.store.Close(); err == nil {
			err = cerr
		}
	}
	a.Log.Info("harbor stopped")
	return err
}

// housekeeping runs periodic cleanup until ctx ends.
func (a *App) housekeeping(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := a.store.PruneSessions(ctx); err != nil && ctx.Err() == nil {
			a.Log.Warn("prune sessions", "err", err)
		} else if n > 0 {
			a.Log.Debug("pruned expired sessions", "count", n)
		}
		if n, err := a.store.PruneDevices(ctx); err != nil && ctx.Err() == nil {
			a.Log.Warn("prune device pairings", "err", err)
		} else if n > 0 {
			a.Log.Debug("pruned expired device pairings", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
