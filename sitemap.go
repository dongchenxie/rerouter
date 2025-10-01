package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultSitemapURLLimit = 5000

var errSitemapURLLimitReached = errors.New("sitemap url limit reached")

type sitemapURLEntry struct {
	Loc string `xml:"loc"`
}

type sitemapURLSet struct {
	URLs []sitemapURLEntry `xml:"url"`
}

type sitemapIndexSet struct {
	Sitemaps []sitemapURLEntry `xml:"sitemap"`
}

func collectSitemapURLs(ctx context.Context, client *http.Client, sitemap string, max int) ([]string, error) {
	if max <= 0 {
		max = defaultSitemapURLLimit
	}
	visited := make(map[string]struct{})
	seenURLs := make(map[string]struct{})
	urls := make([]string, 0, 128)

	var walk func(string) error
	walk = func(current string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, ok := visited[current]; ok {
			return nil
		}
		visited[current] = struct{}{}

		body, err := fetchSitemapBody(ctx, client, current)
		if err != nil {
			return err
		}

		trimmed := bytes.TrimSpace(body)
		if len(trimmed) == 0 {
			return fmt.Errorf("empty sitemap: %s", current)
		}

		var us sitemapURLSet
		if err := xml.Unmarshal(trimmed, &us); err == nil && len(us.URLs) > 0 {
			for _, entry := range us.URLs {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				loc := strings.TrimSpace(entry.Loc)
				if loc == "" {
					continue
				}
				resolved, err := resolveSitemapLocation(current, loc)
				if err != nil {
					return err
				}
				if _, dup := seenURLs[resolved]; dup {
					continue
				}
				seenURLs[resolved] = struct{}{}
				urls = append(urls, resolved)
				if len(urls) >= max {
					return errSitemapURLLimitReached
				}
			}
			return nil
		}

		var si sitemapIndexSet
		if err := xml.Unmarshal(trimmed, &si); err == nil && len(si.Sitemaps) > 0 {
			for _, sm := range si.Sitemaps {
				loc := strings.TrimSpace(sm.Loc)
				if loc == "" {
					continue
				}
				resolved, err := resolveSitemapLocation(current, loc)
				if err != nil {
					return err
				}
				if err := walk(resolved); err != nil {
					if errors.Is(err, errSitemapURLLimitReached) {
						return err
					}
					if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
						return err
					}
					return err
				}
				if len(urls) >= max {
					return errSitemapURLLimitReached
				}
			}
			return nil
		}

		return fmt.Errorf("unrecognized sitemap format: %s", current)
	}

	err := walk(sitemap)
	if errors.Is(err, errSitemapURLLimitReached) {
		err = nil
	}
	return urls, err
}

func fetchSitemapBody(ctx context.Context, client *http.Client, sitemapURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sitemapURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "rerouter-sitemap-fetcher/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch sitemap %s: status %d", sitemapURL, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	body := data
	if isGzipEncoded(resp.Header, sitemapURL) {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("gzip decode %s: %w", sitemapURL, err)
		}
		defer zr.Close()
		decoded, err := io.ReadAll(zr)
		if err != nil {
			return nil, err
		}
		body = decoded
	}

	return body, nil
}

func isGzipEncoded(h http.Header, sitemapURL string) bool {
	if enc := h.Get("Content-Encoding"); enc != "" {
		return strings.Contains(strings.ToLower(enc), "gzip")
	}
	return strings.HasSuffix(strings.ToLower(sitemapURL), ".gz")
}

func resolveSitemapLocation(baseURL, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("empty sitemap loc")
	}
	b, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", err
	}
	resolved := b.ResolveReference(r)
	resolved.Fragment = ""
	return resolved.String(), nil
}

func newSitemapHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &http.Client{Timeout: timeout}
}
