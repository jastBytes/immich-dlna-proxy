package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/jastBytes/immich-dlna-proxy/cache"
	"github.com/jastBytes/immich-dlna-proxy/config"
	"github.com/jastBytes/immich-dlna-proxy/dlna"
	"github.com/jastBytes/immich-dlna-proxy/immich"
)

// version is set at build time via -ldflags "-X main.version=v1.2.3" (see
// release.yml and the Dockerfile's VERSION build arg). Empty means it
// wasn't, and buildVersion falls back to what the Go toolchain recorded.
var version string

// buildVersion returns the version to report: the ldflags-injected one if
// set, else the module version the Go toolchain stamped from VCS (e.g. for
// `go install ...@v0.2.0`), else "dev".
func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	dlna.Version = buildVersion()
	log.Printf("immich-dlna-proxy %s", dlna.Version)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	log.Printf("Immich server: %s", cfg.ImmichURL)
	if cfg.MaxWidth > 0 {
		log.Printf("Downscaling photos to max %dx%d", cfg.MaxWidth, cfg.MaxHeight)
	}

	users, err := buildUsers(cfg)
	if err != nil {
		log.Fatalf("%v", err)
	}
	if len(users) > 1 {
		log.Printf("%d Immich accounts configured - each gets its own top-level DLNA folder", len(users))
	}

	var diskCache *cache.Cache
	if cfg.CacheDir != "" {
		diskCache, err = cache.New(cfg.CacheDir, cfg.CacheMaxBytes)
		if err != nil {
			log.Fatalf("cache init failed: %v", err)
		}
		log.Printf("Disk cache enabled at %s (max %d MB)", cfg.CacheDir, cfg.CacheMaxBytes/1024/1024)
	} else {
		log.Printf("Disk cache disabled (DISABLE_CACHE=true) - streaming directly from Immich every time")
	}

	if cfg.Debug {
		log.Printf("Debug logging enabled")
	}

	server := dlna.NewServer(cfg, users, diskCache)
	httpServer := server.NewHTTPServer()

	// SIGINT/SIGTERM (e.g. `docker stop`) trigger a clean shutdown: SSDP
	// announces ssdp:byebye so TVs drop the server right away, and
	// in-flight HTTP requests get a few seconds to finish.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 2)
	go func() {
		log.Printf("HTTP (description/SOAP/media) listening on %s", cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- fmt.Errorf("HTTP server failed: %w", err)
		}
	}()

	ssdpDone := make(chan struct{})
	go func() {
		defer close(ssdpDone)
		log.Printf("Starting SSDP responder (friendly name: %s, uuid: %s, interface: %s)", cfg.FriendlyName, cfg.UUID, ifaceOrAll(cfg.Interface))
		if err := dlna.RunSSDP(ctx, cfg); err != nil {
			errc <- fmt.Errorf("SSDP responder failed: %w", err)
		}
	}()

	select {
	case err := <-errc:
		log.Fatalf("%v", err)
	case <-ctx.Done():
	}

	log.Printf("Shutting down")
	<-ssdpDone // byebye sent
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP shutdown: %v", err)
	}
}

func ifaceOrAll(iface string) string {
	if iface == "" {
		return "all"
	}
	return iface
}

// buildUsers creates one immich.Client per configured API key. With a
// single key (the common case), the account's display name is never
// shown to DLNA clients (see browseUserScope in dlna/contentdirectory.go),
// so it isn't worth an extra Immich call to fetch it. With more than one
// key (IMMICH_API_KEYS), each account gets its own top-level DLNA folder
// named after it, so its name is fetched from Immich up front - failing
// startup with a clear error if that fails, same as any other
// misconfiguration this proxy can detect early.
func buildUsers(cfg *config.Config) ([]dlna.UserClient, error) {
	users := make([]dlna.UserClient, len(cfg.APIKeys))
	for i, key := range cfg.APIKeys {
		client := immich.New(cfg.ImmichURL, key)
		users[i] = dlna.UserClient{Client: client}

		if len(cfg.APIKeys) == 1 {
			continue
		}
		me, err := client.GetMyUser()
		if err != nil {
			return nil, fmt.Errorf("IMMICH_API_KEYS[%d]: fetching account name failed: %w", i, err)
		}
		name := me.Name
		if name == "" {
			name = me.Email
		}
		users[i].Name = name
		log.Printf("Immich account %d: %s", i, name)
	}
	return users, nil
}
