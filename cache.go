package main

import (
    "crypto/sha1"
    "encoding/hex"
    "encoding/json"
    "errors"
    "net/url"
    "os"
    "path/filepath"
    "strings"
    "time"
)

type cacheEntry struct {
    URL       string            `json:"url"`
    CreatedAt int64             `json:"created_at"`
    ExpiresAt int64             `json:"expires_at"`
    Status    int               `json:"status"`
    Header    map[string]string `json:"header"`
    Body      []byte            `json:"body"`
}

// cacheFilePathForURL returns the absolute path for the cache JSON file for a given absolute URL.
// Layout: <cacheDir>/<host>/<path_segments>/index[.q<hash>].json
// - Root path -> .../<host>/index.json
// - Query string -> append short hash suffix to avoid collisions: index.<hash8>.json
func cacheFilePathForURL(cacheDir, rawURL string) (string, error) {
    u, err := url.Parse(rawURL)
    if err != nil {
        return "", err
    }
    host := u.Host // includes port if present; acceptable as directory name
    // Normalize path
    p := strings.Trim(u.EscapedPath(), "/")
    // Build directory: host + path segments
    dir := filepath.Join(cacheDir, host)
    if p != "" {
        // Split on '/'; filepath.Join will handle platform separators
        for _, seg := range strings.Split(p, "/") {
            if seg == "" { continue }
            dir = filepath.Join(dir, seg)
        }
    }
    // File name
    name := "index.json"
    if u.RawQuery != "" {
        // hash includes full request URI to distinguish queries
        h := sha1.Sum([]byte(u.RequestURI()))
        name = "index." + hex.EncodeToString(h[:4]) + ".json" // 8 hex chars
    }
    return filepath.Join(dir, name), nil
}

func readCacheByURL(cacheDir, rawURL string) (*cacheEntry, error) {
    p, err := cacheFilePathForURL(cacheDir, rawURL)
    if err != nil {
        return nil, err
    }
    b, err := os.ReadFile(p)
    if err != nil {
        return nil, err
    }
    var ce cacheEntry
    if err := json.Unmarshal(b, &ce); err != nil {
        return nil, err
    }
    if time.Now().Unix() >= ce.ExpiresAt {
        return nil, errors.New("cache expired")
    }
    return &ce, nil
}

func writeCacheByURL(cacheDir, rawURL string, ce *cacheEntry) error {
    p, err := cacheFilePathForURL(cacheDir, rawURL)
    if err != nil {
        return err
    }
    if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
        return err
    }
    tmp := p + ".tmp"
    b, err := json.Marshal(ce)
    if err != nil {
        return err
    }
    if err := os.WriteFile(tmp, b, 0o644); err != nil {
        return err
    }
    return os.Rename(tmp, p)
}

// walkCacheJSONFiles lists all .json files recursively under cacheDir.
func walkCacheJSONFiles(cacheDir string) ([]string, error) {
    paths := []string{}
    _ = filepath.WalkDir(cacheDir, func(p string, d os.DirEntry, err error) error {
        if err != nil { return nil }
        if d.IsDir() { return nil }
        if strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
            paths = append(paths, p)
        }
        return nil
    })
    return paths, nil
}

