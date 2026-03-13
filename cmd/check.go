package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"slinky/internal/fsurls"
	"slinky/internal/report"
	"slinky/internal/web"
)

// SerializableResult mirrors web.Result but omits the error field for JSON.
type SerializableResult struct {
	URL         string   `json:"url"`
	OK          bool     `json:"ok"`
	Status      int      `json:"status"`
	ErrMsg      string   `json:"error"`
	Method      string   `json:"method"`
	ContentType string   `json:"contentType"`
	Sources     []string `json:"sources"`
}

func init() {
	checkCmd := &cobra.Command{
		Use:   "check [targets...]",
		Short: "Scan for URLs and validate them (headless)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse targets: allow comma-separated chunks
			var raw []string
			for _, a := range args {
				for part := range strings.SplitSeq(a, ",") {
					p := strings.TrimSpace(part)
					if p != "" {
						raw = append(raw, toSlash(p))
					}
				}
			}
			if len(raw) == 0 {
				raw = []string{"**/*"}
			}

			// Separate into globs (relative to ".") and concrete paths (dirs/files)
			var globPatterns []string
			type pathRoot struct {
				path  string
				isDir bool
			}
			var roots []pathRoot
			for _, t := range raw {
				if hasGlobMeta(t) {
					globPatterns = append(globPatterns, t)
					continue
				}
				if fi, err := os.Stat(t); err == nil {
					roots = append(roots, pathRoot{path: t, isDir: fi.IsDir()})
				} else {
					// If stat fails, treat as glob pattern under "."
					globPatterns = append(globPatterns, t)
				}
			}

			// Debug: show effective targets
			if shouldDebug() {
				fmt.Printf("::debug:: Roots: %s\n", strings.Join(func() []string {
					var out []string
					for _, r := range roots {
						out = append(out, r.path)
					}
					return out
				}(), ","))
				fmt.Printf("::debug:: Glob patterns: %s\n", strings.Join(globPatterns, ","))
			}

			// Load ignore configurations once for all targets
			gitIgnore := fsurls.LoadGitIgnore(".")
			slPathIgnore, slURLPatterns := fsurls.LoadSlinkyIgnore(".")

			// Aggregate URL->files across all targets
			agg := make(map[string]map[string]struct{})
			merge := func(res map[string][]string, prefix string, isDir bool) {
				for u, files := range res {
					set, ok := agg[u]
					if !ok {
						set = make(map[string]struct{})
						agg[u] = set
					}
					for _, fp := range files {
						var merged string
						if prefix == "" {
							merged = fp
						} else if isDir {
							merged = toSlash(filepath.Join(prefix, fp))
						} else {
							// File root: keep the concrete file path
							merged = toSlash(prefix)
						}
						set[merged] = struct{}{}
					}
				}
			}

			// 1) Collect for globs under current dir
			if len(globPatterns) > 0 {
				res, err := fsurls.CollectURLsWithIgnoreConfig(".", globPatterns, respectGitignore, gitIgnore, slPathIgnore, slURLPatterns)
				if err != nil {
					return err
				}
				merge(res, "", true)
			}

			// 2) Collect for each concrete root
			for _, r := range roots {
				clean := toSlash(filepath.Clean(r.path))
				if r.isDir {
					res, err := fsurls.CollectURLsWithIgnoreConfig(r.path, []string{"**/*"}, respectGitignore, gitIgnore, slPathIgnore, slURLPatterns)
					if err != nil {
						return err
					}
					merge(res, clean, true)
				} else {
					res, err := fsurls.CollectURLsWithIgnoreConfig(r.path, nil, respectGitignore, gitIgnore, slPathIgnore, slURLPatterns)
					if err != nil {
						return err
					}
					merge(res, clean, false)
				}
			}

			// Convert aggregator to final map with sorted file lists
			urlToFiles := make(map[string][]string, len(agg))
			for u, set := range agg {
				var files []string
				for f := range set {
					files = append(files, f)
				}
				sort.Strings(files)
				urlToFiles[u] = files
			}

			// Derive display root; we use "." when multiple roots to avoid confusion
			displayRoot := "."
			if len(roots) == 1 && len(globPatterns) == 0 {
				displayRoot = roots[0].path
			}
			if shouldDebug() {
				fmt.Printf("::debug:: Root: %s\n", displayRoot)
			}

			// Build config
			timeout := time.Duration(timeoutSeconds) * time.Second
			cfg := web.Config{MaxConcurrency: maxConcurrency, RequestTimeout: timeout}

			// Prepare URL list
			var urls []string
			for u := range urlToFiles {
				urls = append(urls, u)
			}
			sort.Strings(urls)

			// If no URLs found, exit early
			if len(urls) == 0 {
				fmt.Println("No URLs found.")
				return nil
			}

			// Run checks
			startedAt := time.Now()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			results := make(chan web.Result, 256)
			go web.CheckURLs(ctx, urls, urlToFiles, results, nil, cfg)

			var total, okCount, failCount int
			totalURLs := len(urls)
			lastPctLogged := 0
			var failures []SerializableResult
			var failedResults []web.Result

			for r := range results {
				total++
				if r.OK {
					okCount++
				} else {
					failCount++
				}
				// Progress notices every 5%
				if totalURLs > 0 {
					pct := (total * 100) / totalURLs
					for pct >= lastPctLogged+5 && lastPctLogged < 100 {
						lastPctLogged += 5
						fmt.Printf("::progress:: %d%% (%d/%d)\n", lastPctLogged, total, totalURLs)
					}
				}
				// Emit GitHub Actions debug log for each URL.
				// These lines appear only when step debug logging is enabled via the
				// repository/organization secret ACTIONS_STEP_DEBUG=true.
				if shouldDebug() {
					fmt.Printf("::debug:: Scanned URL: %s status=%d ok=%v err=%s sources=%d\n", r.URL, r.Status, r.OK, r.ErrMsg, len(r.Sources))
				}
				if jsonOut != "" && !r.OK {
					failures = append(failures, SerializableResult{
						URL:         r.URL,
						OK:          r.OK,
						Status:      r.Status,
						ErrMsg:      r.ErrMsg,
						Method:      r.Method,
						ContentType: r.ContentType,
						Sources:     r.Sources,
					})
				}
				if !r.OK {
					failedResults = append(failedResults, r)
				}
			}

			// Write JSON if requested (failures only)
			if jsonOut != "" {
				f, ferr := os.Create(jsonOut)
				if ferr != nil {
					return ferr
				}
				enc := json.NewEncoder(f)
				enc.SetIndent("", "  ")
				if err := enc.Encode(failures); err != nil {
					_ = f.Close()
					return err
				}
				_ = f.Close()
			}

			// Build report summary
			base := repoBlobBase
			if strings.TrimSpace(base) == "" {
				base = os.Getenv("SLINKY_REPO_BLOB_BASE_URL")
			}
			summary := report.Summary{
				RootPath:        displayRoot,
				StartedAt:       startedAt,
				FinishedAt:      time.Now(),
				Processed:       total,
				OK:              okCount,
				Fail:            failCount,
				FilesScanned:    countFiles(urlToFiles),
				JSONPath:        jsonOut,
				RepoBlobBaseURL: base,
			}

			// Ensure we have a markdown file if needed for PR comment
			mdPath := mdOut
			ghRepo, ghPR, ghToken, ghOK := detectGitHubPR()
			var finalMDPath string
			if strings.TrimSpace(mdPath) != "" {
				if _, err := report.WriteMarkdown(mdPath, failedResults, summary); err != nil {
					return err
				}
				finalMDPath = mdPath
			} else if ghOK {
				p, err := report.WriteMarkdown("", failedResults, summary)
				if err != nil {
					return err
				}
				finalMDPath = p
			}

			// If running on a PR, post or update the comment(s), chunking as needed
			if commentPR && ghOK && strings.TrimSpace(finalMDPath) != "" {
				b, rerr := os.ReadFile(finalMDPath)
				if rerr == nil {
					full := string(b)
					if shouldDebug() {
						fmt.Printf("::debug:: Report size (chars): %d\n", len(full))
					}
					chunks := chunkMarkdownByURL(full)
					if shouldDebug() {
						fmt.Printf("::debug:: Posting %d chunk(s)\n", len(chunks))
					}
					_ = upsertPRComments(ghRepo, ghPR, ghToken, chunks)
				}
			}

			fmt.Printf("Checked %d URLs: %d OK, %d failed\n", total, okCount, failCount)
			if failOnFailures && failCount > 0 {
				return fmt.Errorf("%d links failed", failCount)
			}
			return nil
		},
	}

	checkCmd.Flags().IntVar(&maxConcurrency, "concurrency", 16, "maximum concurrent requests")
	checkCmd.Flags().StringVar(&jsonOut, "json-out", "", "path to write full JSON results (array)")
	checkCmd.Flags().StringVar(&mdOut, "md-out", "", "path to write Markdown report for PR comment")
	checkCmd.Flags().StringVar(&repoBlobBase, "repo-blob-base", "", "override GitHub blob base URL (e.g. https://github.com/owner/repo/blob/<sha>)")
	checkCmd.Flags().IntVar(&timeoutSeconds, "timeout", 10, "HTTP request timeout in seconds")
	checkCmd.Flags().BoolVar(&failOnFailures, "fail-on-failures", true, "exit non-zero if any links fail")
	checkCmd.Flags().BoolVar(&commentPR, "comment-pr", true, "post a PR comment with the report when running on a pull request")
	checkCmd.Flags().BoolVar(&respectGitignore, "respect-gitignore", true, "respect .gitignore while scanning (default true)")

	rootCmd.AddCommand(checkCmd)
}

