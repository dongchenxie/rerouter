package main

import (
    "io"
    "log"
    "net/http"
    "net/url"
    "sync"
    "time"
)

type prefetchJob struct {
    target string
    aBase  string // optional A-site base URL for rewriting
}

type Prefetcher struct {
    cfg     *Config
    client  *http.Client
    jobs    chan prefetchJob
    inFlight sync.Map // target -> struct{}
}

func NewPrefetcher(cfg *Config) *Prefetcher {
    return &Prefetcher{
        cfg:    cfg,
        client: &http.Client{Timeout: 15 * time.Second},
        jobs:   make(chan prefetchJob, 256),
    }
}

func (p *Prefetcher) Start(workers int) {
    if workers <= 0 { workers = 2 }
    for i := 0; i < workers; i++ {
        go p.worker()
    }
}

func (p *Prefetcher) Enqueue(target string, aBase string) {
    if _, exists := p.inFlight.LoadOrStore(target, struct{}{}); exists {
        return
    }
    select {
    case p.jobs <- prefetchJob{target: target, aBase: aBase}:
        // enqueued
    default:
        // queue full; drop and clear inFlight marker
        p.inFlight.Delete(target)
    }
}

func (p *Prefetcher) worker() {
    for job := range p.jobs {
        p.handle(job)
        p.inFlight.Delete(job.target)
    }
}

func (p *Prefetcher) handle(job prefetchJob) {
    // Skip if cache fresh
    if ce, err := readCacheByURL(p.cfg.CacheDir, job.target); err == nil && ce.Status == http.StatusOK {
        return
    }
    // Fetch
    req, _ := http.NewRequest(http.MethodGet, job.target, nil)
    // Use a neutral UA
    req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Prefetcher)")
    resp, err := p.client.Do(req)
    if err != nil {
        log.Printf("prefetch fetch error: %v", err)
        return
    }
    defer resp.Body.Close()
    body, _ := io.ReadAll(resp.Body)

    // Headers (minimal)
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

    // Optional rewrite if aBase provided and HTML
    if job.aBase != "" {
        if aURL, err := url.Parse(job.aBase); err == nil {
            if bURL, err2 := url.Parse(p.cfg.BBaseURL); err2 == nil {
                if newBody, rewrote := rewriteBodyForBots(body, ch["Content-Type"], aURL, bURL); rewrote {
                    body = newBody
                    delete(ch, "ETag")
                    delete(ch, "Last-Modified")
                }
            }
        }
    }

    if resp.StatusCode == http.StatusOK {
        ce := &cacheEntry{
            URL:       job.target,
            CreatedAt: time.Now().Unix(),
            ExpiresAt: time.Now().Add(time.Duration(p.cfg.CacheTTLSeconds) * time.Second).Unix(),
            Status:    resp.StatusCode,
            Header:    ch,
            Body:      body,
        }
        if err := writeCacheByURL(p.cfg.CacheDir, job.target, ce); err != nil {
            log.Printf("prefetch cache write error: %v", err)
        }
    }
}

