// NEXPLAY Server — a personal media server (movies, series, music) for the NEXPLAY apps.
// Runs as a Windows service (or in a console), stores its data in SQLite and serves:
//   - /API_v2/*.php   the same API the NEXPLAY Android / Android TV apps already speak
//   - /web/           the admin panel (first-run setup, libraries, users, devices)
//   - /stream/, /img/ signed media URLs (players cannot send the Bearer header)
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kardianos/service"
)

const Version = "1.0.0"

type App struct {
	DataDir string
	Port    int
	DB      *Store
	Lib     *Library
	srv     *http.Server
}

func defaultDataDir() string {
	if runtime.GOOS == "windows" {
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "NEXPLAY Server")
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".nexplay-server")
	}
	return "nexplay-data"
}

// service glue (Windows service / systemd)
type program struct{ app *App }

func (p *program) Start(s service.Service) error { go p.app.Run(); return nil }
func (p *program) Stop(s service.Service) error {
	if p.app.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.app.srv.Shutdown(ctx)
	}
	return nil
}

func main() {
	dataDir := flag.String("data", defaultDataDir(), "data folder (database, images, logs)")
	port := flag.Int("port", 8096, "HTTP port")
	svcCmd := flag.String("service", "", "install | uninstall | start | stop | restart")
	flag.Parse()

	app := &App{DataDir: *dataDir, Port: *port}
	cfg := &service.Config{
		Name:        "NexplayServer",
		DisplayName: "NEXPLAY Server",
		Description: "Personal media server for the NEXPLAY apps (movies, series, music).",
		Arguments:   []string{"-data", *dataDir, "-port", fmt.Sprint(*port)},
	}
	s, err := service.New(&program{app: app}, cfg)
	if err != nil {
		log.Fatal(err)
	}
	if *svcCmd != "" {
		if err := service.Control(s, *svcCmd); err != nil {
			log.Fatalf("service %s: %v", *svcCmd, err)
		}
		fmt.Println("service", *svcCmd, "ok")
		return
	}
	if err := s.Run(); err != nil {
		log.Fatal(err)
	}
}

func (a *App) Run() {
	if err := os.MkdirAll(a.DataDir, 0o755); err != nil {
		log.Fatal(err)
	}
	lf, err := os.OpenFile(filepath.Join(a.DataDir, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		log.SetOutput(lf)
	}
	log.Printf("NEXPLAY Server %s starting, data=%s port=%d", Version, a.DataDir, a.Port)
	db, err := OpenStore(filepath.Join(a.DataDir, "nexplay.db"))
	if err != nil {
		log.Fatal(err)
	}
	a.DB = db
	a.Lib = NewLibrary(a)
	go a.Lib.Watch()
	go RunDiscovery(a)

	mux := http.NewServeMux()
	a.routes(mux)
	a.srv = &http.Server{Addr: fmt.Sprintf(":%d", a.Port), Handler: logRequests(mux), ReadHeaderTimeout: 15 * time.Second}
	log.Printf("listening on %s (%s)", a.srv.Addr, strings_join(LocalAddresses(), ", "))
	if err := a.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "NEXPLAY-Server/"+Version)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// LocalAddresses lists this computer's LAN IPv4 addresses (shown in the admin panel).
func LocalAddresses() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLinkLocalUnicast() {
				out = append(out, ipn.IP.String())
			}
		}
	}
	return out
}

func strings_join(a []string, sep string) string {
	s := ""
	for i, v := range a {
		if i > 0 {
			s += sep
		}
		s += v
	}
	return s
}
