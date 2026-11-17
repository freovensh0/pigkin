package modes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

const (
	fileLimit         = 1024 * 1024
	totalLimit        = 2 * 1024 * 1024
	numstatLimit      = 2 * 1024 * 1024
	commitsLimit      = 500
	replyLimit        = 8 * 1024 * 1024
	stderrLimit       = 64 * 1024
	defaultGitTimeout = 30 * time.Second
)

var (
	diffFlags   = []string{"--no-color", "--no-ext-diff", "-M"}
	patchFlags  = []string{"--no-color", "--no-ext-diff", "-M", "--src-prefix=a/", "--dst-prefix=b/", "--submodule=short"}
	shaPattern  = regexp.MustCompile(`(?i)^[0-9a-f]{4,64}$`)
	changeNames = map[byte]string{'A': "added", 'D': "deleted", 'R': "renamed", 'C': "copied"}
	errTooLarge = errors.New("diff too large")
	errAbort    = errors.New("git aborted")
)

type gitError struct {
	code    string
	message string
	i18n    *host.Text
	timeout bool
}

func (e *gitError) Error() string { return e.message }

func noGit() error {
	return &gitError{code: "no_git", message: "git isn't installed in Termux.", i18n: &host.Text{Key: "git_missing"}}
}

func folderMissing(path string) error {
	return &gitError{code: "not_repo", message: "Folder not found: " + path, i18n: &host.Text{Key: "folder_not_found", Args: map[string]any{"path": path}}}
}

func timedOut(command string) error {
	seconds := gitTimeout().Seconds()
	return &gitError{
		code:    "failed",
		message: fmt.Sprintf("git %s took longer than %s seconds.", command, formatSeconds(seconds)),
		i18n:    &host.Text{Key: "git_timed_out", Args: map[string]any{"command": "git " + command, "seconds": formatSeconds(seconds)}},
		timeout: true,
	}
}

func formatSeconds(seconds float64) string {
	return strconv.FormatFloat(seconds, 'f', -1, 64)
}

func gitFailure(err error) host.Message {
	var gitErr *gitError
	if errors.As(err, &gitErr) {
		reply := host.Message{"error": gitErr.message, "code": gitErr.code}
		if gitErr.i18n != nil {
			reply["errorI18n"] = *gitErr.i18n
		}
		return reply
	}
	var hostErr *host.Error
	if errors.As(err, &hostErr) {
		reply := host.Message{"error": hostErr.Message, "code": hostErr.Code}
		if hostErr.I18n != nil {
			reply["errorI18n"] = *hostErr.I18n
		}
		return reply
	}
	return host.Message{"error": err.Error(), "code": "failed"}
}

func gitTimeout() time.Duration {
	if raw := os.Getenv("PIGKIN_GIT_TIMEOUT_MS"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			return time.Duration(value) * time.Millisecond
		}
	}
	return defaultGitTimeout
}

// git is the long-lived, read-only host for the app's Changes tab. It sends
// pigkin_git_ready, then answers requests with their id until the app hangs up.
// A remote workspace's stand-in runs git on the remote folder it names.
func git(ctx *Context) (host.Message, error) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var wg sync.WaitGroup
	closed := false
	add := func() bool {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return false
		}
		wg.Add(1)
		return true
	}
	go func() {
		<-ctx.Conn.Done()
		cancel()
	}()

	reply := func(kind string, id float64, result host.Message) {
		result["type"] = kind
		result["id"] = id
		ctx.Conn.Send(result)
	}
	ctx.Conn.Send(host.Message{"type": "pigkin_git_ready"})
	ctx.Conn.Listen(func(message host.Message) {
		id, ok := message["id"].(float64)
		if !ok {
			return
		}
		root, ok := message["root"].(string)
		if !ok {
			return
		}
		switch message["type"] {
		case "pigkin_git_detect":
			trusted, ok := message["trusted"].(bool)
			if !ok || !add() {
				return
			}
			go func() {
				defer wg.Done()
				result := detect(base, ctx.Env, root, trusted)
				if result != nil {
					reply("pigkin_git_detected", id, result)
				}
			}()
		case "pigkin_git_changes":
			mode, ok := message["mode"].(string)
			if !ok || (mode != "uncommitted" && mode != "base") || !add() {
				return
			}
			ignore, ok := message["ignoreWhitespace"].(bool)
			if !ok {
				wg.Done()
				return
			}
			go func() {
				defer wg.Done()
				result := changes(base, ctx.Env, root, mode, ignore, message["known"])
				if result != nil {
					reply("pigkin_git_changes_result", id, result)
				}
			}()
		case "pigkin_git_commits":
			if !add() {
				return
			}
			go func() {
				defer wg.Done()
				result := commits(base, ctx.Env, root, message["known"])
				if result != nil {
					reply("pigkin_git_commits_result", id, result)
				}
			}()
		case "pigkin_git_commit_diff":
			sha, ok := message["sha"].(string)
			if !ok || !add() {
				return
			}
			ignore, ok := message["ignoreWhitespace"].(bool)
			if !ok {
				wg.Done()
				return
			}
			go func() {
				defer wg.Done()
				result := commitDiff(base, ctx.Env, root, sha, ignore)
				if result != nil {
					reply("pigkin_git_commit_diff_result", id, result)
				}
			}()
		}
	})
	<-ctx.Conn.Done()
	cancel()
	mu.Lock()
	closed = true
	mu.Unlock()
	wg.Wait()
	return host.Message{}, nil
}

