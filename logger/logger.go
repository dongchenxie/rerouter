package logger

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "sync"
    "time"
)

type Level int

const (
    Debug Level = iota
    Info
    Warn
    Error
)

func ParseLevel(s string) Level {
    switch strings.ToLower(s) {
    case "debug":
        return Debug
    case "warn", "warning":
        return Warn
    case "error":
        return Error
    default:
        return Info
    }
}

type Config struct {
    Level       Level
    File        string // path to log file; if empty, file logging disabled
    MaxSizeMB   int    // rotate when size exceeds this (0 disables)
    MaxBackups  int    // keep at most N rotated files (0 disables cleanup)
    MaxAgeDays  int    // remove rotated files older than this (0 disables)
}

type entry struct {
    Time    string                 `json:"ts"`
    Level   string                 `json:"level"`
    Message string                 `json:"msg"`
    Fields  map[string]interface{} `json:"fields,omitempty"`
}

type Logger struct {
    mu     sync.Mutex
    level  Level
    file   *os.File
    cfg    Config
}

var global *Logger

func Init(cfg Config) error {
    l := &Logger{level: cfg.Level, cfg: cfg}
    if cfg.File != "" {
        if err := os.MkdirAll(filepath.Dir(cfg.File), 0o755); err != nil {
            return err
        }
        f, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
        if err != nil {
            return err
        }
        l.file = f
    }
    global = l
    return nil
}

func Close() {
    if global != nil && global.file != nil {
        _ = global.file.Close()
    }
}

func L() *Logger { return global }

func (l *Logger) log(lvl Level, msg string, fields map[string]interface{}) {
    if l == nil { return }
    if lvl < l.level { return }
    e := entry{
        Time:    time.Now().UTC().Format(time.RFC3339Nano),
        Level:   levelString(lvl),
        Message: msg,
        Fields:  fields,
    }
    b, _ := json.Marshal(e)
    l.mu.Lock()
    defer l.mu.Unlock()
    // Console always
    fmt.Fprintln(os.Stdout, string(b))
    // File with rotation
    if l.file != nil {
        l.rotateIfNeededLocked()
        if l.file != nil { // rotate may fail
            fmt.Fprintln(l.file, string(b))
        }
    }
}

func (l *Logger) rotateIfNeededLocked() {
    if l.file == nil || l.cfg.MaxSizeMB <= 0 { return }
    info, err := l.file.Stat()
    if err != nil { return }
    max := int64(l.cfg.MaxSizeMB) * 1024 * 1024
    if info.Size() < max { return }
    // Rotate: close current file, rename with timestamp, open new
    path := l.file.Name()
    _ = l.file.Close()
    ts := time.Now().UTC().Format("20060102-150405")
    rotated := fmt.Sprintf("%s.%s", path, ts)
    _ = os.Rename(path, rotated)
    nf, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
    if err == nil {
        l.file = nf
    } else {
        l.file = nil
    }
    // Cleanup old files if configured
    l.cleanupOld(path)
}

func (l *Logger) cleanupOld(activePath string) {
    if l.cfg.MaxBackups <= 0 && l.cfg.MaxAgeDays <= 0 { return }
    dir := filepath.Dir(activePath)
    base := filepath.Base(activePath)
    // match files starting with base + .
    entries, err := os.ReadDir(dir)
    if err != nil { return }
    type rf struct { name string; mod time.Time }
    files := make([]rf, 0)
    for _, e := range entries {
        n := e.Name()
        if !strings.HasPrefix(n, base+".") { continue }
        info, err := e.Info()
        if err != nil { continue }
        files = append(files, rf{name: filepath.Join(dir, n), mod: info.ModTime()})
    }
    // Sort newest first
    sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
    // By backups
    keep := len(files)
    if l.cfg.MaxBackups > 0 && keep > l.cfg.MaxBackups {
        for _, f := range files[l.cfg.MaxBackups:] {
            _ = os.Remove(f.name)
        }
        keep = l.cfg.MaxBackups
    }
    // By age
    if l.cfg.MaxAgeDays > 0 {
        cutoff := time.Now().AddDate(0, 0, -l.cfg.MaxAgeDays)
        for _, f := range files[:keep] {
            if f.mod.Before(cutoff) { _ = os.Remove(f.name) }
        }
    }
}

func levelString(lvl Level) string {
    switch lvl {
    case Debug:
        return "debug"
    case Info:
        return "info"
    case Warn:
        return "warn"
    case Error:
        return "error"
    default:
        return "info"
    }
}

func Debugw(msg string, fields map[string]interface{}) { L().log(Debug, msg, fields) }
func Infow(msg string, fields map[string]interface{})  { L().log(Info, msg, fields) }
func Warnw(msg string, fields map[string]interface{})  { L().log(Warn, msg, fields) }
func Errorw(msg string, fields map[string]interface{}) { L().log(Error, msg, fields) }
