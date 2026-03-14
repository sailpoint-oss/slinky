## Slinky Link Checker

**[sailpoint-oss/slinky](https://github.com/sailpoint-oss/slinky)** · Validate external links across your repository. Ships as a self-contained GitHub Action (Docker) and a CLI.

Part of [SailPoint Open Source](https://github.com/sailpoint-oss).

### Quick start (GitHub Action)

Add a workflow:

```yaml
name: Slinky
on:
  pull_request:
    branches: [ main ]
jobs:
  slinky:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      pull-requests: write
    steps:
      - uses: actions/checkout@v4
      - name: Run Slinky
        uses: sailpoint-oss/slinky@v1
        with:
          targets: "docs/,README.md,**/*.md"
```

### Inputs

- **targets**: Comma-separated paths and patterns to scan. Can be directories, files, or glob patterns (e.g. `docs/,api-specs/**/*.yaml,README.md`). Default: `**/*`
- **concurrency**: Max concurrent requests. Default: `16`
- **timeout**: HTTP timeout seconds. Default: `10`
- **json-out**: Optional JSON results path. Default: `results.json`
- **md-out**: Optional Markdown report path. Default: `results.md`
- **repo-blob-base**: Override GitHub blob base URL (`https://github.com/<owner>/<repo>/blob/<sha>`). Auto-detected in Actions.
- **fail-on-failures**: Fail job on any broken links. Default: `true`
- **comment-pr**: Post Markdown as a PR comment when applicable. Default: `true`
- **step-summary**: Append report to the job summary. Default: `true`
- **watch**: Watch for file changes and automatically re-scan (CLI only). Default: `false`

### Output links in PRs

When running on PRs, Slinky auto-links files using the PR head commit. You can override with `repo-blob-base`.

### CLI

Install (from source):

```bash
go build -o slinky ./
```

Usage:

```bash
# Headless: provide one or more targets (files, dirs, or globs)
slinky check **/*
slinky check ./docs/**/* ./markdown/**/*

# TUI mode: same targets
slinky run **/*

# Watch mode: automatically re-scan on file changes
slinky run --watch **/*
```

Notes:
- Targets can be files, directories, or doublestar globs. Multiple targets are allowed.
- If no targets are provided, the default is `**/*` relative to the current working directory.
- Watch mode monitors file changes and automatically re-scans when files are modified.

### Watch Mode

Watch mode provides real-time link checking by monitoring file changes and automatically re-scanning when files are modified. This is particularly useful during development when you want to ensure links remain valid as you edit files.

**Features:**
- **Automatic Re-scanning**: Detects file changes and triggers new scans automatically
- **Sequential Processing**: Completes file scanning before starting URL checking for accurate counts
- **Real-time Updates**: Shows live progress as files are scanned and URLs are checked
- **Configuration Monitoring**: Watches `.slinkignore` files and re-scans when configuration changes
- **Clean State Management**: Each re-scan starts with a fresh state and accurate file counts

**Usage:**
```bash
# Watch all files in current directory
slinky run --watch

# Watch specific directories or files
slinky run --watch docs/ README.md

# Watch with glob patterns
slinky run --watch "**/*.md" "**/*.yaml"
```

**Controls:**
- `q` or `Ctrl+C`: Quit watch mode
- `f`: Toggle display of failed links only

**How it works:**
1. **Initial Scan**: Performs a complete scan of all target files
2. **File Monitoring**: Watches for changes to files matching the target patterns
3. **Configuration Monitoring**: Also watches `.slinkignore` files for configuration changes
4. **Automatic Re-scan**: When changes are detected, cancels the current scan and starts a fresh one
5. **Clean Restart**: Each re-scan resets counters and provides accurate file counts

### Notes

- Respects `.gitignore`.
- Skips likely binary files and files > 2 MiB.
- Uses a browser-like User-Agent to reduce false negatives.

### .slinkignore

Place a `.slinkignore` file at the repository root to exclude paths and/or specific URLs from scanning and reporting. The format is JSON with two optional arrays:

```json
{
  "ignorePaths": [
    "**/vendor/**",
    "**/*.bak"
  ],
  "ignoreURLs": [
    "https://example.com/this/path/does/not/exist",
    "*localhost:*",
    "*internal.example.com*"
  ]
}
```

- ignorePaths: gitignore-style patterns evaluated against repository-relative paths (uses doublestar `**`).
- ignoreURLs: patterns applied to the full URL string. Supports exact matches, substring contains, and doublestar-style wildcard matches.

Examples:
- Ignore generated folders: `"**/dist/**"`, backups: `"**/*.bak"`.
- Ignore known example or placeholder links: `"*example.com*"`, `"https://example.com/foo"`.

