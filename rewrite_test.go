package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestRewriteBToAReplacesBareHost(t *testing.T) {
	aBase, err := url.Parse("http://localhost:8080")
	if err != nil {
		t.Fatalf("parse aBase: %v", err)
	}
	bBase, err := url.Parse("https://pk.ziweidoueshu.cc")
	if err != nil {
		t.Fatalf("parse bBase: %v", err)
	}

	body := []byte(`const origin = "pk.ziweidoueshu.cc";`)
	got, rewrote := rewriteBToA(body, aBase, bBase)
	if !rewrote {
		t.Fatalf("expected rewrite to trigger for bare host")
	}
	s := string(got)
	if strings.Contains(s, "pk.ziweidoueshu.cc") {
		t.Fatalf("expected pk.ziweidoueshu.cc to be replaced, got: %s", s)
	}
	if !strings.Contains(s, "localhost:8080") {
		t.Fatalf("expected localhost:8080 in rewrite result, got: %s", s)
	}
	if strings.Contains(s, "http://localhost:8080") || strings.Contains(s, "https://localhost:8080") {
		t.Fatalf("expected bare host replacement without scheme, got: %s", s)
	}
}

func TestRewriteBToASkipsSubdomainMatches(t *testing.T) {
	aBase, _ := url.Parse("http://localhost:8080")
	bBase, _ := url.Parse("https://pk.ziweidoueshu.cc")

	body := []byte(`https://sub.pk.ziweidoueshu.cc/path`)
	got, rewrote := rewriteBToA(body, aBase, bBase)
	if rewrote {
		t.Fatalf("expected subdomain reference to remain untouched, got: %s", string(got))
	}
	if string(got) != string(body) {
		t.Fatalf("expected body unchanged for subdomain match")
	}
}

func TestRewriteBToAReplacesMultipleBareHosts(t *testing.T) {
	aBase, _ := url.Parse("http://localhost:8080")
	bBase, _ := url.Parse("https://pk.ziweidoueshu.cc")

	body := []byte(`"pk.ziweidoueshu.cc", pk.ziweidoueshu.cc; pk.ziweidoueshu.cc`)
	got, rewrote := rewriteBToA(body, aBase, bBase)
	if !rewrote {
		t.Fatalf("expected multiple bare host occurrences to be rewritten")
	}
	if strings.Count(string(got), "localhost:8080") != 3 {
		t.Fatalf("expected all bare hosts rewritten, got: %s", string(got))
	}
}

func TestRewriteBToAReplacesSchemeVariants(t *testing.T) {
	aBase, _ := url.Parse("http://localhost:8080")
	bBase, _ := url.Parse("https://pk.ziweidoueshu.cc")

	body := []byte("https://pk.ziweidoueshu.cc/path http://pk.ziweidoueshu.cc/path //pk.ziweidoueshu.cc/path")
	got, rewrote := rewriteBToA(body, aBase, bBase)
	if !rewrote {
		t.Fatalf("expected scheme variants to be rewritten")
	}
	s := string(got)
	if strings.Contains(s, "https://pk.ziweidoueshu.cc") || strings.Contains(s, "http://pk.ziweidoueshu.cc") || strings.Contains(s, "//pk.ziweidoueshu.cc") {
		t.Fatalf("expected upstream variants to be removed, got: %s", s)
	}
	if strings.Count(s, "localhost:8080") != 3 {
		t.Fatalf("expected host replacement in all rewritten variants, got: %s", s)
	}
	if strings.Count(s, "http://localhost:8080") != 2 {
		t.Fatalf("expected http scheme for explicit protocols, got: %s", s)
	}
	if !strings.Contains(s, "//localhost:8080/path") {
		t.Fatalf("expected protocol-relative reference to use new host, got: %s", s)
	}
}

func TestRewriteBToARespectsNonHostBoundaries(t *testing.T) {
	aBase, _ := url.Parse("http://localhost:8080")
	bBase, _ := url.Parse("https://pk.ziweidoueshu.cc")

	// Should ignore when characters that could be part of a hostname extend match
	body := []byte(`apk.ziweidoueshu.cc examplepk.ziweidoueshu.cc`)
	got, rewrote := rewriteBToA(body, aBase, bBase)
	if rewrote {
		t.Fatalf("expected no rewrite when host is part of a larger token, got: %s", string(got))
	}
	if string(got) != string(body) {
		t.Fatalf("expected body unchanged when boundaries are not respected")
	}
}

func TestRewriteBToARewritesQueryParams(t *testing.T) {
	aBase, _ := url.Parse("http://localhost:8080")
	bBase, _ := url.Parse("https://pk.ziweidoueshu.cc")

	body := []byte(`{"origin":"pk.ziweidoueshu.cc","next":"https://pk.ziweidoueshu.cc/api?target=pk.ziweidoueshu.cc"}`)
	got, rewrote := rewriteBToA(body, aBase, bBase)
	if !rewrote {
		t.Fatalf("expected rewrite to trigger for JSON payload")
	}
	s := string(got)
	if strings.Contains(s, "pk.ziweidoueshu.cc") {
		t.Fatalf("expected all pk.ziweidoueshu.cc references removed, got: %s", s)
	}
	if strings.Count(s, "localhost:8080") != 3 {
		t.Fatalf("expected three occurrences of localhost:8080, got: %s", s)
	}
}
