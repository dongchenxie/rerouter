package main

import (
    "encoding/json"
    "io"
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "strings"
    "sync/atomic"
    "testing"
)

func newTestCfg(t *testing.T, bURL string) *Config {
    t.Helper()
    dir := t.TempDir()
    return &Config{
        BBaseURL:        bURL,
        ListenAddr:      ":0",
        CacheDir:        dir,
        CacheTTLSeconds: 3600,
        CacheAll:        true,
        RedirectStatus:  302,
        AdminToken:      "secret",
    }
}

func TestHumanRedirects(t *testing.T) {
    up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(200)
        io.WriteString(w, "ok")
    }))
    defer up.Close()

    cfg := newTestCfg(t, up.URL)
    h := buildHandler(cfg)
    srv := httptest.NewServer(h)
    defer srv.Close()

    req, _ := http.NewRequest("GET", srv.URL+"/foo?x=1", nil)
    req.Header.Set("User-Agent", "Mozilla/5.0")
    resp, err := http.DefaultClient.Do(req)
    if err != nil { t.Fatal(err) }
    defer resp.Body.Close()
    if resp.StatusCode != cfg.RedirectStatus {
        t.Fatalf("expected redirect %d, got %d", cfg.RedirectStatus, resp.StatusCode)
    }
    loc := resp.Header.Get("Location")
    want := strings.TrimRight(cfg.BBaseURL, "/") + "/foo?x=1"
    if loc != want {
        t.Fatalf("expected Location %q, got %q", want, loc)
    }
}

func TestBotCaches200(t *testing.T) {
    var calls int32
    up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        atomic.AddInt32(&calls, 1)
        w.Header().Set("Content-Type", "text/html")
        io.WriteString(w, "hello")
    }))
    defer up.Close()

    cfg := newTestCfg(t, up.URL)
    h := buildHandler(cfg)
    srv := httptest.NewServer(h)
    defer srv.Close()

    client := &http.Client{}
    req1, _ := http.NewRequest("GET", srv.URL+"/page", nil)
    req1.Header.Set("User-Agent", "Googlebot")
    r1, err := client.Do(req1)
    if err != nil { t.Fatal(err) }
    io.ReadAll(r1.Body); r1.Body.Close()

    req2, _ := http.NewRequest("GET", srv.URL+"/page", nil)
    req2.Header.Set("User-Agent", "Googlebot")
    r2, err := client.Do(req2)
    if err != nil { t.Fatal(err) }
    io.ReadAll(r2.Body); r2.Body.Close()

    if atomic.LoadInt32(&calls) != 1 {
        t.Fatalf("expected upstream called once, got %d", calls)
    }
}

func TestBotDoesNotCacheNon200(t *testing.T) {
    var calls int32
    up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        atomic.AddInt32(&calls, 1)
        w.WriteHeader(404)
        io.WriteString(w, "missing")
    }))
    defer up.Close()

    cfg := newTestCfg(t, up.URL)
    h := buildHandler(cfg)
    srv := httptest.NewServer(h)
    defer srv.Close()

    client := &http.Client{}
    for i := 0; i < 2; i++ {
        req, _ := http.NewRequest("GET", srv.URL+"/nope", nil)
        req.Header.Set("User-Agent", "Googlebot")
        r, err := client.Do(req)
        if err != nil { t.Fatal(err) }
        io.ReadAll(r.Body); r.Body.Close()
    }
    if atomic.LoadInt32(&calls) != 2 {
        t.Fatalf("expected upstream called twice (no cache), got %d", calls)
    }

    // Ensure no cache file exists
    target := strings.TrimRight(cfg.BBaseURL, "/") + "/nope"
    key := cacheKey(target)
    p := cachePath(cfg.CacheDir, key)
    if _, err := os.Stat(p); err == nil {
        t.Fatalf("expected no cache file, but found %s", p)
    }
}

func TestPurgeExactAndPartial(t *testing.T) {
    up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        io.WriteString(w, "ok")
    }))
    defer up.Close()

    cfg := newTestCfg(t, up.URL)
    if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
        t.Fatal(err)
    }
    h := buildHandler(cfg)
    srv := httptest.NewServer(h)
    defer srv.Close()

    // Seed two cached pages by requesting as a bot
    client := &http.Client{}
    for _, p := range []string{"/a/page1", "/a/page2"} {
        req, _ := http.NewRequest("GET", srv.URL+p, nil)
        req.Header.Set("User-Agent", "Googlebot")
        r, err := client.Do(req)
        if err != nil { t.Fatal(err) }
        io.ReadAll(r.Body); r.Body.Close()
    }

    // Exact purge page1
    target1 := strings.TrimRight(cfg.BBaseURL, "/") + "/a/page1"
    purgeReq, _ := http.NewRequest("POST", srv.URL+"/admin/purge?url="+urlQueryEscape(target1), nil)
    purgeReq.Header.Set("X-Admin-Token", cfg.AdminToken)
    pr, err := client.Do(purgeReq)
    if err != nil { t.Fatal(err) }
    var res1 map[string]any
    json.NewDecoder(pr.Body).Decode(&res1)
    pr.Body.Close()

    key1 := cacheKey(target1)
    if _, err := os.Stat(cachePath(cfg.CacheDir, key1)); !os.IsNotExist(err) {
        t.Fatalf("expected page1 cache removed")
    }

    // Partial purge for remaining under "/a/"
    purgeReq2, _ := http.NewRequest("POST", srv.URL+"/admin/purge?url=/a/&partial=1", nil)
    purgeReq2.Header.Set("X-Admin-Token", cfg.AdminToken)
    pr2, err := client.Do(purgeReq2)
    if err != nil { t.Fatal(err) }
    var res2 map[string]any
    json.NewDecoder(pr2.Body).Decode(&res2)
    pr2.Body.Close()

    // Ensure directory is empty
    files, _ := os.ReadDir(cfg.CacheDir)
    for _, f := range files {
        if strings.HasSuffix(f.Name(), ".json") {
            t.Fatalf("expected no cache files, found %s", f.Name())
        }
    }
}

// helper to safely escape URL for query param without pulling net/url in test imports duplication
func urlQueryEscape(s string) string {
    // minimal escape for space and others used in tests
    r := strings.NewReplacer(" ", "%20", "\n", "%0A")
    return r.Replace(s)
}

func TestAdminAuthRequired(t *testing.T) {
    up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
    defer up.Close()
    cfg := newTestCfg(t, up.URL)
    h := buildHandler(cfg)
    srv := httptest.NewServer(h)
    defer srv.Close()

    r, err := http.Post(srv.URL+"/admin/purge?url=/", "application/json", nil)
    if err != nil { t.Fatal(err) }
    if r.StatusCode != http.StatusForbidden {
        t.Fatalf("expected 403, got %d", r.StatusCode)
    }
}

