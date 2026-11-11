package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/inluxc/harbor/internal/config"
	"github.com/inluxc/harbor/internal/diskfree"
)

// The first-run setup: after the account is created, the UI walks through
// folders and suppliers. It is "done" once finished or skipped, and also for
// installs that already have suppliers (set up before the wizard existed).

const onboardingKey = "onboarding"

func (s *Server) routesOnboarding(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/onboarding", s.authed(s.getOnboarding))
	mux.Handle("PUT /api/v1/onboarding/folders", s.authed(s.putFolders))
	mux.Handle("POST /api/v1/onboarding/done", s.authed(s.finishOnboarding))
}

type folderInfo struct {
	Path string `json:"path"`
	Free int64  `json:"free"` // bytes; -1 unknown
}

func folder(p string) folderInfo {
	f := folderInfo{Path: p, Free: -1}
	if n, err := diskfree.Free(existingParent(p)); err == nil {
		f.Free = int64(n)
	}
	return f
}

// existingParent is p or its nearest existing parent (for free space).
func existingParent(p string) string {
	for {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

func (s *Server) onboardingDone(ctx context.Context) bool {
	var v struct {
		Done bool `json:"done"`
	}
	if s.Store.GetSetting(ctx, onboardingKey, &v) == nil && v.Done {
		return true
	}
	sups, err := s.Store.ListSuppliers(ctx)
	return err == nil && len(sups) > 0
}

// foldersLocked says why the folders can't be changed here ("" = they can).
func (s *Server) foldersLocked(ctx context.Context) string {
	if env := config.EnvOverrides(); len(env) > 0 {
		return fmt.Sprintf("They are set by %s (for example in Docker), so change them there.", strings.Join(env, " and "))
	}
	if dls, err := s.Store.ListDownloads(ctx); err != nil || len(dls) > 0 {
		return "Harbor already has downloads in these folders. To move them, stop Harbor, move the files, and edit the config file."
	}
	return ""
}

func (s *Server) getOnboarding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sups, _ := s.Store.ListSuppliers(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"done":       s.onboardingDone(ctx),
		"download":   folder(s.Config.DownloadDir),
		"library":    folder(s.Config.LibraryDir),
		"hardlinks":  canHardlink(s.Config.DownloadDir, s.Config.LibraryDir),
		"configFile": s.Config.FilePath(),
		"locked":     s.foldersLocked(ctx),
		"canRestart": s.Restart != nil,
		"suppliers":  len(sups),
	})
}

type foldersBody struct {
	Download string `json:"download"`
	Library  string `json:"library"`
}

// putFolders saves new folders to the config file. They take effect after
// a restart, which Harbor does itself when it runs as a service.
func (s *Server) putFolders(w http.ResponseWriter, r *http.Request) {
	var b foldersBody
	if !decode(w, r, &b) {
		return
	}
	if why := s.foldersLocked(r.Context()); why != "" {
		writeError(w, http.StatusConflict, why)
		return
	}
	dl, err := checkFolder(b.Download, "download")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	lib, err := checkFolder(b.Library, "library")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if dl == lib || within(dl, lib) || within(lib, dl) {
		writeError(w, http.StatusBadRequest, "Use two separate folders: one can't be inside the other.")
		return
	}
	changed := dl != s.Config.DownloadDir || lib != s.Config.LibraryDir
	if changed {
		if err := config.UpdateFile(s.Config.FilePath(), map[string]string{"download_dir": dl, "library_dir": lib}); err != nil {
			s.internal(w, "save config", err)
			return
		}
		s.Log.Info("folders changed; restart needed", "downloads", dl, "library", lib, "config", s.Config.FilePath())
	}
	restarting := changed && s.Restart != nil
	writeJSON(w, http.StatusOK, map[string]any{
		"changed":    changed,
		"restarting": restarting,
		"hardlinks":  canHardlink(dl, lib),
		"download":   folder(dl),
		"library":    folder(lib),
	})
	if restarting {
		go func() {
			time.Sleep(500 * time.Millisecond) // let the response go out
			s.Restart()
		}()
	}
}

func (s *Server) finishOnboarding(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.SetSetting(r.Context(), onboardingKey, map[string]bool{"done": true}); err != nil {
		s.internal(w, "save onboarding", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// checkFolder cleans a path, creates it, and checks Harbor can write there.
func checkFolder(p, what string) (string, error) {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), string(filepath.Separator)))
		}
	}
	if p == "" || !filepath.IsAbs(p) {
		return "", fmt.Errorf("Enter the full path of the %s folder, like %s.", what, example())
	}
	p = filepath.Clean(p)
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", fmt.Errorf("Couldn't create the %s folder: %v", what, errors.Unwrap(err))
	}
	f, err := os.CreateTemp(p, ".harbor-write-test-*")
	if err != nil {
		return "", fmt.Errorf("Harbor can't write to the %s folder %s. Check its permissions.", what, p)
	}
	f.Close()
	os.Remove(f.Name())
	return p, nil
}

func example() string {
	if filepath.Separator == '\\' {
		return `D:\Media`
	}
	return "/srv/media"
}

// within reports whether child is inside parent.
func within(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

// canHardlink reports whether files can be hardlinked from dl into lib
// (same disk). Otherwise imports copy, using twice the space while sharing.
func canHardlink(dl, lib string) bool {
	if os.MkdirAll(dl, 0o755) != nil || os.MkdirAll(lib, 0o755) != nil {
		return false
	}
	f, err := os.CreateTemp(dl, ".harbor-link-test-*")
	if err != nil {
		return false
	}
	f.Close()
	defer os.Remove(f.Name())
	dst := filepath.Join(lib, filepath.Base(f.Name()))
	if err := os.Link(f.Name(), dst); err != nil {
		return false
	}
	os.Remove(dst)
	return true
}
