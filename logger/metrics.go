package logger

import (
    "bufio"
    "os"
    "runtime"
    "strconv"
    "strings"
    "syscall"
    "time"
)

// StartMetricsLogger periodically logs system and process metrics.
// diskPath controls where to sample disk usage (e.g., cache dir); if empty, "/".
func StartMetricsLogger(interval time.Duration, diskPath string) chan struct{} {
    if interval <= 0 {
        ch := make(chan struct{})
        close(ch)
        return ch
    }
    stop := make(chan struct{})
    go func() {
        t := time.NewTicker(interval)
        defer t.Stop()
        for {
            select {
            case <-t.C:
                logMetrics(diskPath)
            case <-stop:
                return
            }
        }
    }()
    return stop
}

func logMetrics(diskPath string) {
    if diskPath == "" { diskPath = "/" }

    // Runtime metrics
    var ms runtime.MemStats
    runtime.ReadMemStats(&ms)

    // Disk stats (best-effort)
    dfreeMB, dtotalMB := diskUsageMB(diskPath)

    // Load averages (Linux)
    l1, l5, l15 := loadAverages()

    // Memory (Linux)
    memTotalMB, memFreeMB := memInfoMB()

    fields := map[string]interface{}{
        "goroutines": runtime.NumGoroutine(),
        "num_cpu": runtime.NumCPU(),
        "alloc_mb": bytesToMB(ms.Alloc),
        "heap_inuse_mb": bytesToMB(ms.HeapInuse),
        "stack_inuse_mb": bytesToMB(ms.StackInuse),
        "gc_pause_ns": ms.PauseTotalNs,
        "disk_free_mb": dfreeMB,
        "disk_total_mb": dtotalMB,
        "load1": l1, "load5": l5, "load15": l15,
        "mem_total_mb": memTotalMB,
        "mem_free_mb": memFreeMB,
    }
    Infow("system_metrics", fields)
}

func bytesToMB(b uint64) int64 { return int64(b / (1024 * 1024)) }

func diskUsageMB(path string) (freeMB, totalMB int64) {
    var st syscall.Statfs_t
    if err := syscall.Statfs(path, &st); err != nil { return 0, 0 }
    total := st.Blocks * uint64(st.Bsize)
    free := st.Bavail * uint64(st.Bsize)
    return int64(total / 1024 / 1024), int64(free / 1024 / 1024)
}

func loadAverages() (l1, l5, l15 float64) {
    f, err := os.Open("/proc/loadavg")
    if err != nil { return }
    defer f.Close()
    s := bufio.NewScanner(f)
    if s.Scan() {
        parts := strings.Fields(s.Text())
        if len(parts) >= 3 {
            l1, _ = strconv.ParseFloat(parts[0], 64)
            l5, _ = strconv.ParseFloat(parts[1], 64)
            l15, _ = strconv.ParseFloat(parts[2], 64)
        }
    }
    return
}

func memInfoMB() (totalMB, freeMB int64) {
    f, err := os.Open("/proc/meminfo")
    if err != nil { return }
    defer f.Close()
    s := bufio.NewScanner(f)
    var total, free int64
    for s.Scan() {
        line := s.Text()
        if strings.HasPrefix(line, "MemTotal:") {
            total = parseKBLine(line)
        } else if strings.HasPrefix(line, "MemAvailable:") {
            free = parseKBLine(line)
        }
    }
    return total/1024, free/1024
}

func parseKBLine(line string) int64 {
    f := strings.Fields(line)
    if len(f) < 2 { return 0 }
    v, _ := strconv.ParseInt(f[1], 10, 64)
    return v
}

