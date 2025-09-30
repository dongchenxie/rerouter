package main

import (
    "net/http"
    "time"
)

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
    w.Header().Set("X-Cache", "HIT")
    setCacheMetaHeaders(w, ce)
    for k, v := range ce.Header {
        w.Header().Set(k, v)
    }
    w.WriteHeader(ce.Status)
    if len(ce.Body) > 0 {
        _, _ = w.Write(ce.Body)
    }
}

// setCacheMetaHeaders adds human-readable cache timestamps to response headers.
// - X-Cache-Generated-At: RFC3339 UTC time the cache was created
// - X-Cache-Expires-At:   RFC3339 UTC time the cache will expire
func setCacheMetaHeaders(w http.ResponseWriter, ce *cacheEntry) {
    if ce == nil { return }
    gen := time.Unix(ce.CreatedAt, 0).UTC()
    exp := time.Unix(ce.ExpiresAt, 0).UTC()
    w.Header().Set("X-Cache-Generated-At", gen.Format(time.RFC3339))
    w.Header().Set("X-Cache-Expires-At", exp.Format(time.RFC3339))
}
