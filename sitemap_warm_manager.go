package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"rerouter/logger"
)

type sitemapWarmJobState string

const (
	jobStateQueued    sitemapWarmJobState = "queued"
	jobStateRunning   sitemapWarmJobState = "running"
	jobStateCompleted sitemapWarmJobState = "completed"
	jobStateErrored   sitemapWarmJobState = "error"
)

const sitemapWarmJobTimeout = 72 * time.Hour
const sitemapWarmMaxAttempts = 3

type sitemapWarmURLStatus struct {
	RawURL       string `json:"raw_url"`
	URL          string `json:"url,omitempty"`
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
	Attempts     int    `json:"attempts,omitempty"`
	Error        string `json:"error,omitempty"`
	ExpectedHost string `json:"expected_host,omitempty"`
	ActualHost   string `json:"actual_host,omitempty"`
}

type sitemapWarmJob struct {
	mu            sync.Mutex
	ID            string
	SitemapURL    string
	MaxURLs       int
	ABaseOverride string
	State         sitemapWarmJobState
	SubmittedAt   time.Time
	StartedAt     time.Time
	CompletedAt   time.Time
	Total         int
	Processed     int
	Cached        int
	Skipped       int
	Interrupted   bool
	Error         string
	Duration      time.Duration
	URLStatuses   []sitemapWarmURLStatus
}

func (job *sitemapWarmJob) snapshot() sitemapWarmJobStatus {
	job.mu.Lock()
	defer job.mu.Unlock()
	return sitemapWarmJobStatus{
		JobID:         job.ID,
		SitemapURL:    job.SitemapURL,
		State:         string(job.State),
		TotalURLs:     job.Total,
		Processed:     job.Processed,
		CachedURLs:    job.Cached,
		SkippedURLs:   job.Skipped,
		Interrupted:   job.Interrupted,
		Error:         job.Error,
		SubmittedAt:   job.SubmittedAt,
		StartedAt:     job.StartedAt,
		CompletedAt:   job.CompletedAt,
		DurationMS:    job.Duration.Milliseconds(),
		MaxURLs:       job.MaxURLs,
		ABaseOverride: job.ABaseOverride,
		URLStatuses:   append([]sitemapWarmURLStatus(nil), job.URLStatuses...),
	}
}

func (job *sitemapWarmJob) setState(state sitemapWarmJobState) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.State = state
	if state == jobStateRunning {
		job.StartedAt = time.Now()
	} else if state == jobStateCompleted || state == jobStateErrored {
		job.CompletedAt = time.Now()
		if !job.StartedAt.IsZero() {
			job.Duration = job.CompletedAt.Sub(job.StartedAt)
		}
	}
}

func (job *sitemapWarmJob) markError(err error) {
	job.mu.Lock()
	job.State = jobStateErrored
	job.Error = err.Error()
	job.CompletedAt = time.Now()
	if !job.StartedAt.IsZero() {
		job.Duration = job.CompletedAt.Sub(job.StartedAt)
	}
	job.mu.Unlock()
}

func (job *sitemapWarmJob) updateTotal(n int) {
	job.mu.Lock()
	job.Total = n
	job.mu.Unlock()
}

func (job *sitemapWarmJob) incrementProcessed() {
	job.mu.Lock()
	job.Processed++
	job.mu.Unlock()
}

func (job *sitemapWarmJob) incrementCached() {
	job.mu.Lock()
	job.Cached++
	job.mu.Unlock()
}

func (job *sitemapWarmJob) incrementSkipped() {
	job.mu.Lock()
	job.Skipped++
	job.mu.Unlock()
}

func (job *sitemapWarmJob) addURLStatus(status sitemapWarmURLStatus) {
	job.mu.Lock()
	job.URLStatuses = append(job.URLStatuses, status)
	job.mu.Unlock()
}

func (job *sitemapWarmJob) setInterrupted() {
	job.mu.Lock()
	job.Interrupted = true
	job.mu.Unlock()
}

