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
	// Rewrite HTML, XHTML, and XML content (sitemap/feeds)
	if !(strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml") || strings.Contains(ct, "xml")) {
		return body, false
	}
	return rewriteBToA(body, aBase, bBase)
}

// rewriteBToA performs URL host replacement regardless of content type.
func rewriteBToA(body []byte, aBase, bBase *url.URL) ([]byte, bool) {
	aHost := aBase.Host
	bHost := bBase.Host
	aAbs := aBase.Scheme + "://" + aHost
	bAbs := bBase.Scheme + "://" + bHost

	s := string(body)
	replaced := false

	if strings.Contains(s, bAbs) {
		s = strings.ReplaceAll(s, bAbs, aAbs)
		replaced = true
	}
	if strings.Contains(s, "//"+bHost) {
		s = strings.ReplaceAll(s, "//"+bHost, "//"+aHost)
		replaced = true
	}
	// Also handle mixed-scheme references explicitly
	if strings.Contains(s, "http://"+bHost) {
		s = strings.ReplaceAll(s, "http://"+bHost, aBase.Scheme+"://"+aHost)
		replaced = true
	}
	if strings.Contains(s, "https://"+bHost) {
		s = strings.ReplaceAll(s, "https://"+bHost, aBase.Scheme+"://"+aHost)
		replaced = true
	}
	if ns, ok := replaceHostLiteral(s, bHost, aHost); ok {
		s = ns
		replaced = true
	}

	if replaced {
		return []byte(s), true
	}
	return body, false
}

// replaceHostLiteral swaps bare host occurrences (no scheme) when they are not part of a larger hostname.
func replaceHostLiteral(s, old, repl string) (string, bool) {
	if old == "" || old == repl {
		return s, false
	}
	var b strings.Builder
	// Reserve original length; replacement may differ slightly but builder will grow as needed.
	b.Grow(len(s))
	changed := false

	for i := 0; i < len(s); {
		idx := strings.Index(s[i:], old)
		if idx == -1 {
			b.WriteString(s[i:])
			break
		}
		idx += i
		if !hostBoundaryBefore(s, idx) || !hostBoundaryAfter(s, idx+len(old)) {
			b.WriteString(s[i : idx+1])
			i = idx + 1
			continue
		}
		b.WriteString(s[i:idx])
		b.WriteString(repl)
		i = idx + len(old)
		changed = true
	}

	if !changed {
		return s, false
	}
	return b.String(), true
}

func hostBoundaryBefore(s string, idx int) bool {
	if idx == 0 {
		return true
	}
	return !isHostChar(s[idx-1])
}

func hostBoundaryAfter(s string, idx int) bool {
	if idx >= len(s) {
		return true
	}
	return !isHostChar(s[idx])
}

func isHostChar(ch byte) bool {
	if ch >= 'a' && ch <= 'z' {
		return true
	}
	if ch >= 'A' && ch <= 'Z' {
		return true
	}
	if ch >= '0' && ch <= '9' {
		return true
	}
	return ch == '-' || ch == '.' || ch == ':'
}
