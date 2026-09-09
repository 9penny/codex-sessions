package cmd

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/config"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/spf13/cobra"
)

//go:embed webassets/*
var webAssets embed.FS

type webApp struct {
	store   *sessionindex.Store
	library *webLibrary
	scanner *webScanner
	home    string
	token   string
	handler http.Handler
	refresh chan bool
}

func newWebApp(store *sessionindex.Store, library *webLibrary, home string) (*webApp, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	a := &webApp{store: store, library: library, home: home, token: hex.EncodeToString(secret[:]), scanner: newWebScanner(store), refresh: make(chan bool, 1)}
	assets, err := fs.Sub(webAssets, "webassets")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", a.state)
	mux.HandleFunc("GET /api/sessions", a.sessions)
	mux.HandleFunc("GET /api/session", a.detail)
	mux.HandleFunc("PATCH /api/annotation", a.annotate)
	mux.HandleFunc("POST /api/roots", a.addRoot)
	mux.HandleFunc("POST /api/scan", func(w http.ResponseWriter, r *http.Request) {
		select {
		case a.refresh <- true:
		default:
		}
		webJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
	})
	mux.Handle("GET /", http.FileServer(http.FS(assets)))
	a.handler = mux
	return a, nil
}
func (a *webApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'none'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		webError(w, 403, "请通过 localhost 或 SSH 本地转发访问")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
		webError(w, 403, "不允许跨站访问")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(a.token)) != 1 {
			webError(w, 401, "请使用终端中显示的完整链接打开页面")
			return
		}
	}
	a.handler.ServeHTTP(w, r)
}
func webJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func webError(w http.ResponseWriter, status int, message string) {
	webJSON(w, status, map[string]string{"error": message})
}
func webDecode(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		webError(w, 400, "请求格式不正确")
		return false
	}
	return true
}
func (a *webApp) background(ctx context.Context) {
	_ = a.scanner.run(ctx, a.library.snapshot().Roots, true)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case full := <-a.refresh:
			_ = a.scanner.run(ctx, a.library.snapshot().Roots, full)
		case <-ticker.C:
			_ = a.scanner.run(ctx, a.library.snapshot().Roots, false)
		}
	}
}

// CreateWebCommand serves each machine's independent library. Loopback plus a per-process
// bearer token works through SSH forwarding without exposing a shell or native files remotely.
func CreateWebCommand() *cobra.Command {
	var port int
	command := &cobra.Command{Use: "web", Short: "Open the project-first local session browser", Args: cobra.NoArgs,
		Long: "Browse all local Codex sessions in a web UI. First launch scans your home directory. Continue conversations in your existing terminal. For SSH, forward the selected port to localhost.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if port < 0 || port > 65535 {
				return fmt.Errorf("port must be between 0 and 65535")
			}
			paths, err := config.ResolveCodexSessionsPaths()
			if err != nil {
				return err
			}
			store, err := sessionindex.Open(paths.DatabaseFile)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			// Prevent two services using different ports from racing on personal library.json.
			lock, err := os.OpenFile(filepath.Join(paths.DataDir, "web.lock"), os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				return err
			}
			defer func() { _ = lock.Close() }()
			if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				return fmt.Errorf("a web browser is already running for this library")
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			lib, err := openWebLibrary(filepath.Join(paths.DataDir, "library.json"), home)
			if err != nil {
				return err
			}
			app, err := newWebApp(store, lib, home)
			if err != nil {
				return err
			}
			listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				return fmt.Errorf("opening browser service (choose another --port if in use): %w", err)
			}
			defer func() { _ = listener.Close() }()
			actualPort := listener.Addr().(*net.TCPAddr).Port
			fprintf(cmd.OutOrStdout(), "Codex Sessions\nOpen: http://localhost:%d/#token=%s\nScanning ~/ for native sessions. Ctrl+C stops the service.\nSSH: ssh -N -L %d:127.0.0.1:%d <your-tailscale-host>\n", actualPort, app.token, actualPort, actualPort)
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			done := make(chan struct{})
			go func() { defer close(done); app.background(ctx) }()
			server := &http.Server{Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second}
			stop := make(chan struct{})
			go func() {
				select {
				case <-ctx.Done():
					shutdownCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer closeCancel()
					_ = server.Shutdown(shutdownCtx)
				case <-stop:
				}
			}()
			err = server.Serve(listener)
			close(stop)
			cancel()
			<-done
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}}
	command.Flags().IntVar(&port, "port", 5431, "localhost port (0 chooses an available port)")
	return command
}
