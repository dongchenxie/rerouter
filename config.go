package main

import (
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "net/url"
    "os"
    "strings"
)

type Config struct {
    // Base URL for B site, e.g. https://b.example.com
    BBaseURL string `json:"b_base_url"`
    // Base URL for A site (used for rewriting links in bot-served pages). If empty, derived from request host.
    ABaseURL string `json:"a_base_url"`
    // Address to listen on, e.g. :8080
    ListenAddr string `json:"listen_addr"`
    // Cache directory to store files
    CacheDir string `json:"cache_dir"`
    // Cache TTL in seconds
    CacheTTLSeconds int `json:"cache_ttl_seconds"`
    // Cache all URLs for bots when response is 200
    CacheAll bool `json:"cache_all"`
    // Path patterns to cache for bots if CacheAll=false (comma-separated via env). Supports * wildcard.
    CachePatterns []string `json:"cache_patterns"`
    // HTTP status code used to redirect humans (302 or 307 recommended)
    RedirectStatus int `json:"redirect_status"`
    // Admin token required to call admin endpoints like purge
    AdminToken string `json:"admin_token"`
    // Admin purge UI path (long hashed). If empty, derived from AdminToken.
    AdminUIPath string `json:"admin_ui_path"`
    // Log level: debug, info, warn, error
    LogLevel string `json:"log_level"`
    // Log file path. If empty, file logging disabled.
    LogFile string `json:"log_file"`
    // Log rotation settings
    LogMaxSizeMB int `json:"log_max_size_mb"`
    LogMaxBackups int `json:"log_max_backups"`
    LogMaxAgeDays int `json:"log_max_age_days"`
    // Interval to log system metrics (seconds). 0 disables.
    MetricsIntervalSeconds int `json:"metrics_interval_seconds"`
}

func getenv(key, def string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return def
}

