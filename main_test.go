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
    "time"
    "net/url"
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

    // Do not follow redirects so we can assert status code
    client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
    req, _ := http.NewRequest("GET", srv.URL+"/foo?x=1", nil)
    req.Header.Set("User-Agent", "Mozilla/5.0")
    resp, err := client.Do(req)
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

func TestHumanPrefetchWarmsCache(t *testing.T) {
    // Upstream serves simple page but we won't fetch inline; prefetcher should warm
    upCalls := int32(0)
    up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        atomic.AddInt32(&upCalls, 1)
        w.Header().Set("Content-Type", "text/html")
        io.WriteString(w, "<html><body>ok</body></html>")
    }))
    defer up.Close()

    cfg := newTestCfg(t, up.URL)
    h := buildHandler(cfg)
    srv := httptest.NewServer(h)
    defer srv.Close()

    // Human request triggers redirect and background prefetch
    client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
    r, err := client.Get(srv.URL + "/warm/me?x=1")
    if err != nil { t.Fatal(err) }
    r.Body.Close()
    if r.StatusCode < 300 || r.StatusCode >= 400 { t.Fatalf("expected redirect, got %d", r.StatusCode) }

    // Wait briefly for prefetch
    target := strings.TrimRight(cfg.BBaseURL, "/") + "/warm/me?x=1"
    var ok bool
    for i := 0; i < 50; i++ { // up to ~1s
        p, _ := cacheFilePathForURL(cfg.CacheDir, target)
        if _, err := os.Stat(p); err == nil {
            ok = true
            break
        }
        time.Sleep(20 * time.Millisecond)
    }
    if !ok {
        t.Fatalf("expected cache warmed in background for %s", target)
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
    p, _ := cacheFilePathForURL(cfg.CacheDir, target)
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

    p1, _ := cacheFilePathForURL(cfg.CacheDir, target1)
    if _, err := os.Stat(p1); !os.IsNotExist(err) {
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

func TestCacheFilePathForURL(t *testing.T) {
    dir := t.TempDir()
    cases := []struct{
        raw string
        want string
        suffix string
    }{
        {"https://b.com/", filepath.Join(dir, "b.com", "index.json"), ""},
        {"https://b.com/foo", filepath.Join(dir, "b.com", "foo", "index.json"), ""},
        {"https://b.com/foo/bar/", filepath.Join(dir, "b.com", "foo", "bar", "index.json"), ""},
    }
    for _, c := range cases {
        got, err := cacheFilePathForURL(dir, c.raw)
        if err != nil { t.Fatalf("unexpected error: %v", err) }
        if got != c.want {
            t.Fatalf("for %s want %s got %s", c.raw, c.want, got)
        }
    }
    // Query variants should differ
    p1, _ := cacheFilePathForURL(dir, "https://b.com/foo?q=1")
    p2, _ := cacheFilePathForURL(dir, "https://b.com/foo?q=2")
    if filepath.Dir(p1) != filepath.Join(dir, "b.com", "foo") { t.Fatalf("unexpected dir: %s", filepath.Dir(p1)) }
    if p1 == p2 { t.Fatalf("expected different file names for different queries: %s == %s", p1, p2) }
    pNoQ, _ := cacheFilePathForURL(dir, "https://b.com/foo")
    if filepath.Base(pNoQ) != "index.json" { t.Fatalf("expected index.json for no-query, got %s", filepath.Base(pNoQ)) }
}

func TestRobotsTxtFetchedAndRewritten(t *testing.T) {
    up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/plain")
        // Use upstream host in content so rewrite can match
        io.WriteString(w, "User-agent: *\nAllow: /\nSitemap: https://"+r.Host+"/real-sitemap.xml\n")
    }))
    defer up.Close()

    cfg := newTestCfg(t, up.URL)
    h := buildHandler(cfg)
    srv := httptest.NewServer(h)
    defer srv.Close()

    r, err := http.Get(srv.URL + "/robots.txt")
    if err != nil { t.Fatal(err) }
    b, _ := io.ReadAll(r.Body)
    r.Body.Close()
    if r.StatusCode != 200 { t.Fatalf("expected 200, got %d", r.StatusCode) }
    sb := string(b)
    u, _ := url.Parse(srv.URL)
    if !strings.Contains(sb, u.Host) {
        t.Fatalf("expected robots to contain A-site host %s, got: %s", u.Host, sb)
    }
}

func TestSitemapRewriteForBots(t *testing.T) {
    up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/xml")
        sitemap := "<?xml version=\\\"1.0\\\" encoding=\\\"UTF-8\\\"?>\n" +
            "<urlset xmlns=\\\"http://www.sitemaps.org/schemas/sitemap/0.9\\\">\n" +
            "  <url><loc>https://" + r.Host + "/blog/post1</loc></url>\n" +
            "  <url><loc>https://" + r.Host + "/blog/post2</loc></url>\n" +
            "</urlset>"
        io.WriteString(w, sitemap)
    }))
    defer up.Close()

    cfg := newTestCfg(t, up.URL)
    h := buildHandler(cfg)
    srv := httptest.NewServer(h)
    defer srv.Close()

    req, _ := http.NewRequest("GET", srv.URL+"/sitemap.xml", nil)
    req.Header.Set("User-Agent", "Googlebot")
    resp, err := http.DefaultClient.Do(req)
    if err != nil { t.Fatal(err) }
    b, _ := io.ReadAll(resp.Body)
    resp.Body.Close()
    if resp.StatusCode != 200 { t.Fatalf("expected 200, got %d", resp.StatusCode) }
    sb := string(b)
    au, _ := url.Parse(srv.URL)
    if !strings.Contains(sb, au.Host) {
        t.Fatalf("expected sitemap URLs rewritten to A-site host %s, got: %s", au.Host, sb)
    }
}

func TestHumanGetsSitemapOnAWithoutRedirect(t *testing.T) {
    up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/xml")
        io.WriteString(w, "<urlset><url><loc>https://"+r.Host+"/wp-post</loc></url></urlset>")
    }))
    defer up.Close()

    cfg := newTestCfg(t, up.URL)
    h := buildHandler(cfg)
    srv := httptest.NewServer(h)
    defer srv.Close()

    // Human UA (not a bot). Should not redirect for sitemap path
    req, _ := http.NewRequest("GET", srv.URL+"/wp-sitemap.xml", nil)
    req.Header.Set("User-Agent", "Mozilla/5.0")
    resp, err := http.DefaultClient.Do(req)
    if err != nil { t.Fatal(err) }
    b, _ := io.ReadAll(resp.Body)
    resp.Body.Close()
    if resp.StatusCode != 200 {
        t.Fatalf("expected 200, got %d", resp.StatusCode)
    }
    // Ensure content host is rewritten to A-site
    au, _ := url.Parse(srv.URL)
    if !strings.Contains(string(b), au.Host) {
        t.Fatalf("expected sitemap URLs rewritten to A host %s, got: %s", au.Host, string(b))
    }
}