var (
	timeoutSeconds   int
	failOnFailures   bool
	commentPR        bool
	repoBlobBase     string
	respectGitignore bool
)

func toSlash(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	p = filepath.ToSlash(p)
	if after, ok := strings.CutPrefix(p, "./"); ok {
		p = after
	}
	return p
}

func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func countFiles(urlToFiles map[string][]string) int {
	seen := make(map[string]struct{})
	for _, files := range urlToFiles {
		for _, f := range files {
			seen[f] = struct{}{}
		}
	}
	return len(seen)
}

func detectGitHubPR() (repo string, prNumber int, token string, ok bool) {
	repo = os.Getenv("GITHUB_REPOSITORY")
	token = os.Getenv("GITHUB_TOKEN")
	eventPath := os.Getenv("GITHUB_EVENT_PATH")
	if repo == "" || eventPath == "" || token == "" {
		return "", 0, "", false
	}
	data, err := os.ReadFile(eventPath)
	if err != nil {
		return "", 0, "", false
	}
	var ev struct {
		PullRequest struct {
			Number int `json:"number"`
		} `json:"pull_request"`
	}
	_ = json.Unmarshal(data, &ev)
	if ev.PullRequest.Number == 0 {
		return "", 0, "", false
	}
	return repo, ev.PullRequest.Number, token, true
}

