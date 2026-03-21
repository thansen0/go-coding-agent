package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

var allowedGoActions = map[string][]string{
	"build":    {"go", "build", "."},
	"test":     {"go", "test", "./..."},
	"format":   {"go", "fmt", "./..."},
	"mod_tidy": {"go", "mod", "tidy"},
}

var workspaceRoot string

var allowedShellPrograms = map[string]struct{}{
	"cat":  {},
	"diff": {},
	"find": {},
	"git":  {},
	"go":   {},
	"grep": {},
	"head": {},
	"ls":   {},
	"make": {},
	"node": {},
	"npm":  {},
	"pnpm": {},
	"pwd":  {},
	"rg":   {},
	"sed":  {},
	"tail": {},
	"wc":   {},
	"yarn": {},
}

var blockedShellPrograms = map[string]struct{}{
	"curl":   {},
	"docker": {},
	"ftp":    {},
	"nc":     {},
	"ncat":   {},
	"nmap":   {},
	"scp":    {},
	"ssh":    {},
	"sudo":   {},
	"telnet": {},
	"wget":   {},
}

func SetWorkspaceRoot(root string) error {
	if root == "" {
		return errors.New("workspace root cannot be empty")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return err
	}

	workspaceRoot = resolvedRoot
	return nil
}

func ensureWorkspaceRoot() (string, error) {
	if workspaceRoot == "" {
		return "", errors.New("workspace root is not configured")
	}
	return workspaceRoot, nil
}

func ensurePathWithinRoot(path string, forWrite bool) (string, error) {
	root, err := ensureWorkspaceRoot()
	if err != nil {
		return "", err
	}

	if path == "" {
		return "", errors.New("path cannot be empty")
	}

	joined := path
	if !filepath.IsAbs(joined) {
		joined = filepath.Join(root, path)
	}

	cleaned, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}

	checkPath := cleaned
	if forWrite {
		checkPath = filepath.Dir(cleaned)
	}

	resolvedCheckPath, err := filepath.EvalSymlinks(checkPath)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(root, resolvedCheckPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes workspace root", path)
	}

	return cleaned, nil
}

