package main

import (
    "net/http"
    "path"
    "strings"
)

func isBot(r *http.Request) bool {
    // Allow forcing detection for testing
    if r.Header.Get("X-Bot") == "true" {
        return true
    }
    ua := strings.ToLower(r.UserAgent())
    if ua == "" {
        return false
    }
    bots := []string{
        "googlebot", "bingbot", "slurp", "duckduckbot", "baiduspider",
        "yandexbot", "sogou", "exabot", "facebot", "facebookexternalhit",
        "ia_archiver", "applebot", "semrushbot", "mj12bot", "ahrefsbot",
        "petalbot", "seznambot", "dotbot",
    }
    for _, b := range bots {
        if strings.Contains(ua, b) {
            return true
        }
    }
    return false
}

func patternsMatch(patterns []string, reqPath string) bool {
    // normalize
    if !strings.HasPrefix(reqPath, "/") {
        reqPath = "/" + reqPath
    }
    for _, p := range patterns {
        p = strings.TrimSpace(p)
        if p == "" {
            continue
        }
        // Replace ** with * to keep implementation simple
        p = strings.ReplaceAll(p, "**", "*")
        ok, err := path.Match(p, reqPath)
        if err == nil && ok {
            return true
        }
        // Allow prefix-only pattern like "/blog/" to match
        if strings.HasSuffix(p, "/") && strings.HasPrefix(reqPath, p) {
            return true
        }
    }
    return false
}

