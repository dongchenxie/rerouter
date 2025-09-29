package main

import (
    "encoding/json"
    "io"
    "log"
    "net/http"
    "net/url"
    "os"
    "path/filepath"
    "strings"
    "time"
)

func buildHandler(cfg *Config) http.Handler {
    client := &http.Client{Timeout: 15 * time.Second}
    // Start background prefetcher for human-triggered warming
    pf := NewPrefetcher(cfg)
    pf.Start(2)
    mux := http.NewServeMux()

    mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
        target := strings.TrimRight(cfg.BBaseURL, "/") + "/robots.txt"
        if ce, err := readCacheByURL(cfg.CacheDir, target); err == nil && ce.Status == http.StatusOK {
            // Re-rewrite with current A if needed
            aURL := deriveABaseURL(cfg, r)
            bURL, _ := url.Parse(cfg.BBaseURL)
            body := ce.Body
            if nb, rw := rewriteBToA(body, aURL, bURL); rw {
                // Drop validators if present
                w.Header().Set("Content-Type", ce.Header["Content-Type"])
                w.WriteHeader(ce.Status)
                _, _ = w.Write(nb)
                return
            }
            serveFromCache(w, ce)
            return
        }
        req, _ := http.NewRequest(http.MethodGet, target, nil)
        req.Header.Set("User-Agent", r.UserAgent())
        resp, err := client.Do(req)
        if err != nil {
            http.Error(w, "upstream fetch error", http.StatusBadGateway)
            return
        }
        defer resp.Body.Close()
        body, _ := io.ReadAll(resp.Body)
        ct := resp.Header.Get("Content-Type")
        if ct == "" { ct = "text/plain; charset=utf-8" }
        aURL := deriveABaseURL(cfg, r)
        bURL, _ := url.Parse(cfg.BBaseURL)
        body, rewrote := rewriteBToA(body, aURL, bURL)
        headers := map[string]string{"Content-Type": ct}
        if !rewrote {
            if v := resp.Header.Get("Last-Modified"); v != "" { headers["Last-Modified"] = v }
            if v := resp.Header.Get("ETag"); v != "" { headers["ETag"] = v }
        }
        if resp.StatusCode == http.StatusOK {
            ce := &cacheEntry{URL: target, CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Duration(cfg.CacheTTLSeconds)*time.Second).Unix(), Status: resp.StatusCode, Header: headers, Body: body}
            _ = writeCacheByURL(cfg.CacheDir, target, ce)
        }
        for k, v := range headers { w.Header().Set(k, v) }
        w.WriteHeader(resp.StatusCode)
        if len(body) > 0 { _, _ = w.Write(body) }
    })

    mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("ok"))
    })

    // Admin purge endpoint: POST/DELETE /admin/purge?url=...&partial=1
    mux.HandleFunc("/admin/purge", func(w http.ResponseWriter, r *http.Request) {
        if cfg.AdminToken == "" {
            http.Error(w, "admin disabled: set ADMIN_TOKEN", http.StatusForbidden)
            return
        }
        token := r.Header.Get("X-Admin-Token")
        if token == "" {
            token = r.URL.Query().Get("token")
        }
        if token != cfg.AdminToken {
            http.Error(w, "forbidden", http.StatusForbidden)
            return
        }

        if r.Method != http.MethodPost && r.Method != http.MethodDelete {
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
            return
        }
        _ = r.ParseForm()
        q := r.FormValue("url")
        if q == "" {
            q = r.FormValue("q")
        }
        partial := r.FormValue("partial") == "1" || strings.ToLower(r.FormValue("partial")) == "true"
        // Support JSON body: {"url":"...","partial":true}
        if q == "" && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
            var body struct {
                URL     string `json:"url"`
                Partial bool   `json:"partial"`
            }
            b, _ := io.ReadAll(r.Body)
            _ = json.Unmarshal(b, &body)
            q = body.URL
            partial = partial || body.Partial
        }
        if q == "" {
            http.Error(w, "missing url", http.StatusBadRequest)
            return
        }

        // If q is a path, convert to absolute on B-site
        fullURL := q
        if u, err := url.Parse(q); err == nil {
            if u.Scheme == "" { // treat as path
                if !strings.HasPrefix(q, "/") {
                    q = "/" + q
                }
                fullURL = strings.TrimRight(cfg.BBaseURL, "/") + q
            }
        }

        type result struct {
            Deleted int      `json:"deleted"`
            Files   []string `json:"files"`
        }
        res := result{}

        if !partial {
            p, perr := cacheFilePathForURL(cfg.CacheDir, fullURL)
            if perr != nil {
                http.Error(w, "invalid url", http.StatusBadRequest)
                return
            }
            if _, err := os.Stat(p); err == nil {
                if err := os.Remove(p); err == nil {
                    res.Deleted = 1
                    res.Files = append(res.Files, filepath.Base(p))
                }
            }
        } else {
            // Partial substring match over cached entries' URLs
            files, _ := walkCacheJSONFiles(cfg.CacheDir)
            for _, p := range files {
                b, err := os.ReadFile(p)
                if err != nil { continue }
                var ce cacheEntry
                if err := json.Unmarshal(b, &ce); err != nil { continue }
                if strings.Contains(ce.URL, q) || strings.Contains(ce.URL, fullURL) {
                    if err := os.Remove(p); err == nil {
                        res.Deleted++
                        res.Files = append(res.Files, p)
                    }
                }
            }
        }

        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(res)
    })

    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        // Build target URL on B-site
        target := strings.TrimRight(cfg.BBaseURL, "/") + r.URL.RequestURI()

        // If human, redirect directly to B-site unless this is a sitemap path
        if !isBot(r) && !isSitemapPath(r.URL.Path) {
            // Warm cache asynchronously (non-blocking)
            a := deriveABaseURL(cfg, r)
            pf.Enqueue(target, a.String())
            http.Redirect(w, r, target, cfg.RedirectStatus)
            return
        }

        // Bots: fetch content from B-site (with caching)
        methodCacheable := r.Method == http.MethodGet || r.Method == http.MethodHead
        allowCache := cfg.CacheAll || patternsMatch(cfg.CachePatterns, r.URL.Path)
        if methodCacheable && allowCache {
            if ce, err := readCacheByURL(cfg.CacheDir, target); err == nil && ce.Status == http.StatusOK {
                if isSitemapPath(r.URL.Path) {
                    // Ensure sitemap content is rewritten even if cache is from older version
                    aURL := deriveABaseURL(cfg, r)
                    bURL, _ := url.Parse(cfg.BBaseURL)
                    body := ce.Body
                    if nb, rw := rewriteBToA(body, aURL, bURL); rw {
                        // Copy content-type only
                        if v := ce.Header["Content-Type"]; v != "" { w.Header().Set("Content-Type", v) }
                        w.WriteHeader(ce.Status)
                        _, _ = w.Write(nb)
                        return
                    }
                }
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

            // Rewrite body links from B -> A for bots (HTML/XML), force for sitemap
            aURL := deriveABaseURL(cfg, r)
            bURL, _ := url.Parse(cfg.BBaseURL)
            if strings.Contains(strings.ToLower(r.URL.Path), "sitemap") {
                if nb, rw := rewriteBToA(body, aURL, bURL); rw {
                    body = nb
                    delete(ch, "ETag")
                    delete(ch, "Last-Modified")
                }
            } else {
                if nb, rw := rewriteBodyForBots(body, ch["Content-Type"], aURL, bURL); rw {
                    body = nb
                    delete(ch, "ETag")
                    delete(ch, "Last-Modified")
                }
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
                if err := writeCacheByURL(cfg.CacheDir, target, ce); err != nil {
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
        // Read body to potentially rewrite before serving
        body, _ := io.ReadAll(resp.Body)
        ct := resp.Header.Get("Content-Type")
        aURL := deriveABaseURL(cfg, r)
        bURL, _ := url.Parse(cfg.BBaseURL)
        rewrote := false
        if strings.Contains(strings.ToLower(r.URL.Path), "sitemap") {
            if nb, rw := rewriteBToA(body, aURL, bURL); rw { body = nb; rewrote = true }
        } else {
            if nb, rw := rewriteBodyForBots(body, ct, aURL, bURL); rw { body = nb; rewrote = true }
        }

        // Copy minimal headers, but drop validators if rewritten
        if v := ct; v != "" { w.Header().Set("Content-Type", v) }
        if !rewrote {
            if v := resp.Header.Get("Last-Modified"); v != "" { w.Header().Set("Last-Modified", v) }
            if v := resp.Header.Get("ETag"); v != "" { w.Header().Set("ETag", v) }
        }
        w.WriteHeader(resp.StatusCode)
        if r.Method == http.MethodGet && len(body) > 0 {
            _, _ = w.Write(body)
        }
    })

    return mux
}
