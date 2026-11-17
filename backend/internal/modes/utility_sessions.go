package modes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

const firstMessageChars = 200

type sessionEntry struct {
	id        string
	parent    string
	hasParent bool
	message   json.RawMessage
}

type sessionSummary struct {
	path         string
	id           string
	cwd          string
	name         string
	created      time.Time
	modified     time.Time
	messageCount int
	firstMessage string
	messages     []json.RawMessage
}

// loadSession reads the header, summary and current branch of one session
// JSONL, the way Pi's buildSessionInfo and buildSessionContext do.
func loadSession(path string) (sessionSummary, error) {
	file, err := os.Open(path)
	if err != nil {
		return sessionSummary{}, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return sessionSummary{}, err
	}
	result := sessionSummary{path: path}
	var entries []sessionEntry
	byID := map[string]sessionEntry{}
	texts := []string{}
	latest := float64(0)
	haveLatest := false
	seenHeader := false
	scanner := host.ScanLines(file)
	for scanner.Scan() {
		var entry map[string]any
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		kind, _ := entry["type"].(string)
		if !seenHeader {
			if kind != "session" {
				return sessionSummary{}, errInvalidSession
			}
			seenHeader = true
			result.id, _ = entry["id"].(string)
			result.cwd, _ = entry["cwd"].(string)
			result.created = entryTime(entry["timestamp"])
			continue
		}
		switch kind {
		case "session_info":
			if name, ok := entry["name"].(string); ok {
				result.name = strings.TrimSpace(name)
			}
		case "message":
			message, ok := entry["message"].(map[string]any)
			if !ok {
				return sessionSummary{}, errInvalidSession
			}
			result.messageCount++
			stamp, hasStamp := entryTimeValue(entry["timestamp"])
			if value, ok := message["timestamp"].(float64); ok {
				stamp, hasStamp = value, true
			}
			if hasStamp && (!haveLatest || stamp > latest) {
				latest, haveLatest = stamp, true
			}
			item := sessionEntry{}
			item.id, _ = entry["id"].(string)
			item.parent, item.hasParent = entry["parentId"].(string)
			if item.id == "" {
				item.hasParent = false
			}
			item.message = json.RawMessage(nil)
			if raw, err := json.Marshal(message); err == nil {
				item.message = raw
			}
			if item.id != "" {
				byID[item.id] = item
			}
			entries = append(entries, item)
			role, _ := message["role"].(string)
			if role == "user" || role == "assistant" {
				if text, ok := messageText(message["content"]); ok && text != "" {
					texts = append(texts, text)
					if role == "user" && result.firstMessage == "" {
						result.firstMessage = text
					}
				}
			}
		}
	}
	if !seenHeader {
		return sessionSummary{}, errInvalidSession
	}
	if result.firstMessage == "" {
		result.firstMessage = "(no messages)"
	}
	result.modified = info.ModTime()
	switch {
	case haveLatest && latest > 0:
		result.modified = time.UnixMilli(int64(latest))
	case !result.created.IsZero():
		result.modified = result.created
	}
	result.messages = branchMessages(entries, byID)
	return result, nil
}

var errInvalidSession = os.ErrInvalid

// branchMessages walks from the newest entry to the root, so messages on
// abandoned branches are left out.
func branchMessages(entries []sessionEntry, byID map[string]sessionEntry) []json.RawMessage {
	leaf := ""
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].id != "" {
			leaf = entries[i].id
			break
		}
	}
	messages := []json.RawMessage{}
	if _, ok := byID[leaf]; !ok {
		for _, entry := range entries {
			if entry.message != nil {
				messages = append(messages, entry.message)
			}
		}
		return messages
	}
	var path []json.RawMessage
	for id := leaf; id != ""; {
		entry, ok := byID[id]
		if !ok {
			break
		}
		if entry.message != nil {
			path = append(path, entry.message)
		}
		if !entry.hasParent {
			break
		}
		id = entry.parent
	}
	for i := len(path) - 1; i >= 0; i-- {
		messages = append(messages, path[i])
	}
	return messages
}

