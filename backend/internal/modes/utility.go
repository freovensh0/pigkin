package modes

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// utility is the app's long-lived one-off request host: it sends its ready
// line once, then answers each request with the same result its own host would
// have sent, carrying the request's id.
func utility(ctx *Context) (host.Message, error) {
	ctx.Conn.Send(host.Message{"type": "pigkin_utility_ready"})

	var inFlight sync.WaitGroup
	ctx.Conn.Listen(func(message host.Message) {
		if message["type"] != "pigkin_utility_request" {
			return
		}
		id, ok := message["id"].(float64)
		if !ok {
			return
		}
		inFlight.Add(1)
		go func() {
			defer inFlight.Done()
			result := answerUtility(ctx, message["op"], message["params"])
			result["id"] = id
			ctx.Conn.Send(result)
		}()
	})

	// Returning exits the process, so a save in progress finishes its write.
	<-ctx.Conn.Done()
	inFlight.Wait()
	return host.Message{}, nil
}

type utilityOp func(*Context) (host.Message, error)

var utilityOps = map[string]utilityOp{
	"sessions":               sessionsOp,
	"session_messages":       sessionMessagesOp,
	"delete_sessions":        deleteSessionsOp,
	"save_attachments":       saveAttachmentsOp,
	"backups":                listBackupsOp,
	"backup_delete":          backupDeleteOp,
	"logout":                 logoutOp,
	"auth_status":            authStatusOp,
	"provider_models":        providerModelsOp,
	"provider_save":          providerSaveOp,
	"provider_remove":        providerRemoveOp,
	"model_defaults":         modelDefaultsOp,
	"default_model":          defaultModelOp,
	"subscription_models":    subscriptionModelsOp,
	"subscription_save":      subscriptionSaveOp,
	"local_models_sync":      localModelsSyncOp,
	"remote_hosts":           remoteHostsOp,
	"remote_host_save":       remoteHostSaveOp,
	"remote_host_remove":     remoteHostRemoveOp,
	"remote_key":             remoteKeyOp,
	"remote_key_new":         remoteKeyNewOp,
	"remote_status":          remoteStatusOp,
	"remote_disconnect":      remoteDisconnectOp,
	"remote_forget_host_key": remoteForgetHostKeyOp,
	"remote_workspace":       remoteWorkspaceOp,
	"scheduled_task_run":     scheduledTaskRun,
}

func answerUtility(ctx *Context, op, params any) host.Message {
	name, ok := op.(string)
	run := utilityOps[name]
	if !ok || run == nil {
		return host.Message{
			"type":  "pigkin_result",
			"ok":    false,
			"error": "Unknown operation " + jsString(op),
			"code":  "unknown_op",
		}
	}
	values, ok := params.(map[string]any)
	if !ok {
		return host.Message{"type": "pigkin_result", "ok": false, "error": "Expected params", "code": "bad_request"}
	}
	withParams := *ctx
	withParams.Params = values
	data, err := run(&withParams)
	if err != nil {
		return Failure(err)
	}
	return host.Message{"type": "pigkin_result", "ok": true, "data": data}
}

// jsString is String(op) for the error text an unknown operation produces.
func jsString(value any) string {
	switch v := value.(type) {
	case nil:
		return "undefined"
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}
