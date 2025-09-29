package main

import (
    "net/http"
    "net/url"
    "strings"
)

// deriveABaseURL returns the base URL for site A based on config or request.
func deriveABaseURL(cfg *Config, r *http.Request) *url.URL {
    if cfg.ABaseURL != "" {
        if u, err := url.Parse(cfg.ABaseURL); err == nil {
            return u
        }
    }
    // Fallback: build from request
    scheme := r.Header.Get("X-Forwarded-Proto")
    if scheme == "" {
        if r.TLS != nil {
            scheme = "https"
        } else {
            scheme = "http"
        }
    }
    u := &url.URL{Scheme: scheme, Host: r.Host}
    return u
}

// rewriteBodyForBots replaces absolute URLs pointing to B-site with A-site in HTML-like content.
func rewriteBodyForBots(body []byte, contentType string, aBase, bBase *url.URL) (out []byte, rewrote bool) {
    ct := strings.ToLower(contentType)
    if !(strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml")) {
        return body, false
    }
    aHost := aBase.Host
    bHost := bBase.Host
    aAbs := aBase.Scheme + "://" + aHost
    bAbs := bBase.Scheme + "://" + bHost

    s := string(body)
    // Replace absolute URLs first (with scheme), then protocol-relative
    if strings.Contains(s, bAbs) || strings.Contains(s, "//"+bHost) {
        s = strings.ReplaceAll(s, bAbs, aAbs)
        s = strings.ReplaceAll(s, "//"+bHost, "//"+aHost)
        // Also handle mixed-scheme references explicitly
        s = strings.ReplaceAll(s, "http://"+bHost, aBase.Scheme+"://"+aHost)
        s = strings.ReplaceAll(s, "https://"+bHost, aBase.Scheme+"://"+aHost)
        return []byte(s), true
    }
    return body, false
}

