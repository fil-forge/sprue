package ucan_server

import (
	"bytes"
	"context"
	"fmt"

	"github.com/fil-forge/sprue/pkg/lib/zapipld"
	"github.com/fil-forge/sprue/pkg/store/agent"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"go.uber.org/zap"
)

type ErrorHandler struct {
	Logger *zap.Logger
}

var _ server.ResponseEncodeListener = (*ErrorHandler)(nil)

func (l *ErrorHandler) OnRequestDecode(ctx context.Context, container ucan.Container) error {
	return nil
}

func (l *ErrorHandler) OnResponseEncode(ctx context.Context, ct ucan.Container) error {
	for _, inv := range ct.Invocations() {
		r, ok := ct.Receipt(inv.Task().Link())
		if !ok || !r.Out().IsErr() {
			continue
		}
		_, x := r.Out().Unpack()
		var model datamodel.Map
		if err := model.UnmarshalCBOR(bytes.NewReader(x)); err != nil {
			l.Logger.Error("failed to unmarshal handler execution error", zap.Error(err), zap.Binary("input", x))
			continue
		}
		if model["name"].(string) != execution.HandlerExecutionErrorName {
			continue
		}
		l.Logger.Error(
			"handler execution error",
			zap.Stringer("task", inv.Task().Link()),
			zap.Stringer("command", inv.Command()),
			zap.Any("arguments", zapipld.RawMap(inv.ArgumentsBytes())),
			zap.Any("error", model),
		)
	}
	return nil
}

// AgentMessageLogger stores every request and response the server handles as
// an agent message. Register it with [server.WithConcurrentEventListener]:
// OnRequestDecode then runs alongside the handlers rather than ahead of them,
// which is safe because no handler reads its own request back from the store,
// and the server waits for the write before encoding the response, so the
// message is in place by the time a client can ask for it.
//
// The two writes fail differently. The incoming message is a record nobody
// reads on a request path, so a failed write is logged and the request goes
// on: failing it would reach the client only after the handlers had run, and
// the retry that provokes does more harm than the missing record. The
// outgoing message carries the receipts clients fetch later, so its failure
// fails the request.
type AgentMessageLogger struct {
	Logger     *zap.Logger
	AgentStore agent.Store
}

var _ server.RequestDecodeListener = (*AgentMessageLogger)(nil)
var _ server.ResponseEncodeListener = (*AgentMessageLogger)(nil)

func (r *AgentMessageLogger) OnRequestDecode(ctx context.Context, msg ucan.Container) error {
	err := r.AgentStore.Write(ctx, msg, agent.Index(msg))
	if err != nil {
		tasks := make([]string, 0, len(msg.Invocations()))
		for _, inv := range msg.Invocations() {
			tasks = append(tasks, inv.Command().String()+" "+inv.Task().Link().String())
		}
		r.Logger.Error("failed to write incoming agent message to store", zap.Error(err), zap.Strings("tasks", tasks))
	}
	return nil
}

func (r *AgentMessageLogger) OnResponseEncode(ctx context.Context, msg ucan.Container) error {
	err := r.AgentStore.Write(ctx, msg, agent.Index(msg))
	if err != nil {
		r.Logger.Error("failed to write outgoing agent message to store", zap.Error(err))
		return fmt.Errorf("writing outgoing agent message to agent store: %w", err)
	}
	return nil
}
