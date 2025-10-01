package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"rerouter/logger"
	"sync"
	"time"
)

type prefetchJob struct {
	target string
	aBase  string // optional A-site base URL for rewriting
}

type Prefetcher struct {
	cfg      *Config
	client   *http.Client
	jobs     chan prefetchJob
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
	if workers <= 0 {
		workers = 2
	}
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
		if _, err := p.handle(job); err != nil {
			// Errors already logged inside handle.
		}
		p.inFlight.Delete(job.target)
	}
}

func (p *Prefetcher) FetchAndStore(target, aBase string) (bool, error) {
	if target == "" {
		return false, fmt.Errorf("empty target")
	}
	if _, exists := p.inFlight.LoadOrStore(target, struct{}{}); exists {
		return true, nil
	}
	defer p.inFlight.Delete(target)
	return p.handle(prefetchJob{target: target, aBase: aBase})
}

func (p *Prefetcher) handle(job prefetchJob) (bool, error) {
	// Skip if cache fresh
	if ce, err := readCacheByURL(p.cfg.CacheDir, job.target); err == nil && ce.Status == http.StatusOK {
		return true, nil
	}
	// Fetch
	req, err := http.NewRequest(http.MethodGet, job.target, nil)
	if err != nil {
		logger.Warnw("prefetch_build_request_error", map[string]interface{}{"err": err.Error(), "target": job.target})
		return false, err
	}
	// Use a neutral UA
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Prefetcher)")
	resp, err := p.client.Do(req)
	if err != nil {
		logger.Warnw("prefetch_fetch_error", map[string]interface{}{"err": err.Error(), "target": job.target})
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Warnw("prefetch_read_error", map[string]interface{}{"err": err.Error(), "target": job.target})
		return false, err
	}

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
		// Determine TTL based on target path
		ttl := p.cfg.CacheTTLSeconds
		if u, err := url.Parse(job.target); err == nil {
			ttl = cacheTTLForPath(p.cfg, u.Path)
		}
		ce := &cacheEntry{
			URL:       job.target,
			CreatedAt: time.Now().Unix(),
			ExpiresAt: time.Now().Add(time.Duration(ttl) * time.Second).Unix(),
			Status:    resp.StatusCode,
			Header:    ch,
			Body:      body,
		}
		if err := writeCacheByURL(p.cfg.CacheDir, job.target, ce); err != nil {
			logger.Warnw("prefetch_cache_write_error", map[string]interface{}{"err": err.Error(), "target": job.target})
			return false, err
		}
		logger.Debugw("cache_store", map[string]interface{}{"target": job.target, "ttl_seconds": ttl, "source": "prefetch"})
		return true, nil
	}

	logger.Warnw("prefetch_unexpected_status", map[string]interface{}{"status": resp.StatusCode, "target": job.target})
	return false, fmt.Errorf("prefetch status %d", resp.StatusCode)
}
