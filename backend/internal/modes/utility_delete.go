package modes

import (
	"errors"
	"io/fs"
	"os"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// deleteSessionsOp deletes session files and their subagent transcripts. Every
// path is checked before anything is deleted, so a stray path deletes nothing.
func deleteSessionsOp(ctx *Context) (host.Message, error) {
	paths, err := stringArrayParam(ctx.Params, "paths")
	if err != nil {
		return nil, err
	}
	type target struct {
		file   string
		folder string
	}
	targets := make([]target, 0, len(paths))
	for _, path := range paths {
		file, err := sessionFile(ctx.Env.ConfigDir, path)
		if err != nil {
			if hostErr, ok := err.(*host.Error); ok && hostErr.Code == "not_found" {
				continue
			}
			return nil, err
		}
		var folder string
		if subagents := subagentFolder(file, sessionID(file)); subagents != "" {
			folder, err = containedFolder(ctx.Env.ConfigDir, subagents)
			if err != nil {
				return nil, err
			}
		}
		targets = append(targets, target{file: file, folder: folder})
	}
	for _, item := range targets {
		if err := os.Remove(item.file); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if item.folder != "" {
			if err := os.RemoveAll(item.folder); err != nil {
				return nil, err
			}
		}
	}
	return host.Message{}, nil
}