func tokenizeCommandLine(input string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	var quote rune
	escaped := false

	for _, r := range input {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if escaped {
		return nil, errors.New("unterminated escape in command")
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote in command")
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens, nil
}

func ReadFile(path string) (string, error) {
	resolvedPath, err := ensurePathWithinRoot(path, false)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ReadFileRange(path string, startLine int, endLine int) (string, error) {
	if startLine <= 0 || endLine < startLine {
		return "", errors.New("invalid line range")
	}

	content, err := ReadFile(path)
	if err != nil {
		return "", err
	}

	lines := strings.Split(content, "\n")
	if startLine > len(lines) {
		return "", fmt.Errorf("start_line %d beyond file length %d", startLine, len(lines))
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}

	var builder strings.Builder
	for i := startLine; i <= endLine; i++ {
		builder.WriteString(fmt.Sprintf("%d: %s", i, lines[i-1]))
		if i != endLine {
			builder.WriteByte('\n')
		}
	}
	return builder.String(), nil
}

func WriteFile(path string, content string) (string, error) {
	resolvedPath, err := ensurePathWithinRoot(path, true)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(resolvedPath, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Wrote %d bytes to %s", len(content), resolvedPath), nil
}

func ApplyPatch(path string, before string, after string) (string, error) {
	if before == "" {
		return "", errors.New("before snippet cannot be empty")
	}

	resolvedPath, err := ensurePathWithinRoot(path, true)
	if err != nil {
		return "", err
	}

	originalBytes, err := os.ReadFile(resolvedPath)
	if err != nil {
		return "", err
	}

	original := string(originalBytes)
	count := strings.Count(original, before)
	if count == 0 {
		return "", errors.New("before snippet not found in file")
	}
	if count > 1 {
		return "", fmt.Errorf("before snippet matched %d locations; refine the snippet", count)
	}

	updated := strings.Replace(original, before, after, 1)
	if updated == original {
		return "", errors.New("patch produced no change")
	}

	if err := os.WriteFile(resolvedPath, []byte(updated), 0o644); err != nil {
		return "", err
	}

	return fmt.Sprintf("Applied patch to %s (%d -> %d bytes)", resolvedPath, len(original), len(updated)), nil
}

func ListFiles(pattern string, limit int) (string, error) {
	root, err := ensureWorkspaceRoot()
	if err != nil {
		return "", err
	}
	if limit <= 0 {
		limit = 200
	}

	var files []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}

		rel = filepath.ToSlash(rel)
		if pattern != "" && !strings.Contains(rel, pattern) {
			return nil
		}
		files = append(files, rel)
		if len(files) >= limit {
			return errLimitReached
		}
		return nil
	})
	if err != nil && !errors.Is(err, errLimitReached) {
		return "", err
	}

	sort.Strings(files)
	return strings.Join(files, "\n"), nil
}

var errLimitReached = errors.New("limit reached")

func SearchFiles(query string, glob string, limit int) (string, error) {
	root, err := ensureWorkspaceRoot()
	if err != nil {
		return "", err
	}
	if query == "" {
		return "", errors.New("query cannot be empty")
	}
	if limit <= 0 {
		limit = 20
	}

	var matches []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if glob != "" {
			ok, matchErr := filepath.Match(glob, rel)
			if matchErr != nil {
				return matchErr
			}
			if !ok {
				ok, matchErr = filepath.Match(glob, filepath.Base(rel))
				if matchErr != nil {
					return matchErr
				}
			}
			if !ok {
				return nil
			}
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if strings.Contains(line, query) {
				matches = append(matches, fmt.Sprintf("%s:%d:%s", rel, i+1, line))
				if len(matches) >= limit {
					return errLimitReached
				}
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errLimitReached) {
		return "", err
	}

	if len(matches) == 0 {
		return "No matches found.", nil
	}

	return strings.Join(matches, "\n"), nil
}

func RunGoAction(action string) (string, error) {
	commandArgs, ok := allowedGoActions[action]
	if !ok {
		return "", fmt.Errorf("unsupported go action %q", action)
	}

	root, err := ensureWorkspaceRoot()
	if err != nil {
		return "", err
	}

	c := exec.Command(commandArgs[0], commandArgs[1:]...)
	c.Dir = root
	out, err := c.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("command failed: %w\n%s", err, string(out))
	}
	return string(out), nil
}

func RunCommand(command string, dir string, intent string) (string, error) {
	if intent != "" && intent != "inspection" && intent != "verification" {
		return "", fmt.Errorf("unsupported intent %q", intent)
	}

	parts, err := tokenizeCommandLine(command)
	if err != nil {
		return "", err
	}
	if len(parts) == 0 {
		return "", errors.New("command cannot be empty")
	}

	program := parts[0]
	if strings.Contains(program, string(filepath.Separator)) {
		return "", fmt.Errorf("program %q must not include a path", program)
	}
	if _, blocked := blockedShellPrograms[program]; blocked {
		return "", fmt.Errorf("program %q is blocked", program)
	}
	if _, allowed := allowedShellPrograms[program]; !allowed {
		return "", fmt.Errorf("program %q is not in the allowlist", program)
	}

	for _, arg := range parts[1:] {
		if strings.Contains(arg, "://") {
			return "", fmt.Errorf("network-like argument %q is blocked", arg)
		}
	}

	runDir := "."
	if dir != "" {
		runDir = dir
	}

	resolvedDir, err := ensurePathWithinRoot(runDir, false)
	if err != nil {
		return "", err
	}

	c := exec.Command(program, parts[1:]...)
	c.Dir = resolvedDir
	out, err := c.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("command failed: %w\n%s", err, string(out))
	}

	return string(out), nil
}