// gitProcess starts git for a repo: in Termux, or on a remote host over ssh.
type gitProcess interface {
	run(ctx context.Context, args []string, env map[string]string, onStdout, onStderr func([]byte)) (int, error)
}

type repo struct {
	base    context.Context
	root    string
	process gitProcess
	remote  *remoteFolder
}

type gitPaths struct {
	index   string
	objects string
}

type runOptions struct {
	env         map[string]string
	anyExit     bool
	stdoutLimit int
	truncateAt  int
	onStdout    func([]byte)
}

type runResult struct {
	exitCode  int
	stdout    []byte
	truncated bool
}

func openRepo(base context.Context, env host.Env, root string) (*repo, error) {
	folder, err := remoteFolderForEnv(env, root)
	if err != nil {
		return nil, err
	}
	if folder != nil {
		return &repo{base: base, root: folder.path, process: remoteGit{folder: folder}, remote: folder}, nil
	}
	binary := filepath.Join(env.Prefix, "bin", "git")
	if !isExecutable(binary) {
		return nil, noGit()
	}
	if !filepath.IsAbs(root) {
		return nil, folderMissing(root)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, folderMissing(root)
	}
	safeArgs := []string{"-c", "safe.directory=" + root}
	if realRoot != root {
		safeArgs = append(safeArgs, "-c", "safe.directory="+realRoot)
	}
	return &repo{base: base, root: root, process: localGit{binary: binary, root: root, safeArgs: safeArgs}}, nil
}

type localGit struct {
	binary   string
	root     string
	safeArgs []string
}

func (g localGit) run(ctx context.Context, args []string, env map[string]string, onStdout, onStderr func([]byte)) (int, error) {
	cmd := exec.CommandContext(ctx, g.binary, append(append([]string{}, g.safeArgs...), args...)...)
	cmd.Dir = g.root
	cmd.Env = os.Environ()
	for name, value := range env {
		cmd.Env = append(cmd.Env, name+"="+value)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdout = writerFunc(func(data []byte) (int, error) {
		onStdout(data)
		return len(data), nil
	})
	cmd.Stderr = writerFunc(func(data []byte) (int, error) {
		onStderr(data)
		return len(data), nil
	})
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		return -1, nil
	}
	return cmd.ProcessState.ExitCode(), nil
}

// What a remote git command writes to stderr, with remoteSetupFailed, when it
// can't start git or find the folder.
const (
	remoteNoFolder    = "pigkin: no such folder"
	remoteNoGit       = "pigkin: no git"
	remoteSetupFailed = 127
)

type remoteGit struct{ folder *remoteFolder }

func (g remoteGit) run(ctx context.Context, args []string, env map[string]string, onStdout, onStderr func([]byte)) (int, error) {
	command, err := remoteGitCommand(g.folder.path, args, env)
	if err != nil {
		return 0, err
	}
	result, err := runWatched(g.folder.remote, command, "/", sshRunOptions{
		ctx: ctx, idle: readIdleMs(), onStdout: onStdout, onStderr: onStderr,
	})
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil
		}
		return 0, asRemoteError(g.folder.remote, err)
	}
	code := -1
	if result.code != nil {
		code = *result.code
	}
	if code == remoteSetupFailed {
		if strings.Contains(result.stderr, remoteNoGit) {
			return code, remoteGitMissing(g.folder)
		}
		if strings.Contains(result.stderr, remoteNoFolder) {
			return code, folderMissing(g.folder.path)
		}
	}
	return code, nil
}

func remoteGitMissing(folder *remoteFolder) error {
	name := folder.remote.host.Name
	return &gitError{
		code:    "no_git",
		message: "git isn't installed on " + name + ".",
		i18n:    &host.Text{Key: "remote_git_missing", Args: map[string]any{"host": name}},
	}
}

// remoteGitCommand runs git with args and env in remote root, trusting the
// root and its real path as safe directories.
func remoteGitCommand(root string, args []string, env map[string]string) (string, error) {
	quotedRoot, err := shellQuote(root)
	if err != nil {
		return "", err
	}
	lines := []string{
		`cd -- ` + quotedRoot + ` 2>/dev/null || { echo ` + shellQuoteOrEmpty(remoteNoFolder) + ` >&2; exit 127; }`,
		`command -v git >/dev/null 2>&1 || { echo ` + shellQuoteOrEmpty(remoteNoGit) + ` >&2; exit 127; }`,
	}
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		quoted, err := shellQuote(env[name])
		if err != nil {
			return "", err
		}
		lines = append(lines, `export `+name+`=`+quoted)
	}
	quotedArgs := make([]string, 0, len(args))
	for _, arg := range args {
		quoted, err := shellQuote(arg)
		if err != nil {
			quoted = shellQuoteOrEmpty(arg)
		}
		quotedArgs = append(quotedArgs, quoted)
	}
	lines = append(lines,
		`real=$(pwd -P && echo x); real=${real%x}; real=${real%$'\n'}`,
		`exec git -c safe.directory=`+quotedRoot+` -c "safe.directory=$real" `+strings.Join(quotedArgs, " "),
	)
	return strings.Join(lines, "\n"), nil
}

