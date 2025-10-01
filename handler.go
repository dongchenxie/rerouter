package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"rerouter/logger"
	"strconv"
	"strings"
	"time"
)

type purgeResult struct {
	Deleted int      `json:"deleted"`
	Files   []string `json:"files"`
}

func doPurge(cfg *Config, q string, partial bool) (purgeResult, error) {
	res := purgeResult{}
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
	if !partial {
		p, perr := cacheFilePathForURL(cfg.CacheDir, fullURL)
		if perr != nil {
			return res, perr
		}
		if _, err := os.Stat(p); err == nil {
			if err := os.Remove(p); err == nil {
				res.Deleted = 1
				res.Files = append(res.Files, filepath.Base(p))
			}
		}
	} else {
		files, _ := walkCacheJSONFiles(cfg.CacheDir)
		for _, p := range files {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			var ce cacheEntry
			if err := json.Unmarshal(b, &ce); err != nil {
				continue
			}
			if strings.Contains(ce.URL, q) || strings.Contains(ce.URL, fullURL) {
				if err := os.Remove(p); err == nil {
					res.Deleted++
					res.Files = append(res.Files, p)
				}
			}
		}
	}
	return res, nil
}

func buildHandler(cfg *Config) http.Handler {
	client := &http.Client{Timeout: 15 * time.Second}
	// Start background prefetcher for human-triggered warming
	pf := NewPrefetcher(cfg)
	pf.Start(2)
	sitemapClient := newSitemapHTTPClient(30 * time.Second)
	warmMgr := newSitemapWarmManager(cfg, pf, sitemapClient)
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
				w.Header().Set("X-Cache", "HIT")
				setCacheMetaHeaders(w, ce)
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
			logger.Errorw("robots_fetch_error", map[string]interface{}{"err": err.Error(), "target": target, "req_id": getRequestID(r.Context())})
			http.Error(w, "upstream fetch error", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			ct = "text/plain; charset=utf-8"
		}
		aURL := deriveABaseURL(cfg, r)
		bURL, _ := url.Parse(cfg.BBaseURL)
		body, rewrote := rewriteBToA(body, aURL, bURL)
		headers := map[string]string{"Content-Type": ct}
		if !rewrote {
			if v := resp.Header.Get("Last-Modified"); v != "" {
				headers["Last-Modified"] = v
			}
			if v := resp.Header.Get("ETag"); v != "" {
				headers["ETag"] = v
			}
		}
		if resp.StatusCode == http.StatusOK {
			ttl := cacheTTLForPath(cfg, "/robots.txt")
			ce := &cacheEntry{URL: target, CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Duration(ttl) * time.Second).Unix(), Status: resp.StatusCode, Header: headers, Body: body}
			if err := writeCacheByURL(cfg.CacheDir, target, ce); err != nil {
				logger.Warnw("cache_write_error", map[string]interface{}{"err": err.Error(), "url": target, "req_id": getRequestID(r.Context())})
			} else {
				logger.Debugw("cache_store", map[string]interface{}{"req_id": getRequestID(r.Context()), "target": target, "ttl_seconds": ttl})
			}
		}
		w.Header().Set("X-Cache", "MISS")
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(resp.StatusCode)
		if len(body) > 0 {
			_, _ = w.Write(body)
		}
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
		res, perr := doPurge(cfg, q, partial)
		if perr != nil {
			http.Error(w, "invalid url", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
		logger.Infow("admin_purge", map[string]interface{}{
			"req_id":  getRequestID(r.Context()),
			"partial": partial,
			"query":   q,
			"deleted": res.Deleted,
		})
	})

	mux.HandleFunc("/admin/sitemap-cache/status", func(w http.ResponseWriter, r *http.Request) {
		if cfg.AdminToken == "" {
			http.Error(w, "admin disabled: set ADMIN_TOKEN", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
		jobID := r.URL.Query().Get("job")
		if jobID == "" {
			jobID = r.URL.Query().Get("job_id")
		}
		w.Header().Set("Content-Type", "application/json")
		if jobID != "" {
			if job, ok := warmMgr.GetJob(jobID); ok {
				_ = json.NewEncoder(w).Encode(job.snapshot())
				return
			}
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		jobs := warmMgr.ListJobs()
		statuses := make([]sitemapWarmJobStatus, 0, len(jobs))
		for _, job := range jobs {
			statuses = append(statuses, job.snapshot())
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"jobs": statuses})
	})

	mux.HandleFunc("/admin/sitemap-cache", func(w http.ResponseWriter, r *http.Request) {
		if cfg.AdminToken == "" {
			http.Error(w, "admin disabled: set ADMIN_TOKEN", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		token := r.Header.Get("X-Admin-Token")
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		var body struct {
			SitemapURL string `json:"sitemap_url"`
			MaxURLs    int    `json:"max_urls"`
			ABaseURL   string `json:"a_base_url"`
			Token      string `json:"token"`
		}

		if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			data, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(data, &body); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
		} else {
			_ = r.ParseForm()
			if token == "" {
				token = r.FormValue("token")
			}
			body.SitemapURL = r.FormValue("sitemap_url")
			if v := r.FormValue("max_urls"); v != "" {
				var n int
				fmt.Sscanf(v, "%d", &n)
				body.MaxURLs = n
			}
			body.ABaseURL = r.FormValue("a_base_url")
		}
		if body.Token != "" {
			token = body.Token
		}
		if token != cfg.AdminToken {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		body.SitemapURL = strings.TrimSpace(body.SitemapURL)
		if body.SitemapURL == "" {
			http.Error(w, "missing sitemap_url", http.StatusBadRequest)
			return
		}

		job, err := warmMgr.StartJob(body.SitemapURL, body.MaxURLs, body.ABaseURL)
		if err != nil {
			http.Error(w, "failed to start job", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		resp := map[string]interface{}{
			"job_id":      job.ID,
			"state":       string(job.State),
			"sitemap_url": job.SitemapURL,
			"status_url":  "/admin/sitemap-cache/status?job=" + url.QueryEscape(job.ID),
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			logger.Errorw("admin_sitemap_cache_write_error", map[string]interface{}{"err": err.Error()})
		}
	})

	// Admin UI page to purge cache at a long hashed path
	if cfg.AdminToken != "" && cfg.AdminUIPath != "" {
		mux.HandleFunc(cfg.AdminUIPath, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte(adminUIHTML()))
			case http.MethodPost:
				_ = r.ParseForm()
				formType := r.FormValue("form")
				token := r.FormValue("token")
				if token == "" {
					token = r.FormValue("password")
				}
				if token != cfg.AdminToken {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
				switch formType {
				case "purge":
					urlQ := r.FormValue("url")
					partial := r.FormValue("partial") == "1" || strings.ToLower(r.FormValue("partial")) == "true" || r.FormValue("partial") == "on"
					res, err := doPurge(cfg, urlQ, partial)
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					if err != nil {
						_, _ = w.Write([]byte("<p>Invalid URL</p>"))
						return
					}
					logger.Infow("admin_purge_ui", map[string]interface{}{"req_id": getRequestID(r.Context()), "partial": partial, "query": urlQ, "deleted": res.Deleted})
					_, _ = w.Write([]byte(renderPurgeResultHTML(urlQ, partial, res)))
				case "sitemap":
					sitemapURL := strings.TrimSpace(r.FormValue("sitemap_url"))
					if sitemapURL == "" {
						http.Error(w, "missing sitemap_url", http.StatusBadRequest)
						return
					}
					var maxURLs int
					if v := r.FormValue("max_urls"); v != "" {
						fmt.Sscanf(v, "%d", &maxURLs)
					}
					aBaseOverride := r.FormValue("a_base_url")
					job, err := warmMgr.StartJob(sitemapURL, maxURLs, aBaseOverride)
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					if err != nil {
						logger.Errorw("admin_sitemap_cache_ui_error", map[string]interface{}{"err": err.Error(), "sitemap": sitemapURL})
						_, _ = w.Write([]byte("<p>Failed to start sitemap caching.</p>"))
						return
					}
					logger.Infow("admin_sitemap_cache_queued", map[string]interface{}{
						"req_id":  getRequestID(r.Context()),
						"sitemap": sitemapURL,
						"job_id":  job.ID,
					})
					_, _ = w.Write([]byte(renderSitemapJobQueuedHTML(job)))
				default:
					http.Error(w, "bad request", http.StatusBadRequest)
				}
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Build target URL on B-site
		target := strings.TrimRight(cfg.BBaseURL, "/") + r.URL.RequestURI()

		// If human, redirect directly to B-site unless this is a sitemap path
		if !isBot(r) && !isSitemapPath(r.URL.Path) {
			// Warm cache asynchronously (non-blocking)
			a := deriveABaseURL(cfg, r)
			pf.Enqueue(target, a.String())
			logger.Infow("human_redirect", map[string]interface{}{"req_id": getRequestID(r.Context()), "target": target})
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
						w.Header().Set("X-Cache", "HIT")
						setCacheMetaHeaders(w, ce)
						if v := ce.Header["Content-Type"]; v != "" {
							w.Header().Set("Content-Type", v)
						}
						w.WriteHeader(ce.Status)
						_, _ = w.Write(nb)
						return
					}
				}
				serveFromCache(w, ce)
				logger.Debugw("cache_hit", map[string]interface{}{"req_id": getRequestID(r.Context()), "target": target})
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
				logger.Errorw("fetch_error", map[string]interface{}{"err": err.Error(), "target": target, "req_id": getRequestID(r.Context())})
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
				ttl := cacheTTLForPath(cfg, r.URL.Path)
				ce := &cacheEntry{
					URL:       target,
					CreatedAt: time.Now().Unix(),
					ExpiresAt: time.Now().Add(time.Duration(ttl) * time.Second).Unix(),
					Status:    resp.StatusCode,
					Header:    ch,
					Body:      body,
				}
				if err := writeCacheByURL(cfg.CacheDir, target, ce); err != nil {
					logger.Warnw("cache_write_error", map[string]interface{}{"err": err.Error(), "url": target, "req_id": getRequestID(r.Context())})
				} else {
					logger.Debugw("cache_store", map[string]interface{}{"req_id": getRequestID(r.Context()), "target": target, "ttl_seconds": ttl})
				}
			}

			// Serve response (cache miss)
			w.Header().Set("X-Cache", "MISS")
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
			logger.Errorw("fetch_error", map[string]interface{}{"err": err.Error(), "target": target, "req_id": getRequestID(r.Context())})
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
			if nb, rw := rewriteBToA(body, aURL, bURL); rw {
				body = nb
				rewrote = true
			}
		} else {
			if nb, rw := rewriteBodyForBots(body, ct, aURL, bURL); rw {
				body = nb
				rewrote = true
			}
		}

		// Copy minimal headers, but drop validators if rewritten
		w.Header().Set("X-Cache", "MISS")
		if v := ct; v != "" {
			w.Header().Set("Content-Type", v)
		}
		if !rewrote {
			if v := resp.Header.Get("Last-Modified"); v != "" {
				w.Header().Set("Last-Modified", v)
			}
			if v := resp.Header.Get("ETag"); v != "" {
				w.Header().Set("ETag", v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		if r.Method == http.MethodGet && len(body) > 0 {
			_, _ = w.Write(body)
		}
	})

	return mux
}

func adminUIHTML() string {
	return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Admin Tools</title>
  <style>
    body{font-family:system-ui,-apple-system,Segoe UI,Roboto,Ubuntu,Cantarell,Noto Sans,sans-serif;margin:2rem;line-height:1.5;color:#222;background:#f7f7f7}
    h1{margin-bottom:0.5rem}
    h2{margin-top:2rem}
    form{max-width:640px;padding:1rem;margin-top:1rem;border:1px solid #ddd;border-radius:8px;background:#fff;box-shadow:0 1px 2px rgba(0,0,0,0.08)}
    label{display:block;margin:.5rem 0 .25rem;font-weight:600;color:#333}
    input[type=text],input[type=password],input[type=number]{width:100%;padding:.5rem;border:1px solid #bbb;border-radius:6px;font:inherit}
    .row{display:flex;gap:1rem;align-items:center;margin-top:.5rem}
    .hint{color:#555;font-size:.95rem;margin-bottom:.5rem}
    button{margin-top:1rem;padding:.6rem 1.2rem;border:0;border-radius:6px;background:#0b5;color:#fff;cursor:pointer;font-weight:600}
    button:hover{background:#0a4}
    small{color:#666}
  </style>
  </head>
<body>
  <h1>Admin Utilities</h1>
  <section>
    <h2>Invalidate Cache Entry</h2>
    <p class="hint">Enter a path or absolute URL from the B site. Enable Partial to delete every cached item containing the value.</p>
    <form method="post">
      <input type="hidden" name="form" value="purge">
      <label for="url">URL or Path</label>
      <input type="text" id="url" name="url" placeholder="/blog/post or https://b.site/blog/post" required>
      <div class="row">
        <label><input type="checkbox" name="partial"> Partial purge</label>
      </div>
      <label for="password">Admin token</label>
      <input type="password" id="password" name="password" placeholder="Admin token" required>
      <button type="submit">Purge Cache</button>
    </form>
  </section>

  <section>
    <h2>Warm Cache From Sitemap</h2>
    <p class="hint">Provide a sitemap or sitemap index hosted on the B site. URLs outside the B host are skipped.</p>
    <form method="post">
      <input type="hidden" name="form" value="sitemap">
      <label for="sitemap_url">Sitemap URL</label>
      <input type="text" id="sitemap_url" name="sitemap_url" placeholder="https://b.site/sitemap.xml" required>
      <label for="max_urls">Max URLs (optional)</label>
      <input type="number" id="max_urls" name="max_urls" min="0" placeholder="Defaults to ` + fmtInt(defaultSitemapURLLimit) + `">
      <label for="a_base_url">Override A-site base (optional)</label>
      <input type="text" id="a_base_url" name="a_base_url" placeholder="http://localhost:8080">
      <label for="token">Admin token</label>
      <input type="password" id="token" name="token" placeholder="Admin token" required>
      <small>Job runs in the background. Use the status endpoint with this token to check progress.</small>
      <button type="submit">Warm Cache</button>
    </form>
  </section>
</body>
</html>`
}

func renderPurgeResultHTML(q string, partial bool, res purgeResult) string {
	return `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Purge Result</title></head>
<body>
  <p>Purge complete. Deleted: ` + fmtInt(res.Deleted) + ` entries.</p>
  <a href="">Back</a>
</body></html>`
}

func renderSitemapJobQueuedHTML(job *sitemapWarmJob) string {
	statusURL := "/admin/sitemap-cache/status?job=" + htmlEscape(job.ID)
	return `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Sitemap Warm Started</title></head>
<body>
  <h1>Sitemap Cache Warm Queued</h1>
  <p>The sitemap <strong>` + htmlEscape(job.SitemapURL) + `</strong> was accepted for caching.</p>
  <p>Job ID: <code>` + htmlEscape(job.ID) + `</code></p>
  <p>Check progress via <code>` + statusURL + `</code> using the admin token.</p>
  <a href="">Back</a>
</body></html>`
}

func htmlEscape(s string) string { return html.EscapeString(s) }

func fmtInt(n int) string { return strconv.FormatInt(int64(n), 10) }
