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
    // Known crawler identifiers (lowercased substrings). Keep generic "bot" last.
    // Hybrid detection:
    // 1) Generic keywords catch most crawlers quickly
    if strings.Contains(ua, "bot") || strings.Contains(ua, "crawl") || strings.Contains(ua, "spider") {
        return true
    }
    // 2) Comprehensive curated substrings for known crawlers and preview fetchers
    // Note: Keep items lowercased; we already lowercased UA above.
    known := []string{
        // Google family
        "googlebot", "adsbot-google", "mediapartners-google", "apis-google",
        "feedfetcher-google", "google-inspectiontool", "googleother",
        "duplexweb-google", "googleweblight", "google-proxy", "google favicon",
        "google-read-aloud", "google-extended",
        // Microsoft/Bing
        "bingbot", "msnbot", "bingpreview", "adidxbot", "msnbot-media",
        "bingurlpreview",
        // Yahoo
        "slurp",
        // DuckDuckGo
        "duckduckbot", "duckduckgo-favicons-bot",
        // Baidu
        "baiduspider",
        // Yandex
        "yandexbot", "yandeximages", "yandexmobilebot", "yandexnews",
        "yandexvideo",
        // Sogou / Exalead / Seznam / Qwant
        "sogou", "exabot", "seznambot", "qwantify",
        // Naver
        "naverbot", "naver-yeti", "yeti",
        // Apple / Huawei
        "applebot", "applenewsbot", "petalbot", "aspiegelbot",
        // Social previews
        "facebot", "facebookbot", "facebookexternalhit", "facebookcatalog",
        "meta-externalagent", "twitterbot", "linkedinbot", "pinterestbot",
        "discordbot", "slackbot", "slack-imgproxy", "telegrambot",
        "skypeuripreview", "whatsapp", "vkshare", "odklbot", "redditbot",
        // SEO crawlers and link explorers
        "ahrefsbot", "mj12bot", "semrushbot", "dotbot", "blexbot",
        "seokicks-robot", "spbot", "rogerbot", "linkdexbot", "megaindex",
        "serpstatbot", "siteexplorer", "barkrowler", "seobilitybot",
        "sistrix", "mauibot", "ezooms", "linkpadbot", "dataforseobot",
        "zoominfobot",
        // Other engines / archives
        "ia_archiver", "mail.ru_bot", "mail.ru bot", "coccocbot", "bytespider",
        "toutiaospider", "ccbot", "heritrix", "nutch", "diffbot", "twingly",
        "sosospider", "youdaobot",
        // Performance tools and auditors
        "lighthouse", "pagespeed", "ptst", "gtmetrix", "speedcurve", "pingdom",
        "siteimprove", "w3c_validator", "validator",
        // Headless browsers commonly used for crawling
        "headlesschrome", "phantomjs", "puppeteer", "rendertron", "prerender",
        // AI crawlers
        "gptbot", "oai-searchbot", "perplexitybot", "claudebot", "claude-web",
        "amazonbot",
    }
    for _, k := range known {
        if strings.Contains(ua, k) {
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

// isSitemapPath returns true if the requested path looks like a sitemap.
func isSitemapPath(p string) bool {
    lp := strings.ToLower(p)
    return strings.Contains(lp, "sitemap")
}
