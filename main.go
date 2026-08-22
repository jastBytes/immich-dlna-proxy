package main

import (
	"fmt"
	"log"

	"github.com/jastBytes/immich-dlna-proxy/cache"
	"github.com/jastBytes/immich-dlna-proxy/config"
	"github.com/jastBytes/immich-dlna-proxy/dlna"
	"github.com/jastBytes/immich-dlna-proxy/immich"
)

func main() {
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

	server := dlna.NewServer(cfg, users, diskCache)

	go func() {
		if err := server.ListenAndServe(); err != nil {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	log.Printf("Starting SSDP responder (friendly name: %s, uuid: %s, interface: %s)", cfg.FriendlyName, cfg.UUID, ifaceOrAll(cfg.Interface))
	if err := dlna.RunSSDP(cfg); err != nil {
		log.Fatalf("SSDP responder failed: %v", err)
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