// posixResolve is node's path.resolve for a remote root and a possibly
// relative path from git.
func posixResolve(root, raw string) string {
	if path.IsAbs(raw) {
		return path.Clean(raw)
	}
	return path.Join(root, raw)
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func hasDotGit(root string) bool {
	info, err := os.Lstat(filepath.Join(root, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

func readText(r *repo, filePath string) (string, error) {
	if r.remote != nil {
		data, err := readRemoteFile(r.remote.remote, posixResolve(r.root, filePath), sshRunOptions{ctx: r.base, idle: readIdleMs()})
		if err != nil {
			return "", asRemoteError(r.remote.remote, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	data, err := os.ReadFile(resolvePath(r.root, filePath))
	return strings.TrimSpace(string(data)), err
}

func gitEnvironment(options runOptions) map[string]string {
	env := map[string]string{
		"GIT_OPTIONAL_LOCKS":    "0",
		"GIT_TERMINAL_PROMPT":   "0",
		"GIT_LITERAL_PATHSPECS": "1",
		"LC_ALL":                "C",
	}
	for name, value := range options.env {
		env[name] = value
	}
	return env
}

func run(r *repo, args []string, options runOptions) (runResult, error) {
	ctx, cancel := context.WithTimeout(r.base, gitTimeout())
	defer cancel()

	var mu sync.Mutex
	reason := ""
	mark := func(value string) {
		mu.Lock()
		if reason == "" {
			reason = value
		}
		mu.Unlock()
		cancel()
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	onStdout := func(data []byte) {
		mu.Lock()
		if reason != "" {
			mu.Unlock()
			return
		}
		if options.onStdout != nil {
			mu.Unlock()
			options.onStdout(data)
			return
		}
		if options.truncateAt > 0 && stdout.Len()+len(data) > options.truncateAt {
			remaining := options.truncateAt - stdout.Len()
			if remaining > 0 {
				stdout.Write(data[:remaining])
			}
			mu.Unlock()
			mark("truncated")
			return
		}
		stdout.Write(data)
		overflow := options.stdoutLimit > 0 && stdout.Len() > options.stdoutLimit
		mu.Unlock()
		if overflow {
			mark("overflow")
		}
	}
	onStderr := func(data []byte) {
		if stderr.Len() < stderrLimit {
			remaining := stderrLimit - stderr.Len()
			if len(data) < remaining {
				remaining = len(data)
			}
			stderr.Write(data[:remaining])
		}
	}

	exitCode, processErr := r.process.run(ctx, args, gitEnvironment(options), onStdout, onStderr)
	mu.Lock()
	why := reason
	mu.Unlock()
	if why == "" {
		if r.base.Err() != nil {
			why = "abort"
		} else if ctx.Err() == context.DeadlineExceeded {
			why = "timeout"
		}
	}
	if why == "abort" {
		return runResult{}, errAbort
	}
	if why == "timeout" {
		return runResult{}, timedOut(args[0])
	}
	if why == "overflow" {
		return runResult{}, errTooLarge
	}
	if processErr != nil {
		return runResult{}, processErr
	}
	result := runResult{exitCode: exitCode, stdout: stdout.Bytes(), truncated: why == "truncated"}
	if result.truncated || exitCode == 0 || options.anyExit {
		return result, nil
	}
	if text := strings.TrimSpace(stderr.String()); text != "" {
		return result, &gitError{code: "failed", message: text}
	}
	command := "git " + args[0]
	return result, &gitError{
		code:    "failed",
		message: fmt.Sprintf("%s exited with %d.", command, exitCode),
		i18n:    &host.Text{Key: "git_exited", Args: map[string]any{"command": command, "exit": strconv.Itoa(exitCode)}},
	}
}

func requireRepo(r *repo) (gitPaths, error) {
	result, err := run(r, []string{"rev-parse", "--show-toplevel", "--git-path", "index", "--git-path", "objects"}, runOptions{})
	if err != nil {
		var gitErr *gitError
		if errors.As(err, &gitErr) && gitErr.code == "failed" {
			return gitPaths{}, &gitError{code: "not_repo", message: gitErr.message, i18n: gitErr.i18n, timeout: gitErr.timeout}
		}
		return gitPaths{}, err
	}
	lines := strings.Split(string(result.stdout), "\n")
	return gitPaths{
		index:   resolvePath(r.root, at(lines, 1)),
		objects: resolvePath(r.root, at(lines, 2)),
	}, nil
}

func at(lines []string, index int) string {
	if index < len(lines) {
		return lines[index]
	}
	return ""
}

func refExists(r *repo, fullRef string) bool {
	result, err := run(r, []string{"show-ref", "--verify", "--quiet", fullRef}, runOptions{anyExit: true})
	return err == nil && result.exitCode == 0
}

func currentBranch(r *repo) (string, error) {
	result, err := run(r, []string{"symbolic-ref", "--quiet", "--short", "HEAD"}, runOptions{anyExit: true})
	if err != nil {
		return "", err
	}
	if result.exitCode == 0 && strings.TrimSpace(string(result.stdout)) != "" {
		return strings.TrimSpace(string(result.stdout)), nil
	}
	paths, err := run(r, []string{"rev-parse", "--git-path", "rebase-merge/head-name", "--git-path", "rebase-apply/head-name"}, runOptions{})
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(paths.stdout), "\n")
	for _, path := range lines[:min(2, len(lines))] {
		name, err := readText(r, path)
		if err != nil || name == "" {
			continue
		}
		name = strings.TrimPrefix(name, "refs/heads/")
		if name != "" {
			return name, nil
		}
	}
	return "", nil
}

func headCommit(r *repo) (string, error) {
	result, err := run(r, []string{"rev-parse", "--verify", "--quiet", "HEAD^{commit}"}, runOptions{anyExit: true})
	if err != nil {
		return "", err
	}
	if result.exitCode == 0 && strings.TrimSpace(string(result.stdout)) != "" {
		return strings.TrimSpace(string(result.stdout)), nil
	}
	return "", nil
}

func emptyTree(r *repo) (string, error) {
	result, err := run(r, []string{"hash-object", "-t", "tree", "/dev/null"}, runOptions{})
	return strings.TrimSpace(string(result.stdout)), err
}

func defaultBranch(r *repo) (string, error) {
	originHead, err := run(r, []string{"symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"}, runOptions{anyExit: true})
	if err != nil {
		return "", err
	}
	ref := ""
	if originHead.exitCode == 0 {
		ref = strings.TrimSpace(string(originHead.stdout))
	}
	if ref != "" {
		remoteName := strings.TrimPrefix(ref, "refs/remotes/")
		localName := strings.TrimPrefix(remoteName, "origin/")
		if refExists(r, "refs/heads/"+localName) {
			return localName, nil
		}
		return remoteName, nil
	}
	if refExists(r, "refs/heads/main") {
		return "main", nil
	}
	if refExists(r, "refs/heads/master") {
		return "master", nil
	}
	return "", nil
}

func branchNameFromRef(ref string) string {
	if strings.HasPrefix(ref, "refs/heads/") {
		return strings.TrimPrefix(ref, "refs/heads/")
	}
	if strings.HasPrefix(ref, "refs/remotes/") {
		remainder := strings.TrimPrefix(ref, "refs/remotes/")
		if index := strings.IndexByte(remainder, '/'); index >= 0 {
			return remainder[index+1:]
		}
		return remainder
	}
	return strings.TrimPrefix(ref, "origin/")
}

func comparisonRef(r *repo, base string) (string, error) {
	name := branchNameFromRef(base)
	hasLocal := refExists(r, "refs/heads/"+name)
	hasOrigin := refExists(r, "refs/remotes/origin/"+name)
	if hasOrigin {
		return "origin/" + name, nil
	}
	if hasLocal {
		return name, nil
	}
	return "", nil
}

func mergeBase(r *repo, ref string) (string, error) {
	result, err := run(r, []string{"merge-base", ref, "HEAD"}, runOptions{anyExit: true})
	if err != nil {
		return "", err
	}
	if result.exitCode == 0 && strings.TrimSpace(string(result.stdout)) != "" {
		return strings.TrimSpace(string(result.stdout)), nil
	}
	return "", nil
}

type fileStat struct {
	path      string
	oldPath   string
	status    byte
	additions int
	deletions int
	binary    bool
}

func parseRaw(output string) map[string]*fileStat {
	tokens := strings.Split(output, "\x00")
	files := make(map[string]*fileStat)
	for i := 0; i < len(tokens) && strings.HasPrefix(tokens[i], ":"); i++ {
		fields := strings.Split(tokens[i], " ")
		status := byte('M')
		if len(fields) > 4 && len(fields[4]) > 0 {
			status = fields[4][0]
		}
		oldPath := ""
		if status == 'R' || status == 'C' {
			i++
			oldPath = at(tokens, i)
		}
		i++
		path := at(tokens, i)
		if path == "" {
			break
		}
		if _, exists := files[path]; exists && status == 'U' {
			continue
		}
		files[path] = &fileStat{path: path, oldPath: oldPath, status: status}
	}
	return files
}

func applyNumstat(files map[string]*fileStat, output string, truncated bool) {
	tokens := strings.Split(output, "\x00")
	if truncated && len(tokens) > 0 {
		tokens = tokens[:len(tokens)-1]
	}
	for i := 0; i < len(tokens); i++ {
		record := tokens[i]
		first := strings.IndexByte(record, '\t')
		if first < 0 {
			continue
		}
		second := strings.IndexByte(record[first+1:], '\t')
		if second < 0 {
			continue
		}
		second += first + 1
		path := record[second+1:]
		if path == "" {
			i += 2
			path = at(tokens, i)
		}
		file := files[path]
		if file == nil {
			continue
		}
		added, deleted := record[:first], record[first+1:second]
		file.binary = added == "-" && deleted == "-"
		if !file.binary {
			file.additions, _ = strconv.Atoi(added)
			file.deletions, _ = strconv.Atoi(deleted)
		}
	}
}

type hunk struct {
	Header   string   `json:"header"`
	OldStart int      `json:"oldStart"`
	NewStart int      `json:"newStart"`
	Lines    []string `json:"lines"`
}

type patchSection struct {
	path string
	text string
}

type patchSections struct {
	kept      []patchSection
	tooLarge  map[string]bool
	keptBytes int

	filePath     string
	fileBytes    int
	fileOverflow bool
	fileSections []string

	inSection    bool
	sectionBuf   []byte
	sectionBytes int
	overflowed   bool
	overflowPath string

	line []byte
}

func newPatchSections() *patchSections {
	return &patchSections{tooLarge: make(map[string]bool)}
}

func (p *patchSections) push(chunk []byte) {
	p.line = append(p.line, chunk...)
	for {
		index := bytes.IndexByte(p.line, '\n')
		if index < 0 {
			return
		}
		line := p.line[:index+1]
		p.consumeLine(line)
		p.line = p.line[index+1:]
	}
}

func (p *patchSections) finish() {
	if len(p.line) > 0 {
		p.consumeLine(p.line)
		p.line = nil
	}
	p.endSection()
	p.endFile()
}

func (p *patchSections) consumeLine(line []byte) {
	if bytes.HasPrefix(line, []byte("diff --git ")) {
		p.startSection()
	}
	p.append(line)
}

func (p *patchSections) startSection() {
	p.endSection()
	p.inSection = true
	p.sectionBuf = nil
	p.sectionBytes = 0
	p.overflowed = false
	p.overflowPath = ""
}

func (p *patchSections) endSection() {
	if !p.inSection {
		return
	}
	p.inSection = false
	text := ""
	path := ""
	if p.overflowed {
		path = p.overflowPath
	} else {
		text = string(p.sectionBuf)
		path = sectionPath(text)
	}
	p.sectionBuf = nil
	if path == "" {
		return
	}
	if p.filePath != path {
		p.endFile()
		p.filePath = path
		p.fileSections = nil
		p.fileBytes = 0
		p.fileOverflow = false
	}
	p.fileBytes += p.sectionBytes
	if p.overflowed {
		p.fileOverflow = true
	}
	if !p.fileOverflow {
		p.fileSections = append(p.fileSections, text)
	}
}

func (p *patchSections) endFile() {
	path := p.filePath
	p.filePath = ""
	sections := p.fileSections
	p.fileSections = nil
	size := p.fileBytes
	overflow := p.fileOverflow
	if path == "" {
		return
	}
	if overflow || size > fileLimit || p.keptBytes+size > totalLimit {
		p.tooLarge[path] = true
		return
	}
	for _, text := range sections {
		p.kept = append(p.kept, patchSection{path: path, text: text})
	}
	p.keptBytes += size
}

func (p *patchSections) append(data []byte) {
	if !p.inSection || p.overflowed {
		return
	}
	p.sectionBuf = append(p.sectionBuf, data...)
	p.sectionBytes += len(data)
	if p.sectionBytes > fileLimit {
		p.overflowPath = sectionPath(string(p.sectionBuf))
		p.sectionBuf = nil
		p.overflowed = true
	}
}

func sectionPath(text string) string {
	plus, minus := "", ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "+++ ") {
			plus = strings.TrimPrefix(line, "+++ ")
		}
		if strings.HasPrefix(line, "--- ") {
			minus = strings.TrimPrefix(line, "--- ")
		}
		if strings.HasPrefix(line, "@@ ") {
			break
		}
	}
	name := plus
	if name == "" || name == "/dev/null" {
		name = minus
	}
	name = gitPath(name)
	if name == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(name, "a/") || strings.HasPrefix(name, "b/") {
		return name[2:]
	}
	return name
}

// gitPath decodes git's C-style quoting of unusual path names.
func gitPath(value string) string {
	if len(value) < 2 || value[0] != '"' {
		return value
	}
	var out []byte
	for i := 1; i < len(value); i++ {
		c := value[i]
		if c == '"' {
			break
		}
		if c != '\\' || i+1 >= len(value) {
			out = append(out, c)
			continue
		}
		i++
		switch value[i] {
		case 'a':
			out = append(out, '\a')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'v':
			out = append(out, '\v')
		case '\\', '"':
			out = append(out, value[i])
		default:
			if value[i] >= '0' && value[i] <= '7' {
				number := 0
				count := 0
				for count < 3 && i < len(value) && value[i] >= '0' && value[i] <= '7' {
					number = number*8 + int(value[i]-'0')
					i++
					count++
				}
				i--
				out = append(out, byte(number))
			} else {
				out = append(out, value[i])
			}
		}
	}
	return string(out)
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(data []byte) (int, error) { return f(data) }

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func parseHunks(text string) []hunk {
	lines := strings.Split(text, "\n")
	var hunks []hunk
	var current *hunk
	for _, line := range lines {
		if strings.HasPrefix(line, "@@ ") {
			parts := hunkHeader.FindStringSubmatch(line)
			if parts == nil {
				continue
			}
			oldStart, _ := strconv.Atoi(parts[1])
			newStart, _ := strconv.Atoi(parts[2])
			h := hunk{Header: strings.TrimSuffix(line, "\r"), OldStart: oldStart, NewStart: newStart, Lines: []string{}}
			hunks = append(hunks, h)
			current = &hunks[len(hunks)-1]
			continue
		}
		if current == nil || line == "" {
			continue
		}
		switch line[0] {
		case ' ', '+', '-', '\\':
			current.Lines = append(current.Lines, line)
		}
	}
	return hunks
}

type diffBody struct {
	files    []host.Message
	tooLarge bool
}

func diffFiles(r *repo, revisions []string, ignoreWhitespace bool, env map[string]string) (diffBody, error) {
	whitespace := []string{}
	if ignoreWhitespace {
		whitespace = append(whitespace, "-w")
	}
	args := func(flags []string) []string {
		return append(append(append([]string{}, "diff"), flags...), append(whitespace, append(append([]string{}, revisions...), "--")...)...)
	}
	stop, stopCancel := context.WithCancel(r.base)
	defer stopCancel()
	stopped := *r
	stopped.base = stop
	collector := newPatchSections()

	type outcome struct {
		result runResult
		err    error
	}
	listCh, numstatCh, patchCh := make(chan outcome, 1), make(chan outcome, 1), make(chan outcome, 1)
	go func() {
		result, err := run(r, args(append(append([]string{}, diffFlags...), "--raw", "-z")), runOptions{env: env, stdoutLimit: replyLimit})
		listCh <- outcome{result, err}
	}()
	go func() {
		result, err := run(&stopped, args(append(append([]string{}, diffFlags...), "--numstat", "-z")), runOptions{env: env, truncateAt: numstatLimit})
		numstatCh <- outcome{result, err}
	}()
	go func() {
		result, err := run(&stopped, args(patchFlags), runOptions{env: env, onStdout: collector.push})
		patchCh <- outcome{result, err}
	}()
	list, numstat, patch := <-listCh, <-numstatCh, <-patchCh

	outsized := func(err error) bool {
		return errors.Is(err, errTooLarge) || isTimeout(err)
	}
	if outsized(list.err) || outsized(numstat.err) || outsized(patch.err) {
		return diffBody{tooLarge: true}, nil
	}
	if list.err != nil {
		return diffBody{}, list.err
	}
	if numstat.err != nil {
		return diffBody{}, numstat.err
	}
	if patch.err != nil {
		return diffBody{}, patch.err
	}

	stats := parseRaw(string(list.result.stdout))
	applyNumstat(stats, string(numstat.result.stdout), numstat.result.truncated)
	collector.finish()
	hunksByPath := make(map[string][]hunk)
	for _, section := range collector.kept {
		hunksByPath[section.path] = append(hunksByPath[section.path], parseHunks(section.text)...)
	}

	paths := make([]string, 0, len(stats))
	for path := range stats {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	files := make([]host.Message, 0, len(paths))
	for _, path := range paths {
		file := stats[path]
		if ignoreWhitespace && file.status == 'M' && !file.binary && file.additions == 0 && file.deletions == 0 {
			continue
		}
		entry := host.Message{
			"path":      path,
			"change":    changeNames[file.status],
			"additions": file.additions,
			"deletions": file.deletions,
		}
		if entry["change"] == "" {
			entry["change"] = "modified"
		}
		if file.oldPath != "" {
			entry["oldPath"] = file.oldPath
		}
		hunks := hunksByPath[path]
		switch {
		case file.binary:
			entry["kind"] = "binary"
		case len(hunks) > 0:
			entry["kind"] = "text"
			entry["hunks"] = hunks
		case collector.tooLarge[path]:
			entry["kind"] = "too_large"
		default:
			entry["kind"] = "text"
			entry["hunks"] = []hunk{}
		}
		files = append(files, entry)
	}
	return diffBody{files: files}, nil
}

func isTimeout(err error) bool {
	var gitErr *gitError
	return errors.As(err, &gitErr) && gitErr.timeout
}

// localScratch is a temporary folder holding an empty objects folder and, when
// there is one, a copy of index.
func localScratch(index string) (string, error) {
	dir, err := os.MkdirTemp("", "pigkin-git-")
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if err := os.Mkdir(filepath.Join(dir, "objects"), 0o755); err != nil {
		return fail(err)
	}
	if data, err := os.ReadFile(index); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "index"), data, 0o600); err != nil {
			return fail(err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fail(err)
	}
	return dir, nil
}

// remoteScratch makes the same folder on the remote. A fresh repo has no index
// until the first git add.
func remoteScratch(r *repo, index string) (string, error) {
	quotedIndex, err := shellQuote(index)
	if err != nil {
		return "", err
	}
	script := strings.Join([]string{
		`i=` + quotedIndex,
		`d=$(mktemp -d "${TMPDIR:-/tmp}/pigkin-git-XXXXXX") || exit 1`,
		`mkdir -- "$d/objects" || { rm -rf -- "$d"; exit 1; }`,
		`if [ -e "$i" ]; then cp -- "$i" "$d/index" || { rm -rf -- "$d"; exit 1; }; fi`,
		`printf '%s' "$d"`,
	}, "\n")
	result, err := runScript(r.remote.remote, script, sshRunOptions{ctx: r.base, idle: readIdleMs()})
	if err != nil {
		return "", asRemoteError(r.remote.remote, err)
	}
	if result.code == nil || *result.code != 0 {
		message := strings.TrimSpace(result.stderr)
		if message == "" {
			message = "Couldn't make a temporary folder."
		}
		return "", &gitError{code: "failed", message: message}
	}
	return string(result.stdout), nil
}

func (r *repo) removeScratch(dir string) {
	if r.remote != nil {
		_, _ = runScript(r.remote.remote, "rm -rf -- "+shellQuoteOrEmpty(dir), sshRunOptions{ctx: r.base, timeout: 15 * time.Second})
		return
	}
	_ = os.RemoveAll(dir)
}

func workTreeDiff(r *repo, paths gitPaths, ignoreWhitespace bool) (diffBody, error) {
	from, err := headCommit(r)
	if err != nil {
		return diffBody{}, err
	}
	if from == "" {
		from, err = emptyTree(r)
		if err != nil {
			return diffBody{}, err
		}
	}
	var dir string
	if r.remote != nil {
		dir, err = remoteScratch(r, paths.index)
	} else {
		dir, err = localScratch(paths.index)
	}
	if err != nil {
		return diffBody{}, err
	}
	defer r.removeScratch(dir)
	alternate := paths.objects
	if strings.ContainsAny(alternate, `:"`) {
		quoted, _ := json.Marshal(alternate)
		alternate = string(quoted)
	}
	env := map[string]string{
		"GIT_INDEX_FILE":                   filepath.Join(dir, "index"),
		"GIT_OBJECT_DIRECTORY":             filepath.Join(dir, "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": alternate,
	}
	if _, err := run(r, []string{"add", "--intent-to-add", "--all"}, runOptions{env: env}); err != nil {
		return diffBody{}, err
	}
	return diffFiles(r, []string{from}, ignoreWhitespace, env)
}

func branchDiff(r *repo, base string, ignoreWhitespace bool) (diffBody, error) {
	comparison := ""
	if base != "" {
		var err error
		comparison, err = comparisonRef(r, base)
		if err != nil {
			return diffBody{}, err
		}
	}
	empty := diffBody{files: []host.Message{}}
	if comparison == "" {
		return empty, nil
	}
	head, err := headCommit(r)
	if err != nil || head == "" {
		return empty, err
	}
	from, err := mergeBase(r, comparison)
	if err != nil {
		return diffBody{}, err
	}
	if from == "" {
		from = comparison
	}
	return diffFiles(r, []string{from, "HEAD"}, ignoreWhitespace, nil)
}

func fingerprinted(body host.Message, known any) host.Message {
	data, _ := json.Marshal(body)
	sum := sha256.Sum256(data)
	fingerprint := hex.EncodeToString(sum[:])
	if value, ok := known.(string); ok && value == fingerprint {
		return host.Message{"fingerprint": fingerprint, "unchanged": true}
	}
	result := host.Message{"fingerprint": fingerprint}
	for name, value := range body {
		result[name] = value
	}
	return result
}

func guarded(base context.Context, env host.Env, root string) (*repo, host.Message, error) {
	r, err := openRepo(base, env, root)
	if err != nil {
		if errors.Is(err, errAbort) {
			return nil, nil, err
		}
		return nil, gitFailure(err), nil
	}
	if _, err := requireRepo(r); err != nil {
		if errors.Is(err, errAbort) {
			return nil, nil, err
		}
		return nil, gitFailure(err), nil
	}
	return r, nil, nil
}

func detect(base context.Context, env host.Env, root string, trusted bool) host.Message {
	folder, err := remoteFolderForEnv(env, root)
	if err != nil {
		return gitFailure(err)
	}
	if folder != nil {
		return detectRemote(base, folder, trusted)
	}
	hasGit := isExecutable(filepath.Join(env.Prefix, "bin", "git"))
	dotGit := filepath.IsAbs(root) && hasDotGit(root)
	if !trusted || !hasGit || !dotGit {
		return host.Message{"repo": dotGit, "git": hasGit}
	}
	r, err := openRepo(base, env, root)
	if err == nil {
		var result runResult
		result, err = run(r, []string{"rev-parse", "--show-toplevel"}, runOptions{})
		if err == nil {
			realRoot, resolveErr := filepath.EvalSymlinks(root)
			if resolveErr != nil {
				return host.Message{"repo": false, "git": true}
			}
			return host.Message{"repo": strings.TrimSuffix(string(result.stdout), "\n") == realRoot, "git": true}
		}
	}
	if errors.Is(err, errAbort) {
		return nil
	}
	return host.Message{"repo": false, "git": true}
}

// detectRemote probes the remote in one round trip. A host that can't be
// reached answers with the failure instead of repo and git, so the app can
// keep what it knew.
func detectRemote(base context.Context, folder *remoteFolder, trusted bool) host.Message {
	root := folder.path
	quotedRoot, err := shellQuote(root)
	if err != nil {
		return gitFailure(err)
	}
	script := strings.Join([]string{
		`command -v git >/dev/null 2>&1 && g=1 || g=0`,
		`r=` + quotedRoot,
		`if [ ! -L "$r/.git" ] && { [ -d "$r/.git" ] || [ -f "$r/.git" ]; }; then d=1; else d=0; fi`,
		`printf '%s %s ' "$g" "$d"`,
		`cd -P -- "$r" 2>/dev/null && printf '%s' "$PWD"`,
		`exit 0`,
	}, "\n")
	result, err := runScript(folder.remote, script, sshRunOptions{ctx: base, idle: readIdleMs()})
	if err != nil {
		if base.Err() != nil {
			return nil
		}
		return gitFailure(asRemoteError(folder.remote, err))
	}
	probe := string(result.stdout)
	hasGit := strings.HasPrefix(probe, "1")
	dotGit := len(probe) > 2 && probe[2:3] == "1"
	realRoot := ""
	if len(probe) > 4 {
		realRoot = probe[4:]
	}
	if !trusted || !hasGit || !dotGit {
		return host.Message{"repo": dotGit, "git": hasGit}
	}
	r := &repo{base: base, root: root, process: remoteGit{folder: folder}, remote: folder}
	checked, err := run(r, []string{"rev-parse", "--show-toplevel"}, runOptions{})
	if err != nil {
		if errors.Is(err, errAbort) {
			return nil
		}
		if _, ok := err.(*host.Error); ok {
			return gitFailure(err)
		}
		return host.Message{"repo": false, "git": true}
	}
	return host.Message{"repo": strings.TrimSuffix(string(checked.stdout), "\n") == realRoot, "git": true}
}

func changes(base context.Context, env host.Env, root, mode string, ignoreWhitespace bool, known any) host.Message {
	r, failure, err := guarded(base, env, root)
	if failure != nil {
		return failure
	}
	if err != nil {
		if errors.Is(err, errAbort) {
			return nil
		}
		return gitFailure(err)
	}
	branch, err := currentBranch(r)
	if err != nil {
		if errors.Is(err, errAbort) {
			return nil
		}
		return gitFailure(err)
	}
	baseRef, err := defaultBranch(r)
	if err != nil {
		if errors.Is(err, errAbort) {
			return nil
		}
		return gitFailure(err)
	}
	var diff diffBody
	if mode == "base" {
		diff, err = branchDiff(r, baseRef, ignoreWhitespace)
	} else {
		var paths gitPaths
		paths, err = requireRepo(r)
		if err == nil {
			diff, err = workTreeDiff(r, paths, ignoreWhitespace)
		}
	}
	if err != nil {
		if errors.Is(err, errAbort) {
			return nil
		}
		return gitFailure(err)
	}
	var branchValue, baseValue any
	if branch != "" {
		branchValue = branch
	}
	if baseRef != "" {
		baseValue = baseRef
	}
	body := host.Message{"branch": branchValue, "baseRef": baseValue}
	if diff.tooLarge {
		body["tooLarge"] = true
		body["files"] = []host.Message{}
	} else {
		body["files"] = diff.files
	}
	if data, _ := json.Marshal(body); len(data) > replyLimit {
		body = host.Message{"branch": branchValue, "baseRef": baseValue, "tooLarge": true, "files": []host.Message{}}
	}
	return fingerprinted(body, known)
}

func commits(base context.Context, env host.Env, root string, known any) host.Message {
	r, failure, err := guarded(base, env, root)
	if failure != nil {
		return failure
	}
	if err != nil {
		if errors.Is(err, errAbort) {
			return nil
		}
		return gitFailure(err)
	}
	branch, branchErr := currentBranch(r)
	head, headErr := headCommit(r)
	baseRef, baseErr := defaultBranch(r)
	for _, e := range []error{branchErr, headErr, baseErr} {
		if errors.Is(e, errAbort) {
			return nil
		}
		if e != nil {
			return gitFailure(e)
		}
	}
	comparison := ""
	if branch != "" && head != "" && baseRef != "" {
		comparison, err = comparisonRef(r, baseRef)
		if err != nil {
			if errors.Is(err, errAbort) {
				return nil
			}
			return gitFailure(err)
		}
	}
	if comparison == "" {
		return fingerprinted(host.Message{"baseRef": nil, "total": 0, "commits": []host.Message{}}, known)
	}
	rangeRef := comparison + "..HEAD"
	log, logErr := run(r, []string{"log", "--no-color", "--no-show-signature", "-z", "--max-count=" + strconv.Itoa(commitsLimit), "--format=%H%x00%at%x00%s", rangeRef, "--"}, runOptions{})
	count, countErr := run(r, []string{"rev-list", "--count", rangeRef, "--"}, runOptions{})
	for _, e := range []error{logErr, countErr} {
		if errors.Is(e, errAbort) {
			return nil
		}
		if e != nil {
			return gitFailure(e)
		}
	}
	fields := strings.Split(string(log.stdout), "\x00")
	list := make([]host.Message, 0, len(fields)/3)
	for i := 0; i+2 < len(fields); i += 3 {
		when, _ := strconv.ParseInt(fields[i+1], 10, 64)
		list = append(list, host.Message{"sha": fields[i], "subject": fields[i+2], "authorTime": when})
	}
	total, _ := strconv.Atoi(strings.TrimSpace(string(count.stdout)))
	return fingerprinted(host.Message{"baseRef": comparison, "total": total, "commits": list}, known)
}

func commitDiff(base context.Context, env host.Env, root, sha string, ignoreWhitespace bool) host.Message {
	r, failure, err := guarded(base, env, root)
	if failure != nil {
		return failure
	}
	if err != nil {
		if errors.Is(err, errAbort) {
			return nil
		}
		return gitFailure(err)
	}
	notFound := &gitError{code: "commit_not_found", message: "Commit " + sha + " not found."}
	if !shaPattern.MatchString(sha) {
		return gitFailure(notFound)
	}
	commit, err := run(r, []string{"rev-parse", "--verify", "--quiet", sha + "^{commit}"}, runOptions{anyExit: true})
	if errors.Is(err, errAbort) {
		return nil
	}
	if err != nil || commit.exitCode != 0 {
		return gitFailure(notFound)
	}
	full := strings.TrimSpace(string(commit.stdout))
	parent, err := run(r, []string{"rev-parse", "--verify", "--quiet", full + "^1"}, runOptions{anyExit: true})
	if errors.Is(err, errAbort) {
		return nil
	}
	from := ""
	if err == nil && parent.exitCode == 0 {
		from = strings.TrimSpace(string(parent.stdout))
	} else {
		from, err = emptyTree(r)
		if errors.Is(err, errAbort) {
			return nil
		}
		if err != nil {
			return gitFailure(err)
		}
	}
	diff, err := diffFiles(r, []string{from, full}, ignoreWhitespace, nil)
	if errors.Is(err, errAbort) {
		return nil
	}
	if err != nil {
		return gitFailure(err)
	}
	if diff.tooLarge {
		return host.Message{"tooLarge": true, "files": []host.Message{}}
	}
	return host.Message{"files": diff.files}
}