func loadConfig() (*Config, error) {
    cfg := &Config{
        BBaseURL:        getenv("B_BASE_URL", ""),
        ABaseURL:        getenv("A_BASE_URL", ""),
        ListenAddr:      getenv("LISTEN_ADDR", ":8080"),
        CacheDir:        getenv("CACHE_DIR", "./cache"),
        CacheTTLSeconds: 3600,
        CacheAll:        true,
        CachePatterns:   []string{"/sitemap.xml", "/blog/*", "/products/*"},
        RedirectStatus:  302,
        LogLevel:        getenv("LOG_LEVEL", "info"),
        LogFile:         getenv("LOG_FILE", "./logs/a-site.log"),
        LogMaxSizeMB:    10,
        LogMaxBackups:   5,
        LogMaxAgeDays:   7,
        MetricsIntervalSeconds: 60,
    }

    if v := os.Getenv("CACHE_TTL_SECONDS"); v != "" {
        var n int
        fmt.Sscanf(v, "%d", &n)
        if n > 0 {
            cfg.CacheTTLSeconds = n
        }
    }
    if v := strings.ToLower(os.Getenv("CACHE_ALL")); v != "" {
        if v == "1" || v == "true" || v == "yes" || v == "on" {
            cfg.CacheAll = true
        } else if v == "0" || v == "false" || v == "no" || v == "off" {
            cfg.CacheAll = false
        }
    }
    if v := os.Getenv("CACHE_PATTERNS"); v != "" {
        parts := strings.Split(v, ",")
        out := make([]string, 0, len(parts))
        for _, p := range parts {
            p = strings.TrimSpace(p)
            if p != "" {
                if !strings.HasPrefix(p, "/") {
                    p = "/" + p
                }
                out = append(out, p)
            }
        }
        if len(out) > 0 {
            cfg.CachePatterns = out
        }
    }
    if v := os.Getenv("REDIRECT_STATUS"); v != "" {
        var n int
        fmt.Sscanf(v, "%d", &n)
        if n >= 300 && n < 400 {
            cfg.RedirectStatus = n
        }
    }
    if v := os.Getenv("METRICS_INTERVAL_SECONDS"); v != "" {
        var n int
        fmt.Sscanf(v, "%d", &n)
        if n >= 0 { cfg.MetricsIntervalSeconds = n }
    }
    if v := os.Getenv("LOG_MAX_SIZE_MB"); v != "" {
        var n int
        fmt.Sscanf(v, "%d", &n)
        if n > 0 { cfg.LogMaxSizeMB = n }
    }
    if v := os.Getenv("LOG_MAX_BACKUPS"); v != "" {
        var n int
        fmt.Sscanf(v, "%d", &n)
        if n >= 0 { cfg.LogMaxBackups = n }
    }
    if v := os.Getenv("LOG_MAX_AGE_DAYS"); v != "" {
        var n int
        fmt.Sscanf(v, "%d", &n)
        if n >= 0 { cfg.LogMaxAgeDays = n }
    }
    if v := os.Getenv("ADMIN_TOKEN"); v != "" {
        cfg.AdminToken = v
    }

    // Optional JSON config file path
    configPath := getenv("CONFIG_PATH", "./config.json")
    if b, err := os.ReadFile(configPath); err == nil {
        // overlay values from file
        type confAlias Config
        fileCfg := new(confAlias)
        if err := json.Unmarshal(b, fileCfg); err != nil {
            return nil, fmt.Errorf("parse config.json: %w", err)
        }
        mergeConfig(cfg, (*Config)(fileCfg))
    }

    // Admin UI path: env overrides file; if still empty, derive from token
    if v := getenv("ADMIN_UI_PATH", ""); v != "" {
        if strings.HasPrefix(v, "/") { cfg.AdminUIPath = v } else { cfg.AdminUIPath = "/" + v }
    }
    if cfg.AdminUIPath == "" && cfg.AdminToken != "" {
        sum := sha256.Sum256([]byte(cfg.AdminToken + "::rerouter-admin-ui"))
        cfg.AdminUIPath = "/admin/" + hex.EncodeToString(sum[:])[:48]
    }

    if cfg.BBaseURL == "" {
        return nil, errors.New("B_BASE_URL is required (env or config.json)")
    }
    if _, err := url.Parse(cfg.BBaseURL); err != nil {
        return nil, fmt.Errorf("invalid B_BASE_URL: %w", err)
    }
    if cfg.ABaseURL != "" {
        if _, err := url.Parse(cfg.ABaseURL); err != nil {
            return nil, fmt.Errorf("invalid A_BASE_URL: %w", err)
        }
    }
    return cfg, nil
}

func mergeConfig(dst, src *Config) {
    if src.BBaseURL != "" {
        dst.BBaseURL = src.BBaseURL
    }
    if src.ListenAddr != "" {
        dst.ListenAddr = src.ListenAddr
    }
    if src.ABaseURL != "" {
        dst.ABaseURL = src.ABaseURL
    }
    if src.CacheDir != "" {
        dst.CacheDir = src.CacheDir
    }
    if src.CacheTTLSeconds != 0 {
        dst.CacheTTLSeconds = src.CacheTTLSeconds
    }
    // If provided in file, allow overriding CacheAll
    if src.CacheAll {
        dst.CacheAll = true
    } else {
        dst.CacheAll = src.CacheAll
    }
    if len(src.CachePatterns) != 0 {
        dst.CachePatterns = src.CachePatterns
    }
    if src.RedirectStatus != 0 {
        dst.RedirectStatus = src.RedirectStatus
    }
    if src.LogLevel != "" { dst.LogLevel = src.LogLevel }
    if src.LogFile != "" { dst.LogFile = src.LogFile }
    if src.LogMaxSizeMB != 0 { dst.LogMaxSizeMB = src.LogMaxSizeMB }
    if src.LogMaxBackups != 0 { dst.LogMaxBackups = src.LogMaxBackups }
    if src.LogMaxAgeDays != 0 { dst.LogMaxAgeDays = src.LogMaxAgeDays }
    if src.MetricsIntervalSeconds != 0 { dst.MetricsIntervalSeconds = src.MetricsIntervalSeconds }
    if src.AdminUIPath != "" { dst.AdminUIPath = src.AdminUIPath }
}
