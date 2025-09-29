package main

import (
    "crypto/sha1"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "log"
    "net/http"
    "net/url"
    "os"
    "path"
    "path/filepath"
    "strings"
    "time"
)

type Config struct {
    // Base URL for B site, e.g. https://b.example.com
    BBaseURL string `json:"b_base_url"`
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
}

type cacheEntry struct {
    URL       string            `json:"url"`
    CreatedAt int64             `json:"created_at"`
    ExpiresAt int64             `json:"expires_at"`
    Status    int               `json:"status"`
    Header    map[string]string `json:"header"`
    Body      []byte            `json:"body"`
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
        ListenAddr:      getenv("LISTEN_ADDR", ":8080"),
        CacheDir:        getenv("CACHE_DIR", "./cache"),
        CacheTTLSeconds: 3600,
        CacheAll:        true,
        CachePatterns:   []string{"/sitemap.xml", "/blog/*", "/products/*"},
        RedirectStatus:  302,
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

    if cfg.BBaseURL == "" {
        return nil, errors.New("B_BASE_URL is required (env or config.json)")
    }
    if _, err := url.Parse(cfg.BBaseURL); err != nil {
        return nil, fmt.Errorf("invalid B_BASE_URL: %w", err)
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
        // Only set to false if explicitly false in file (JSON default false)
        // Here we detect presence by comparing to zero-value with patterns length; accept provided value unconditionally.
        dst.CacheAll = src.CacheAll
    }
    if len(src.CachePatterns) != 0 {
        dst.CachePatterns = src.CachePatterns
    }
    if src.RedirectStatus != 0 {
        dst.RedirectStatus = src.RedirectStatus
    }
}

func isBot(r *http.Request) bool {
    // Allow forcing detection for testing
    if r.Header.Get("X-Bot") == "true" {
        return true
    }
    ua := strings.ToLower(r.UserAgent())
    if ua == "" {
        return false
    }
    bots := []string{
        "googlebot", "bingbot", "slurp", "duckduckbot", "baiduspider",
        "yandexbot", "sogou", "exabot", "facebot", "facebookexternalhit",
        "ia_archiver", "applebot", "semrushbot", "mj12bot", "ahrefsbot",
        "petalbot", "seznambot", "dotbot",
    }
    for _, b := range bots {
        if strings.Contains(ua, b) {
            return true
        }
    }
    return false
}

func patternsMatch(patterns []string, reqPath string) bool {
    // normalize
    if !strings.HasPrefix(reqPath, "/") {
        reqPath = "/" + reqPath
    }
    for _, p := range patterns {
        p = strings.TrimSpace(p)
        if p == "" {
            continue
        }
        // Replace ** with * to keep implementation simple
        p = strings.ReplaceAll(p, "**", "*")
        ok, err := path.Match(p, reqPath)
        if err == nil && ok {
            return true
        }
        // Allow prefix-only pattern like "/blog/" to match
        if strings.HasSuffix(p, "/") && strings.HasPrefix(reqPath, p) {
            return true
        }
    }
    return false
}

func cacheKey(u string) string {
    h := sha1.Sum([]byte(u))
    return hex.EncodeToString(h[:])
}

func cachePath(dir, key string) string {
    return filepath.Join(dir, key+".json")
}

func readCache(dir, key string) (*cacheEntry, error) {
    p := cachePath(dir, key)
    b, err := os.ReadFile(p)
    if err != nil {
        return nil, err
    }
    var ce cacheEntry
    if err := json.Unmarshal(b, &ce); err != nil {
        return nil, err
    }
    if time.Now().Unix() >= ce.ExpiresAt {
        return nil, errors.New("cache expired")
    }
    return &ce, nil
}

func writeCache(dir, key string, ce *cacheEntry) error {
    if err := os.MkdirAll(dir, 0o755); err != nil {
        return err
    }
    p := cachePath(dir, key)
    tmp := p + ".tmp"
    b, err := json.Marshal(ce)
    if err != nil {
        return err
    }
    if err := os.WriteFile(tmp, b, 0o644); err != nil {
        return err
    }
    return os.Rename(tmp, p)
}

func copyImportantHeaders(dst http.ResponseWriter, src *http.Response) {
    // Only a minimal, safe subset
    if v := src.Header.Get("Content-Type"); v != "" {
        dst.Header().Set("Content-Type", v)
    }
    if v := src.Header.Get("Last-Modified"); v != "" {
        dst.Header().Set("Last-Modified", v)
    }
    if v := src.Header.Get("ETag"); v != "" {
        dst.Header().Set("ETag", v)
    }
}