func messageText(content any) (string, bool) {
	switch value := content.(type) {
	case string:
		return value, true
	case []any:
		texts := make([]string, 0, len(value))
		for _, block := range value {
			object, ok := block.(map[string]any)
			if !ok {
				return "", false
			}
			if object["type"] == "text" {
				text, _ := object["text"].(string)
				texts = append(texts, text)
			}
		}
		return strings.Join(texts, " "), true
	}
	return "", false
}

func entryTime(value any) time.Time {
	stamp, ok := entryTimeValue(value)
	if !ok {
		return time.Time{}
	}
	return time.UnixMilli(int64(stamp))
}

func entryTimeValue(value any) (float64, bool) {
	switch v := value.(type) {
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if parsed, err := time.Parse(layout, v); err == nil {
				return float64(parsed.UnixMilli()), true
			}
		}
	case float64:
		return v, true
	}
	return 0, false
}

// sessionsOp lists every session, newest first, with its workspace and
// subagent state.
func sessionsOp(ctx *Context) (host.Message, error) {
	root := filepath.Join(ctx.Env.ConfigDir, "sessions")
	folders, err := os.ReadDir(root)
	if err != nil {
		return host.Message{"sessions": []host.Message{}}, nil
	}
	var all []sessionSummary
	for _, folder := range folders {
		if !folder.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, folder.Name()))
		if err != nil {
			continue
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".jsonl") {
				continue
			}
			summary, err := loadSession(filepath.Join(root, folder.Name(), file.Name()))
			if err == nil {
				all = append(all, summary)
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].modified.After(all[j].modified)
	})

	paths := remotePathsFor(ctx.Env)
	cwds := map[string]bool{}
	remotes := map[string]host.Message{}
	for _, session := range all {
		if _, ok := cwds[session.cwd]; ok {
			continue
		}
		cwds[session.cwd] = isDirectory(session.cwd)
		standIn, err := readStandIn(paths.remotes, session.cwd)
		if err != nil {
			return nil, err
		}
		if standIn != nil {
			remotes[session.cwd] = host.Message{"remote": standIn}
		}
	}

	listed := make([]host.Message, 0, len(all))
	for _, session := range all {
		first := []rune(session.firstMessage)
		if len(first) > firstMessageChars {
			first = first[:firstMessageChars]
		}
		item := host.Message{
			"path":         session.path,
			"id":           session.id,
			"cwd":          session.cwd,
			"cwdExists":    cwds[session.cwd],
			"name":         session.name,
			"created":      session.created.UnixMilli(),
			"modified":     session.modified.UnixMilli(),
			"messageCount": session.messageCount,
			"firstMessage": string(first),
			"subagents":    countSubagents(session.path, session.id),
		}
		for key, value := range remotes[session.cwd] {
			item[key] = value
		}
		listed = append(listed, item)
	}
	return host.Message{"sessions": listed}, nil
}

func countSubagents(path, id string) int {
	folder := subagentFolder(path, id)
	if folder == "" {
		return 0
	}
	files, err := os.ReadDir(folder)
	if err != nil {
		return 0
	}
	count := 0
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".jsonl") {
			count++
		}
	}
	return count
}

// sessionMessagesOp reads a session's current messages without starting Pi.
func sessionMessagesOp(ctx *Context) (host.Message, error) {
	path, err := stringParam(ctx.Params, "path")
	if err != nil {
		return nil, err
	}
	file, err := sessionFile(ctx.Env.ConfigDir, path)
	if err != nil {
		return nil, err
	}
	summary, err := loadSession(file)
	if err != nil {
		return nil, host.NewError("failed", "Not a valid Pi session: "+path, nil)
	}
	return host.Message{"messages": summary.messages}, nil
}
