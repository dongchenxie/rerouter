package main

import "strings"

// cacheTTLForPath returns the TTL seconds for a given request path based on config rules.
// Rules are evaluated in order; first match wins. Falls back to global CacheTTLSeconds.
func cacheTTLForPath(cfg *Config, reqPath string) int {
    if cfg == nil {
        return 0
    }
    if len(cfg.CacheTTLRules) > 0 {
        for _, r := range cfg.CacheTTLRules {
            pat := r.Pattern
            if strings.HasPrefix(pat, "*.") || strings.HasPrefix(pat, ".") {
                // Extension/suffix pattern (case-insensitive)
                suf := strings.TrimPrefix(pat, "*.")
                suf = strings.TrimPrefix(suf, ".")
                if strings.HasSuffix(strings.ToLower(reqPath), strings.ToLower("."+suf)) {
                    if r.TTLSeconds > 0 { return r.TTLSeconds }
                }
                continue
            }
            if patternsMatch([]string{pat}, reqPath) {
                if r.TTLSeconds > 0 { return r.TTLSeconds }
            }
        }
    }
    if cfg.CacheTTLSeconds > 0 {
        return cfg.CacheTTLSeconds
    }
    return 0
}