type sitemapWarmJobStatus struct {
	JobID         string                 `json:"job_id"`
	SitemapURL    string                 `json:"sitemap_url"`
	State         string                 `json:"state"`
	TotalURLs     int                    `json:"total_urls"`
	Processed     int                    `json:"processed_urls"`
	CachedURLs    int                    `json:"cached_urls"`
	SkippedURLs   int                    `json:"skipped_urls"`
	Interrupted   bool                   `json:"interrupted"`
	Error         string                 `json:"error,omitempty"`
	SubmittedAt   time.Time              `json:"submitted_at"`
	StartedAt     time.Time              `json:"started_at"`
	CompletedAt   time.Time              `json:"completed_at"`
	DurationMS    int64                  `json:"duration_ms"`
	MaxURLs       int                    `json:"max_urls"`
	ABaseOverride string                 `json:"a_base_url_override,omitempty"`
	URLStatuses   []sitemapWarmURLStatus `json:"url_statuses,omitempty"`
}

type sitemapWarmManager struct {
	cfg    *Config
	pf     *Prefetcher
	client *http.Client
	mu     sync.Mutex
	jobs   map[string]*sitemapWarmJob
	seq    uint64
}

func newSitemapWarmManager(cfg *Config, pf *Prefetcher, client *http.Client) *sitemapWarmManager {
	return &sitemapWarmManager{
		cfg:    cfg,
		pf:     pf,
		client: client,
		jobs:   make(map[string]*sitemapWarmJob),
	}
}

func (m *sitemapWarmManager) StartJob(sitemapURL string, max int, aBaseOverride string) (*sitemapWarmJob, error) {
	if sitemapURL == "" {
		return nil, fmt.Errorf("sitemap_url required")
	}
	id := fmt.Sprintf("job-%d", atomic.AddUint64(&m.seq, 1))
	job := &sitemapWarmJob{
		ID:            id,
		SitemapURL:    sitemapURL,
		MaxURLs:       max,
		ABaseOverride: strings.TrimSpace(aBaseOverride),
		State:         jobStateQueued,
		SubmittedAt:   time.Now(),
	}
	m.mu.Lock()
	m.jobs[id] = job
	m.mu.Unlock()

	logger.Infow("sitemap_cache_job_enqueued", map[string]interface{}{"job_id": id, "sitemap": sitemapURL, "max_urls": max, "override": job.ABaseOverride})
	go m.run(job)
	return job, nil
}

