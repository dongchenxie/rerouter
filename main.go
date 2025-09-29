package main

import (
    "log"
    "net/http"
    "os"
)
// Auto-load .env from project root if present (minimal, clean)
import _ "github.com/joho/godotenv/autoload"

// config, bot detection, and rewriting helpers are in separate files for clarity.

// buildHandler moved to handler.go

func main() {
    cfg, err := loadConfig()
    if err != nil {
        log.Fatalf("config error: %v", err)
    }
    if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
        log.Fatalf("failed to create cache dir: %v", err)
    }

    log.Printf("Starting A-site on %s, proxying bots from %s", cfg.ListenAddr, cfg.BBaseURL)

    handler := buildHandler(cfg)
    srv := &http.Server{Addr: cfg.ListenAddr, Handler: handler}
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        log.Fatalf("server error: %v", err)
    }
}
