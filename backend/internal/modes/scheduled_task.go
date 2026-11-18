package modes

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// scheduledTaskTimeout bounds a one-off prompt so a stuck Pi can't hold the utility host.
const scheduledTaskTimeout = 5 * time.Minute

// scheduledTaskPrompt is the prompt to run, or a bad_request refusal when it is missing or blank.
func scheduledTaskPrompt(params host.Message) (string, *host.Error) {
	prompt, _ := params["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		return "", host.NewError("bad_request", "A prompt is required", &host.Text{Key: "scheduled_task_prompt_required"})
	}
	return prompt, nil
}

// scheduledTaskResult is the one-off run's answer: Pi's final stdout without its trailing whitespace.
func scheduledTaskResult(stdout string) host.Message {
	return host.Message{"result": strings.TrimSpace(stdout)}
}

// scheduledTaskArgs builds the Pi --print arguments, adding --model and --thinking only for a non-blank value.
func scheduledTaskArgs(entry, prompt, model, thinking string) []string {
	args := []string{entry}
	if model = strings.TrimSpace(model); model != "" {
		args = append(args, "--model", model)
	}
	if thinking = strings.TrimSpace(thinking); thinking != "" {
		args = append(args, "--thinking", thinking)
	}
	return append(args, "--print", prompt)
}

// scheduledTaskRun runs a prompt once with the bundled Pi, mirroring host/src/modes/scheduled_task.ts.
func scheduledTaskRun(ctx *Context) (host.Message, error) {
	prompt, promptErr := scheduledTaskPrompt(ctx.Params)
	if promptErr != nil {
		return nil, promptErr
	}
	entry, err := piEntry(ctx.Env.Root)
	if err != nil {
		return nil, host.NewError("pi_version", "The agent's install is incomplete", &host.Text{Key: "agent_install_incomplete"})
	}
	node, err := piNode(ctx.Env)
	if err != nil {
		return nil, err
	}
	base, cancel := context.WithTimeout(connContext(ctx.Conn), scheduledTaskTimeout)
	defer cancel()
	model, _ := ctx.Params["model"].(string)
	thinking, _ := ctx.Params["thinking"].(string)
	cmd := exec.CommandContext(base, node, scheduledTaskArgs(entry, prompt, model, thinking)...)
	cmd.Dir = ctx.Env.Home
	cmd.Env = append(os.Environ(),
		"PIGKIN_ROOT="+ctx.Env.Root,
		"PI_CODING_AGENT_DIR="+ctx.Env.ConfigDir,
		"HOME="+ctx.Env.Home,
		"PREFIX="+ctx.Env.Prefix,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if errors.Is(base.Err(), context.DeadlineExceeded) {
		return nil, host.NewError("scheduled_task_failed", "The task took longer than five minutes", &host.Text{Key: "scheduled_task_timed_out"})
	}
	if base.Err() != nil {
		return nil, commandFailed("scheduled_task_failed", "pi --print", nil)
	}
	if runErr != nil {
		return nil, commandFailed("scheduled_task_failed", "pi --print", exitCode(runErr))
	}
	return scheduledTaskResult(stdout.String()), nil
}
