package main

import (
    "net/http"
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
    for k, v := range ce.Header {
        w.Header().Set(k, v)
    }
    w.WriteHeader(ce.Status)
    if len(ce.Body) > 0 {
        _, _ = w.Write(ce.Body)
    }
}
