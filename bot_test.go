package main

import (
    "net/http/httptest"
    "testing"
)

func TestIsBot_GoogleVariants(t *testing.T) {
    cases := []string{
        "Googlebot/2.1 (+http://www.google.com/bot.html)",
        "Googlebot-Image/1.0",
        "Googlebot-Smartphone",
        "AdsBot-Google",
        "AdsBot-Google-Mobile",
        "Mediapartners-Google",
        "APIs-Google",
        "FeedFetcher-Google; (+http://www.google.com/feedfetcher.html)",
        "Google-InspectionTool",
        "GoogleOther/1.0",
        "DuplexWeb-Google",
    }
    for _, ua := range cases {
        r := httptest.NewRequest("GET", "/", nil)
        r.Header.Set("User-Agent", ua)
        if !isBot(r) {
            t.Fatalf("expected isBot true for UA: %q", ua)
        }
    }
}

func TestIsBot_NonBots(t *testing.T) {
    cases := []string{
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)",
        "Safari/604.1",
        "Chrome/123.0.0.0",
    }
    for _, ua := range cases {
        r := httptest.NewRequest("GET", "/", nil)
        r.Header.Set("User-Agent", ua)
        if isBot(r) {
            t.Fatalf("expected isBot false for UA: %q", ua)
        }
    }
}

func TestIsBot_NonGenericKnowns(t *testing.T) {
    // These do not contain "bot|crawl|spider" but should be detected via list
    cases := []string{
        "facebookexternalhit/1.1 (+http://www.facebook.com/externalhit_uatext.php)",
        "BingPreview/1.0b",
        "Qwantify/4.0",
        "Google-InspectionTool/1.0",
        "Mediapartners-Google",
        "APIs-Google",
        "FeedFetcher-Google; (+http://www.google.com/feedfetcher.html)",
        "GoogleWebLight",
        "BingURLPreview/2.0",
        "CCBot/2.0 (https://commoncrawl.org/faq/)",
        "HeadlessChrome/123.0.0.0",
        "GPTBot/1.0 (+https://openai.com/gptbot)",
        "PerplexityBot/1.0",
        "ClaudeBot",
        "Amazonbot/0.1",
    }
    for _, ua := range cases {
        r := httptest.NewRequest("GET", "/", nil)
        r.Header.Set("User-Agent", ua)
        if !isBot(r) {
            t.Fatalf("expected isBot true for UA: %q", ua)
        }
    }
}