func (m *sitemapWarmManager) run(job *sitemapWarmJob) {
	bURL, err := url.Parse(m.cfg.BBaseURL)
	if err != nil {
		job.markError(fmt.Errorf("invalid b_base_url: %w", err))
		logger.Errorw("sitemap_cache_job_error", map[string]interface{}{"job_id": job.ID, "err": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), sitemapWarmJobTimeout)
	defer cancel()
	job.setState(jobStateRunning)
	logger.Infow("sitemap_cache_job_started", map[string]interface{}{"job_id": job.ID, "sitemap": job.SitemapURL})

	urls, err := collectSitemapURLs(ctx, m.client, job.SitemapURL, job.MaxURLs)
	if err != nil {
		job.markError(err)
		logger.Errorw("sitemap_cache_job_error", map[string]interface{}{"job_id": job.ID, "err": err.Error()})
		return
	}
	job.updateTotal(len(urls))
	aBase := strings.TrimSpace(m.cfg.ABaseURL)
	if job.ABaseOverride != "" {
		aBase = job.ABaseOverride
	}
	seen := make(map[string]struct{})
	delay := time.Duration(m.cfg.SitemapWarmDelaySeconds) * time.Second
urlsLoop:
	for idx, loc := range urls {
		if ctx.Err() != nil {
			job.setInterrupted()
			break
		}
		u, err := url.Parse(loc)
		if err != nil {
			job.incrementProcessed()
			job.incrementSkipped()
			logger.Infow("sitemap_cache_job_url_skipped", map[string]interface{}{
				"job_id":  job.ID,
				"sitemap": job.SitemapURL,
				"raw_url": loc,
				"reason":  "parse_error",
				"error":   err.Error(),
			})
			job.addURLStatus(sitemapWarmURLStatus{
				RawURL: loc,
				Status: "skipped",
				Reason: "parse_error",
				Error:  err.Error(),
			})
			continue
		}
		if u.Host == "" {
			u.Scheme = bURL.Scheme
			u.Host = bURL.Host
		}
		if !strings.EqualFold(u.Host, bURL.Host) {
			job.incrementProcessed()
			job.incrementSkipped()
			logger.Infow("sitemap_cache_job_url_skipped", map[string]interface{}{
				"job_id":     job.ID,
				"sitemap":    job.SitemapURL,
				"raw_url":    loc,
				"normalized": u.String(),
				"reason":     "host_mismatch",
				"expected":   bURL.Host,
				"actual":     u.Host,
			})
			job.addURLStatus(sitemapWarmURLStatus{
				RawURL:       loc,
				URL:          u.String(),
				Status:       "skipped",
				Reason:       "host_mismatch",
				ExpectedHost: bURL.Host,
				ActualHost:   u.Host,
			})
			continue
		}
		u.Fragment = ""
		target := u.String()
		if _, dup := seen[target]; dup {
			job.incrementProcessed()
			job.incrementSkipped()
			job.addURLStatus(sitemapWarmURLStatus{
				RawURL: loc,
				URL:    target,
				Status: "skipped",
				Reason: "duplicate",
			})
			logger.Debugw("sitemap_cache_job_url_skipped", map[string]interface{}{
				"job_id":  job.ID,
				"sitemap": job.SitemapURL,
				"target":  target,
				"reason":  "duplicate",
			})
			continue
		}
		seen[target] = struct{}{}
		job.incrementProcessed()
		var (
			success bool
			lastErr error
		)
		for attempt := 1; attempt <= sitemapWarmMaxAttempts; attempt++ {
			success, lastErr = m.pf.FetchAndStore(target, aBase)
			if success {
				job.incrementCached()
				logger.Infow("sitemap_cache_job_url_cached", map[string]interface{}{
					"job_id":  job.ID,
					"sitemap": job.SitemapURL,
					"target":  target,
					"attempt": attempt,
					"a_base":  aBase,
				})
				job.addURLStatus(sitemapWarmURLStatus{
					RawURL:   loc,
					URL:      target,
					Status:   "cached",
					Attempts: attempt,
				})
				break
			}
			if ctx.Err() != nil {
				job.setInterrupted()
				break urlsLoop
			}
		}
		if ctx.Err() != nil {
			job.setInterrupted()
			break
		}
		if !success {
			job.incrementSkipped()
			errMsg := ""
			if lastErr != nil {
				errMsg = lastErr.Error()
			}
			logger.Warnw("sitemap_cache_job_url_failed", map[string]interface{}{
				"job_id":   job.ID,
				"sitemap":  job.SitemapURL,
				"target":   target,
				"attempts": sitemapWarmMaxAttempts,
				"error":    errMsg,
			})
			job.addURLStatus(sitemapWarmURLStatus{
				RawURL:   loc,
				URL:      target,
				Status:   "failed",
				Reason:   "fetch_failed",
				Attempts: sitemapWarmMaxAttempts,
				Error:    errMsg,
			})
		}
		if delay > 0 && idx < len(urls)-1 {
			select {
			case <-ctx.Done():
				job.setInterrupted()
				break urlsLoop
			case <-time.After(delay):
			}
		}
	}
	if job.Interrupted {
		err := fmt.Errorf("job timed out after %s before processing all URLs", sitemapWarmJobTimeout)
		job.markError(err)
		logger.Warnw("sitemap_cache_job_interrupted", map[string]interface{}{
			"job_id":    job.ID,
			"sitemap":   job.SitemapURL,
			"total":     job.Total,
			"processed": job.Processed,
			"cached":    job.Cached,
			"skipped":   job.Skipped,
		})
		return
	}
	job.setState(jobStateCompleted)
	logger.Infow("sitemap_cache_job_completed", map[string]interface{}{
		"job_id":    job.ID,
		"sitemap":   job.SitemapURL,
		"total":     job.Total,
		"processed": job.Processed,
		"cached":    job.Cached,
		"skipped":   job.Skipped,
	})
}

func (m *sitemapWarmManager) GetJob(id string) (*sitemapWarmJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	return job, ok
}

func (m *sitemapWarmManager) ListJobs() []*sitemapWarmJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*sitemapWarmJob, 0, len(m.jobs))
	for _, job := range m.jobs {
		out = append(out, job)
	}
	return out
}
