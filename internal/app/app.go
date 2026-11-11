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
	"github.com/inluxc/harbor/internal/store"
	"github.com/inluxc/harbor/internal/version"
)

// App is a running Harbor instance.
type App struct {
	Config *config.Config
	Log    *slog.Logger
	UI     fs.FS

	store  *store.Store
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

	srv := &api.Server{Store: st, Auth: auth.New(st), Config: a.Config, UI: a.UI, Log: a.Log}
	a.http = &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	ln, err := net.Listen("tcp", a.Config.Listen)
	if err != nil {
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
	go a.housekeeping(runCtx)

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
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
