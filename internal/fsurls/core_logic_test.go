package fsurls

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestIsURLIgnored_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		patterns []string
		want     bool
	}{
		{name: "empty patterns", url: "https://example.com", patterns: nil, want: false},
		{name: "blank pattern ignored", url: "https://example.com", patterns: []string{"   "}, want: false},
		{name: "exact match", url: "https://example.com", patterns: []string{"https://example.com"}, want: true},
		{name: "substring match", url: "https://acme.example.com/path", patterns: []string{"acme.example"}, want: true},
		{name: "wildcard star", url: "https://acme.example.com/path", patterns: []string{"*acme*"}, want: true},
		{name: "wildcard question mark", url: "https://a.example.com", patterns: []string{"https://?.example.com"}, want: true},
		{name: "no match", url: "https://example.com", patterns: []string{"https://other.com", "*nomatch*"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isURLIgnored(tt.url, tt.patterns)
			if got != tt.want {
				t.Fatalf("isURLIgnored(%q, %v) = %v, want %v", tt.url, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestSanitizeURLToken_TableDriven(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain https", input: "https://example.com", want: "https://example.com"},
		{name: "angle brackets", input: "<https://example.com>", want: "https://example.com"},
		{name: "quoted url", input: "\"https://example.com\"", want: "https://example.com"},
		{name: "trim trailing punctuation", input: "https://example.com/foo,", want: "https://example.com/foo"},
		{name: "balanced parens preserved", input: "https://example.com/q?(x)", want: "https://example.com/q?(x)"},
		{name: "unbalanced parens trimmed", input: "https://example.com/path)", want: "https://example.com/path"},
		{name: "placeholder host rejected", input: "https://[tenant].example.com", want: ""},
		{name: "no scheme rejected", input: "example.com/path", want: ""},
		{name: "wrong scheme rejected", input: "ftp://example.com", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeURLToken(tt.input)
			if got != tt.want {
				t.Fatalf("sanitizeURLToken(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractCandidateMatches_TableDriven(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		wantContains []string
		wantLen      int
	}{
		{
			name:         "markdown html quoted and bare",
			content:      `[x](https://md.example.com) href="https://href.example.com" "https://quoted.example.com" https://bare.example.com`,
			wantContains: []string{"https://md.example.com", "https://href.example.com", "https://quoted.example.com", "https://bare.example.com"},
		},
		{
			name:         "dedupe overlapping angle and bare",
			content:      `<https://example.com>`,
			wantContains: []string{"https://example.com"},
			wantLen:      1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := extractCandidateMatches(tt.content)
			got := make(map[string]int)
			for _, m := range matches {
				got[m.URL]++
			}
			for _, wantURL := range tt.wantContains {
				if got[wantURL] == 0 {
					t.Fatalf("expected URL %q in matches, got %v", wantURL, got)
				}
			}
			if tt.wantLen > 0 && len(matches) != tt.wantLen {
				t.Fatalf("expected %d matches, got %d (%v)", tt.wantLen, len(matches), got)
			}
		})
	}
}

func TestCollectURLsWithIgnoreConfig_GlobsAndIgnore(t *testing.T) {
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, ".slinkignore"), []byte(`{
  "ignorePaths": ["ignored/"],
  "ignoreURLs": ["*skipme*"]
}`), 0o644); err != nil {
		t.Fatalf("write .slinkignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte(`
https://keep.example.com
https://skipme.example.com/path
`), 0o644); err != nil {
		t.Fatalf("write a.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ignored"), 0o755); err != nil {
		t.Fatalf("mkdir ignored: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignored", "b.md"), []byte(`https://ignored.example.com`), 0o644); err != nil {
		t.Fatalf("write ignored/b.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(`https://txt.example.com`), 0o644); err != nil {
		t.Fatalf("write note.txt: %v", err)
	}

	urls, err := CollectURLsWithIgnoreConfig(root, []string{"**/*.md"}, false, nil, nil, nil)
	if err != nil {
		t.Fatalf("CollectURLsWithIgnoreConfig error: %v", err)
	}

	if _, ok := urls["https://keep.example.com"]; !ok {
		t.Fatalf("expected keep URL in md files; got %v", urls)
	}
	if _, ok := urls["https://skipme.example.com/path"]; ok {
		t.Fatalf("expected skipme URL to be ignored by ignoreURLs")
	}
	if _, ok := urls["https://ignored.example.com"]; ok {
		t.Fatalf("expected ignored directory URL to be ignored by ignorePaths")
	}
	if _, ok := urls["https://txt.example.com"]; ok {
		t.Fatalf("expected txt URL excluded by glob filter")
	}
}

func TestCollectURLsProgressWithIgnoreConfig_Parity(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.md"), []byte("https://one.example.com"), 0o644); err != nil {
		t.Fatalf("write one.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "two.txt"), []byte("https://two.example.com"), 0o644); err != nil {
		t.Fatalf("write two.txt: %v", err)
	}

	t.Chdir(root)

	base, err := CollectURLsWithIgnoreConfig(".", []string{"**/*"}, false, nil, nil, nil)
	if err != nil {
		t.Fatalf("CollectURLsWithIgnoreConfig error: %v", err)
	}

	var seenFiles []string
	withProgress, err := CollectURLsProgressWithIgnoreConfig(".", []string{"**/*"}, false, func(rel string) {
		seenFiles = append(seenFiles, rel)
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("CollectURLsProgressWithIgnoreConfig error: %v", err)
	}

	baseKeys := mapKeys(base)
	progressKeys := mapKeys(withProgress)
	sort.Strings(baseKeys)
	sort.Strings(progressKeys)
	if len(baseKeys) != len(progressKeys) {
		t.Fatalf("URL key count mismatch: base=%v progress=%v", baseKeys, progressKeys)
	}
	for i := range baseKeys {
		if baseKeys[i] != progressKeys[i] {
			t.Fatalf("URL key mismatch: base=%v progress=%v", baseKeys, progressKeys)
		}
	}

	if len(seenFiles) != 2 {
		t.Fatalf("expected onFile called for 2 included files, got %d (%v)", len(seenFiles), seenFiles)
	}
}

func mapKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
