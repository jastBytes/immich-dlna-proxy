package main

import (
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

	client := immich.New(cfg.ImmichURL, cfg.APIKey)

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

	server := dlna.NewServer(cfg, client, diskCache)

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
