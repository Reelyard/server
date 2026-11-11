// Command harbor tracks movies and series from Stremio addon suppliers,
// downloads them, and serves the library.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kardianos/service"
	"golang.org/x/term"

	"github.com/inluxc/harbor/internal/app"
	"github.com/inluxc/harbor/internal/auth"
	"github.com/inluxc/harbor/internal/config"
	"github.com/inluxc/harbor/internal/logging"
	"github.com/inluxc/harbor/internal/store"
	"github.com/inluxc/harbor/internal/version"
	"github.com/inluxc/harbor/web"
)

const usage = `Harbor — media tracker and downloader

Usage:
  harbor <command> [flags]

Commands:
  serve            Run the server in the foreground (or under a service manager)
  install          Install Harbor as a system service (needs admin/root; --user for a user service)
  uninstall        Remove the service
  start | stop | restart | status
                   Control the installed service
  reset-password   Set a new password for the admin account
  version          Print version information

Common flags:
  --config <file>  Config file (default: <data dir>/config.yaml)
  --system         Use system-wide default locations (implied by install without --user)

Environment:
  HARBOR_DATA_DIR, HARBOR_LISTEN, HARBOR_DOWNLOAD_DIR, HARBOR_LIBRARY_DIR,
  HARBOR_LOG_LEVEL, HARBOR_LOG_FORMAT, HARBOR_LOG_FILE
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "harbor:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "serve":
		return cmdServe(rest)
	case "install":
		return cmdInstall(rest)
	case "uninstall", "start", "stop", "restart":
		return cmdControl(cmd, rest)
	case "status":
		return cmdStatus(rest)
	case "reset-password":
		return cmdResetPassword(rest)
	case "version", "--version", "-v":
		fmt.Println(version.String())
		return nil
	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q (run 'harbor help')", cmd)
	}
}

// commonFlags registers --config and --system on fs.
type commonFlags struct {
	config string
	system bool
	user   bool
}

func newFlags(name string, withUser bool) (*flag.FlagSet, *commonFlags) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	cf := &commonFlags{}
	fs.StringVar(&cf.config, "config", "", "config file")
	fs.BoolVar(&cf.system, "system", false, "use system-wide default locations")
	if withUser {
		fs.BoolVar(&cf.user, "user", false, "install as a per-user service instead of system-wide")
	}
	return fs, cf
}

// ---- serve ----

type program struct {
	cfg *config.Config
	app *app.App
}

func (p *program) Start(s service.Service) error {
	return p.app.Start(context.Background())
}

func (p *program) Stop(s service.Service) error {
	return p.app.Stop(15 * time.Second)
}

func cmdServe(args []string) error {
	fs, cf := newFlags("serve", false)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(cf.config, cf.system)
	if err != nil {
		return err
	}
	log, closer, err := logging.Setup(cfg, nil)
	if err != nil {
		return err
	}
	defer closer.Close()

	a := &app.App{Config: cfg, Log: log, UI: web.FS()}

	if service.Interactive() {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := a.Start(ctx); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Harbor is running at %s — press Ctrl+C to stop\n", displayURL(a.Addr().String()))
		<-ctx.Done()
		return a.Stop(15 * time.Second)
	}

	// Under systemd / launchd / Windows Service Control Manager: when the
	// bound VPN interface gets a new address, stop cleanly and exit with an
	// error so the service manager starts Harbor again on the new address.
	restart := func(why string) func() {
		return func() {
			log.Warn(why + "; restarting Harbor")
			_ = a.Stop(15 * time.Second)
			os.Exit(3)
		}
	}
	a.OnRebind = restart("bound network interface changed address")
	a.Restart = restart("settings changed")
	s, err := service.New(&program{cfg: cfg, app: a}, serviceConfig(cfg.Path(), false))
	if err != nil {
		return err
	}
	return s.Run()
}

func displayURL(addr string) string {
	host, port, ok := strings.Cut(addr, "]:")
	if ok { // IPv6 [::]:8787
		host = strings.TrimPrefix(host, "[")
	} else {
		i := strings.LastIndex(addr, ":")
		host, port = addr[:i], addr[i+1:]
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "http://" + host + ":" + port
}

// ---- service management ----

func serviceConfig(configPath string, user bool) *service.Config {
	c := &service.Config{
		Name:        "harbor",
		DisplayName: "Harbor",
		Description: "Tracks movies and series from Stremio addons, downloads them and serves the media library.",
		Arguments:   []string{"serve"},
		Option:      service.KeyValue{},
	}
	if configPath != "" {
		c.Arguments = append(c.Arguments, "--config", configPath)
	}
	if user {
		c.Option["UserService"] = true
	}
	// Restart on failure (systemd / Windows).
	c.Option["Restart"] = "on-failure"
	c.Option["OnFailure"] = "restart"
	return c
}

func cmdInstall(args []string) error {
	fs, cf := newFlags("install", true)
	if err := fs.Parse(args); err != nil {
		return err
	}
	system := !cf.user || cf.system
	cfg, err := config.Load(cf.config, system)
	if err != nil {
		return err
	}
	path := cfg.Path()
	if path == "" {
		// Write a config file so the service and later CLI calls agree on locations.
		path = filepath.Join(cfg.DataDir, config.FileName)
		if err := cfg.Save(path); err != nil {
			return fmt.Errorf("write %s: %w (installing a system service needs admin/root; use --user otherwise)", path, err)
		}
		fmt.Println("Wrote config:", path)
	}
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}
	s, err := service.New(&program{}, serviceConfig(path, cf.user))
	if err != nil {
		return err
	}
	if err := s.Install(); err != nil {
		return fmt.Errorf("install service: %w", err)
	}
	fmt.Printf("Installed the Harbor service (platform: %s).\nStart it with: harbor start%s\nThen open %s\n",
		s.Platform(), userSuffix(cf.user), displayURL(cfg.Listen))
	return nil
}

func userSuffix(user bool) string {
	if user {
		return " --user"
	}
	return ""
}

func cmdControl(action string, args []string) error {
	fs, cf := newFlags(action, true)
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := service.New(&program{}, serviceConfig("", cf.user))
	if err != nil {
		return err
	}
	if err := service.Control(s, action); err != nil {
		return err
	}
	fmt.Printf("Harbor service: %s done\n", action)
	return nil
}

func cmdStatus(args []string) error {
	fs, cf := newFlags("status", true)
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := service.New(&program{}, serviceConfig("", cf.user))
	if err != nil {
		return err
	}
	st, err := s.Status()
	if errors.Is(err, service.ErrNotInstalled) {
		fmt.Println("Harbor service: not installed")
		return nil
	}
	if err != nil {
		return err
	}
	names := map[service.Status]string{service.StatusRunning: "running", service.StatusStopped: "stopped"}
	name, ok := names[st]
	if !ok {
		name = "unknown"
	}
	fmt.Println("Harbor service:", name)
	return nil
}

// ---- reset-password ----

func cmdResetPassword(args []string) error {
	fs, cf := newFlags("reset-password", false)
	username := fs.String("username", "", "account to reset (default: the admin account)")
	fromStdin := fs.Bool("password-stdin", false, "read the new password from standard input")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(cf.config, cf.system)
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()

	var u *store.User
	if *username != "" {
		u, err = st.UserByName(ctx, *username)
	} else {
		u, err = st.FirstUser(ctx)
	}
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("no such account; open the web UI to create the first one")
	}
	if err != nil {
		return err
	}

	pw, err := readPassword(*fromStdin, os.Stdin)
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := st.SetPassword(ctx, u.ID, hash); err != nil {
		return err
	}
	fmt.Printf("Password changed for %q. All existing sign-ins were ended.\n", u.Username)
	return nil
}

func readPassword(fromStdin bool, in io.Reader) (string, error) {
	if fromStdin || !term.IsTerminal(int(os.Stdin.Fd())) {
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, "New password: ")
	a, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "Repeat it: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(a) != string(b) {
		return "", errors.New("passwords do not match")
	}
	return string(a), nil
}