// chunkMarkdownByURL splits markdown into chunks under GitHub's comment body limit,
// keeping whole URL entries together. Only the first chunk includes the original
// header and the "Failures by URL" section header. Subsequent chunks have no headers.
func chunkMarkdownByURL(body string) []string {
	const maxBody = 65000
	lines := strings.Split(body, "\n")
	// locate failures header
	failIdx := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "### Failures by URL" {
			failIdx = i
			break
		}
	}
	if failIdx < 0 {
		// no failures section; return as single chunk
		return []string{body}
	}
	preamble := strings.Join(lines[:failIdx+1], "\n") + "\n"
	entryLines := lines[failIdx+1:]

	// build entries by URL block, starting at lines with "- " at column 0
	type entry struct {
		text   string
		length int
	}
	var entries []entry
	for i := 0; i < len(entryLines); {
		// skip leading blank lines
		for i < len(entryLines) && strings.TrimSpace(entryLines[i]) == "" {
			i++
		}
		if i >= len(entryLines) {
			break
		}
		if !strings.HasPrefix(entryLines[i], "- ") {
			// if unexpected, include line as is
			entries = append(entries, entry{text: entryLines[i] + "\n", length: len(entryLines[i]) + 1})
			i++
			continue
		}
		start := i
		i++
		for i < len(entryLines) && !strings.HasPrefix(entryLines[i], "- ") {
			i++
		}
		block := strings.Join(entryLines[start:i], "\n") + "\n"
		entries = append(entries, entry{text: block, length: len(block)})
	}

	var chunks []string
	// start first chunk with full preamble
	cur := preamble
	curLen := len(cur)
	for _, e := range entries {
		if curLen+e.length > maxBody && curLen > len(preamble) {
			// flush current chunk, start new without headers
			chunks = append(chunks, cur)
			cur = ""
			curLen = 0
		}
		// if new chunk and would still exceed, force place the single large entry
		if curLen == 0 && e.length > maxBody {
			// fallback: include as is; GitHub will still likely accept since entries are typically smaller
		}
		cur += e.text
		curLen += e.length
	}
	if strings.TrimSpace(cur) != "" {
		chunks = append(chunks, cur)
	}
	return chunks
}

// upsertPRComments deletes any existing slinky comments and posts the new chunked comments in order.
func upsertPRComments(repo string, prNumber int, token string, chunks []string) error {
	apiBase := "https://api.github.com"
	listURL := fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=100", apiBase, repo, prNumber)
	req, _ := http.NewRequest(http.MethodGet, listURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var comments []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
	}
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &comments)

	// Delete all existing slinky-report comments to avoid stale entries
	for _, c := range comments {
		if strings.Contains(c.Body, "<!-- slinky-report -->") {
			delURL := fmt.Sprintf("%s/repos/%s/issues/comments/%d", apiBase, repo, c.ID)
			dReq, _ := http.NewRequest(http.MethodDelete, delURL, nil)
			dReq.Header.Set("Authorization", "Bearer "+token)
			dReq.Header.Set("Accept", "application/vnd.github+json")
			_, _ = http.DefaultClient.Do(dReq)
		}
	}

	// Post new comments in order
	for idx, chunk := range chunks {
		body := fmt.Sprintf("%s\n%s", "<!-- slinky-report -->", chunk)
		postURL := fmt.Sprintf("%s/repos/%s/issues/%d/comments", apiBase, repo, prNumber)
		payload, _ := json.Marshal(map[string]string{"body": body})
		req, _ = http.NewRequest(http.MethodPost, postURL, bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Content-Type", "application/json")
		res, _ := http.DefaultClient.Do(req)
		if shouldDebug() {
			fmt.Printf("::debug:: Posted chunk %d/%d: %v\n", idx+1, len(chunks), res)
		}
	}
	return nil
}
