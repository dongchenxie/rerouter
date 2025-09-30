package main

import (
    "context"
    "fmt"
    "net/http"
    "os"
    "time"
    "rerouter/logger"
)

type ctxKey string

const requestIDKey ctxKey = "req_id"

func withRequestID(ctx context.Context, id string) context.Context {
    return context.WithValue(ctx, requestIDKey, id)
}

func getRequestID(ctx context.Context) string {
    v := ctx.Value(requestIDKey)
    if s, ok := v.(string); ok { return s }
    return ""
}

func newRequestID() string {
    // 16 random bytes hex-encoded
    b := make([]byte, 16)
    // crypto/rand preferred but avoid extra dependency; read from /dev/urandom via Read
    // Note: math/rand is not cryptographically secure but fine for correlation IDs
    if _, err := randRead(b); err != nil {
        t := time.Now().UnixNano()
        return fmt.Sprintf("%x", t)
    }
    return fmt.Sprintf("%x", b)
}

// indirection for testing
var randRead = func(b []byte) (int, error) { return randReader.Read(b) }

var (
    randReader = defaultRandReader{}
)

type defaultRandReader struct{}

func (defaultRandReader) Read(b []byte) (int, error) {
    return readFromDevURandom(b)
}

// minimal /dev/urandom reader
func readFromDevURandom(b []byte) (int, error) {
    f, err := os.Open("/dev/urandom")
    if err != nil { return 0, err }
    defer f.Close()
    return f.Read(b)
}

// loggingMiddleware wraps an http.Handler to add request ID and access log
func loggingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        rid := newRequestID()
        r = r.WithContext(withRequestID(r.Context(), rid))
        w.Header().Set("X-Request-ID", rid)
        sw := &statusWriter{ResponseWriter: w, status: 200}
        start := time.Now()
        next.ServeHTTP(sw, r)
        dur := time.Since(start)
        logger.Infow("access", map[string]interface{}{
            "req_id": rid,
            "method": r.Method,
            "path": r.URL.RequestURI(),
            "remote": r.RemoteAddr,
            "status": sw.status,
            "bytes": sw.written,
            "duration_ms": dur.Milliseconds(),
            "ua": r.UserAgent(),
        })
    })
}

type statusWriter struct {
    http.ResponseWriter
    status  int
    written int
}

func (w *statusWriter) WriteHeader(code int) {
    w.status = code
    w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
    n, err := w.ResponseWriter.Write(b)
    w.written += n
    return n, err
}
