package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAdminSitemapCacheEndpoint(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(fmt.Sprintf("<html><body>host=%s</body></html>", r.Host)))
	}))
	defer up.Close()

	cfg := newTestCfg(t, up.URL)
	cfg.AdminToken = "secret"
	cfg.ABaseURL = "http://localhost:8080"

	h := buildHandler(cfg)
	srv := httptest.NewServer(h)
	defer srv.Close()

	sitemapMux := http.NewServeMux()
	var sitemapBase string
	sitemapMux.HandleFunc("/index.xml", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>` + sitemapBase + `/posts.xml</loc></sitemap>
  <sitemap><loc>` + sitemapBase + `/extra.xml</loc></sitemap>
</sitemapindex>`))
	})
	sitemapMux.HandleFunc("/posts.xml", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>` + up.URL + `/page1</loc></url>
  <url><loc>` + up.URL + `/page2</loc></url>
</urlset>`))
	})
	sitemapMux.HandleFunc("/extra.xml", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>` + up.URL + `/page3</loc></url>
</urlset>`))
	})

	sitemapSrv := httptest.NewServer(sitemapMux)
	defer sitemapSrv.Close()
	sitemapBase = sitemapSrv.URL

	reqBody := []byte(fmt.Sprintf(`{"sitemap_url":"%s/index.xml"}`, sitemapSrv.URL))
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/sitemap-cache", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", cfg.AdminToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	var payload struct {
		JobID string `json:"job_id"`
		State string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.JobID == "" {
		t.Fatalf("expected job_id in response")
	}
	status := waitForSitemapJob(t, srv.URL, cfg.AdminToken, payload.JobID)
	if status.State != string(jobStateCompleted) {
		t.Fatalf("expected job completed, got state %s", status.State)
	}
	if status.CachedURLs != 3 {
		t.Fatalf("expected cached_urls 3, got %d", status.CachedURLs)
	}
	if status.SkippedURLs != 0 {
		t.Fatalf("expected skipped_urls 0, got %d", status.SkippedURLs)
	}

	for _, path := range []string{"/page1", "/page2", "/page3"} {
		target := strings.TrimRight(cfg.BBaseURL, "/") + path
		ce, err := readCacheByURL(cfg.CacheDir, target)
		if err != nil {
			t.Fatalf("expected cache for %s: %v", target, err)
		}
		if !bytes.Contains(ce.Body, []byte("localhost:8080")) {
			t.Fatalf("expected body to be rewritten to localhost:8080 for %s, got %s", target, string(ce.Body))
		}
	}
}

func TestAdminSitemapCacheUIForm(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(fmt.Sprintf("<html><body>host=%s</body></html>", r.Host)))
	}))
	defer up.Close()

	cfg := newTestCfg(t, up.URL)
	cfg.AdminToken = "secret"
	cfg.ABaseURL = "http://localhost:8080"
	cfg.AdminUIPath = "/admin/ui"

	h := buildHandler(cfg)
	srv := httptest.NewServer(h)
	defer srv.Close()

	sitemapMux := http.NewServeMux()
	var sitemapBase string
	sitemapMux.HandleFunc("/root.xml", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>` + sitemapBase + `/one.xml</loc></sitemap>
</sitemapindex>`))
	})
	sitemapMux.HandleFunc("/one.xml", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>` + up.URL + `/pageA</loc></url>
</urlset>`))
	})

	sitemapSrv := httptest.NewServer(sitemapMux)
	defer sitemapSrv.Close()
	sitemapBase = sitemapSrv.URL

	form := url.Values{}
	form.Set("form", "sitemap")
	form.Set("sitemap_url", sitemapSrv.URL+"/root.xml")
	form.Set("token", cfg.AdminToken)

	resp, err := http.PostForm(srv.URL+cfg.AdminUIPath, form)
	if err != nil {
		t.Fatalf("post form: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("Job ID")) {
		t.Fatalf("expected job confirmation in HTML, got: %s", string(body))
	}
	start := bytes.Index(body, []byte("<code>"))
	if start == -1 {
		t.Fatalf("expected job id code block in response: %s", string(body))
	}
	start += len("<code>")
	end := bytes.Index(body[start:], []byte("</code>"))
	if end == -1 {
		t.Fatalf("expected closing code tag in response: %s", string(body))
	}
	jobID := string(body[start : start+end])

	status := waitForSitemapJob(t, srv.URL, cfg.AdminToken, jobID)
	if status.State != string(jobStateCompleted) {
		t.Fatalf("expected job completed, got state %s", status.State)
	}
	if status.CachedURLs != 1 {
		t.Fatalf("expected cached_urls 1, got %d", status.CachedURLs)
	}

	target := strings.TrimRight(cfg.BBaseURL, "/") + "/pageA"
	ce, err := readCacheByURL(cfg.CacheDir, target)
	if err != nil {
		t.Fatalf("expected cache entry: %v", err)
	}
	if !bytes.Contains(ce.Body, []byte("localhost:8080")) {
		t.Fatalf("expected rewritten body for UI-triggered cache, got: %s", string(ce.Body))
	}
}

func waitForSitemapJob(t *testing.T, baseURL, token, jobID string) sitemapWarmJobStatus {
	t.Helper()
	var last sitemapWarmJobStatus
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, baseURL+"/admin/sitemap-cache/status?job="+url.QueryEscape(jobID), nil)
		if err != nil {
			t.Fatalf("new status request: %v", err)
		}
		req.Header.Set("X-Admin-Token", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("status request error: %v", err)
		}
		func() {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return
			}
			if err := json.NewDecoder(resp.Body).Decode(&last); err != nil {
				t.Fatalf("decode status: %v", err)
			}
		}()
		if last.State == string(jobStateCompleted) || last.State == string(jobStateErrored) {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s did not complete in time (last state %s)", jobID, last.State)
	return last
}
