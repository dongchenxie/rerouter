package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCollectSitemapURLsHandlesIndexAndGzip(t *testing.T) {
	var bHost string
	var gzBody bytes.Buffer
	gz := gzip.NewWriter(&gzBody)
	_, _ = gz.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>/relative/page3</loc></url>
  <url><loc>https://another.example.com/outside</loc></url>
</urlset>`))
	gz.Close()

	mux := http.NewServeMux()
	var sitemapBase string
	mux.HandleFunc("/index.xml", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>` + sitemapBase + `/child.xml</loc></sitemap>
  <sitemap><loc>` + sitemapBase + `/child2.xml.gz</loc></sitemap>
</sitemapindex>`))
	})
	mux.HandleFunc("/child.xml", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>` + bHost + `/page1</loc></url>
  <url><loc>` + bHost + `/page2</loc></url>
  <url><loc>` + bHost + `/page1</loc></url>
</urlset>`))
	})
	mux.HandleFunc("/child2.xml.gz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(gzBody.Bytes())
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	sitemapBase = srv.URL
	bHost = srv.URL

	client := newSitemapHTTPClient(0, defaultUpstreamUserAgent)
	urls, err := collectSitemapURLs(context.Background(), client, srv.URL+"/index.xml", 10)
	if err != nil {
		t.Fatalf("collectSitemapURLs error: %v", err)
	}
	if len(urls) != 4 {
		t.Fatalf("expected 4 URLs, got %d (%v)", len(urls), urls)
	}
	want := map[string]bool{
		bHost + "/page1":                      true,
		bHost + "/page2":                      true,
		bHost + "/relative/page3":             true,
		"https://another.example.com/outside": true,
	}
	for _, u := range urls {
		if !want[u] {
			t.Fatalf("unexpected URL %s", u)
		}
	}
}

func TestCollectSitemapURLsRespectsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/a</loc></url>
  <url><loc>https://example.com/b</loc></url>
  <url><loc>https://example.com/c</loc></url>
</urlset>`))
	}))
	defer srv.Close()

	client := newSitemapHTTPClient(0, defaultUpstreamUserAgent)
	urls, err := collectSitemapURLs(context.Background(), client, srv.URL, 2)
	if err != nil {
		t.Fatalf("collectSitemapURLs error: %v", err)
	}
	if len(urls) != 2 {
		t.Fatalf("expected 2 URLs due to limit, got %d", len(urls))
	}
}
