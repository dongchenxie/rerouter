package main

import (
    "net/http"
    "os"
    "time"
    "rerouter/logger"
)
// Auto-load .env from project root if present (minimal, clean)
import _ "github.com/joho/godotenv/autoload"

// config, bot detection, and rewriting helpers are in separate files for clarity.

// buildHandler moved to handler.go

func main() {
    cfg, err := loadConfig()
    if err != nil {
        // Fallback simple stderr
        panic(err)
    }
    // Initialize structured logger
    _ = os.MkdirAll("./logs", 0o755)
    _ = logger.Init(logger.Config{
        Level:      logger.ParseLevel(cfg.LogLevel),
        File:       cfg.LogFile,
        MaxSizeMB:  cfg.LogMaxSizeMB,
        MaxBackups: cfg.LogMaxBackups,
        MaxAgeDays: cfg.LogMaxAgeDays,
    })
    defer logger.Close()
    if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
        logger.Errorw("failed_create_cache_dir", map[string]interface{}{"err": err.Error(), "dir": cfg.CacheDir})
        os.Exit(1)
    }
    logger.Infow("startup", map[string]interface{}{"listen": cfg.ListenAddr, "b_base_url": cfg.BBaseURL})
    if cfg.AdminToken != "" && cfg.AdminUIPath != "" {
        logger.Infow("admin_ui_enabled", map[string]interface{}{"path": cfg.AdminUIPath})
    }

    // Start periodic metrics logger
    if cfg.MetricsIntervalSeconds > 0 {
        logger.StartMetricsLogger(time.Duration(cfg.MetricsIntervalSeconds)*time.Second, cfg.CacheDir)
    }

    handler := loggingMiddleware(buildHandler(cfg))
    srv := &http.Server{Addr: cfg.ListenAddr, Handler: handler}
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        logger.Errorw("server_error", map[string]interface{}{"err": err.Error()})
        os.Exit(1)
    }
}