func serveFromCache(w http.ResponseWriter, ce *cacheEntry) {
    for k, v := range ce.Header {
        w.Header().Set(k, v)
    }
    w.WriteHeader(ce.Status)
    if len(ce.Body) > 0 {
        _, _ = w.Write(ce.Body)
    }
}

func main() {
    cfg, err := loadConfig()
    if err != nil {
        log.Fatalf("config error: %v", err)
    }

    log.Printf("Starting A-site on %s, proxying bots from %s", cfg.ListenAddr, cfg.BBaseURL)

    client := &http.Client{Timeout: 15 * time.Second}

    http.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/plain; charset=utf-8")
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("User-agent: *\nAllow: /\n"))
    })

    http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("ok"))
    })

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        // Build target URL on B-site
        target := strings.TrimRight(cfg.BBaseURL, "/") + r.URL.RequestURI()

        // If human, redirect directly to B-site
        if !isBot(r) {
            http.Redirect(w, r, target, cfg.RedirectStatus)
            return
        }

        // Bots: fetch content from B-site (with caching)
        methodCacheable := r.Method == http.MethodGet || r.Method == http.MethodHead
        allowCache := cfg.CacheAll || patternsMatch(cfg.CachePatterns, r.URL.Path)
        if methodCacheable && allowCache {
            key := cacheKey(target)
            if ce, err := readCache(cfg.CacheDir, key); err == nil && ce.Status == http.StatusOK {
                serveFromCache(w, ce)
                return
            }
            // miss or expired: fetch and populate cache
            req, _ := http.NewRequest(r.Method, target, nil)
            // Forward minimal headers to appear normal to origin
            req.Header.Set("User-Agent", r.UserAgent())
            if v := r.Header.Get("Accept"); v != "" {
                req.Header.Set("Accept", v)
            }
            resp, err := client.Do(req)
            if err != nil {
                log.Printf("fetch error: %v", err)
                http.Error(w, "upstream fetch error", http.StatusBadGateway)
                return
            }
            defer resp.Body.Close()

            body, _ := io.ReadAll(resp.Body)

            // Prepare cache entry (store minimal headers)
            ch := map[string]string{}
            if ct := resp.Header.Get("Content-Type"); ct != "" {
                ch["Content-Type"] = ct
            }
            if lm := resp.Header.Get("Last-Modified"); lm != "" {
                ch["Last-Modified"] = lm
            }
            if et := resp.Header.Get("ETag"); et != "" {
                ch["ETag"] = et
            }

            if resp.StatusCode == http.StatusOK {
                ce := &cacheEntry{
                    URL:       target,
                    CreatedAt: time.Now().Unix(),
                    ExpiresAt: time.Now().Add(time.Duration(cfg.CacheTTLSeconds) * time.Second).Unix(),
                    Status:    resp.StatusCode,
                    Header:    ch,
                    Body:      body,
                }
                if err := writeCache(cfg.CacheDir, key, ce); err != nil {
                    log.Printf("cache write error: %v", err)
                }
            }

            // Serve response
            for k, v := range ch {
                w.Header().Set(k, v)
            }
            w.WriteHeader(resp.StatusCode)
            if len(body) > 0 && r.Method == http.MethodGet {
                _, _ = w.Write(body)
            }
            return
        }

        // Not cached or caching disabled: simple fetch-through for bots
        req, _ := http.NewRequest(r.Method, target, r.Body)
        // Since it's a bot path but not cached, just forward as closely as feasible
        req.Header.Set("User-Agent", r.UserAgent())
        if v := r.Header.Get("Accept"); v != "" {
            req.Header.Set("Accept", v)
        }
        resp, err := client.Do(req)
        if err != nil {
            log.Printf("fetch error: %v", err)
            http.Error(w, "upstream fetch error", http.StatusBadGateway)
            return
        }
        defer resp.Body.Close()
        copyImportantHeaders(w, resp)
        w.WriteHeader(resp.StatusCode)
        if r.Method == http.MethodGet {
            _, _ = io.Copy(w, resp.Body)
        }
    })

    if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
        log.Fatalf("failed to create cache dir: %v", err)
    }

    srv := &http.Server{Addr: cfg.ListenAddr}
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        log.Fatalf("server error: %v", err)
    }
}
