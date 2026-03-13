package web

import (
	"context"
	"net"
	"net/http"
	"sort"
	"sync/atomic"
	"time"
)

// CheckURLs performs concurrent GET requests for each URL and emits Result events.
// sources maps URL -> list of file paths where it was found.
func CheckURLs(ctx context.Context, urls []string, sources map[string][]string, out chan<- Result, stats chan<- Stats, cfg Config) {
	defer close(out)

	// Build HTTP client similar to crawler
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          cfg.MaxConcurrency * 2,
		MaxIdleConnsPerHost:   cfg.MaxConcurrency,
		MaxConnsPerHost:       cfg.MaxConcurrency,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: cfg.RequestTimeout,
	}
	client := &http.Client{Timeout: cfg.RequestTimeout, Transport: transport}

	type job struct{ url string }
	jobs := make(chan job, len(urls))
	done := make(chan struct{})

	// Seed jobs
	unique := make(map[string]struct{}, len(urls))
	for _, u := range urls {
		if u == "" {
			continue
		}
		if _, ok := unique[u]; ok {
			continue
		}
		unique[u] = struct{}{}
		jobs <- job{url: u}
	}
	close(jobs)

	concurrency := cfg.MaxConcurrency
	if concurrency <= 0 {
		concurrency = 8
	}
	var processed atomic.Int64
	var pending atomic.Int64
	pending.Store(int64(len(unique)))

	worker := func() {
		for j := range jobs {
			select {
			case <-ctx.Done():
				return
			default:
			}
			ok, status, resp, err := fetchWithMethod(ctx, client, http.MethodGet, j.url)
			if resp != nil && resp.Body != nil {
				resp.Body.Close()
			}
			// Treat 401/403/408/429 as valid links
			if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests {
				ok = true
				err = nil
			}
			// Check context before sending result
			select {
			case <-ctx.Done():
				return
			default:
			}

			var srcs []string
			if sources != nil {
				srcs = sources[j.url]
			}

			// Send result with context check
			select {
			case out <- Result{URL: j.url, OK: ok, Status: status, Err: err, ErrMsg: errString(err), Method: http.MethodGet, Sources: cloneAndSort(srcs)}:
			case <-ctx.Done():
				return
			}

			p := processed.Add(1)
			pn := pending.Add(-1)
			if stats != nil {
				select {
				case stats <- Stats{Pending: int(pn), Processed: int(p)}:
				default:
				}
			}
		}
		done <- struct{}{}
	}

	for i := 0; i < concurrency; i++ {
		go worker()
	}
	for i := 0; i < concurrency; i++ {
		<-done
	}
}

func cloneAndSort(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
