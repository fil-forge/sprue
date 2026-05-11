package ucan_server

import (
	"context"
	"fmt"

	"github.com/fil-forge/sprue/pkg/store/agent"
	edm "github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/result"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"go.uber.org/zap"
)

type ErrorHandler struct {
	Logger *zap.Logger
}

var _ server.ResponseEncodeListener = (*ErrorHandler)(nil)

func (l ErrorHandler) OnResponseEncode(ctx context.Context, ct ucan.Container) error {
	for _, inv := range ct.Invocations() {
		if r, ok := ct.Receipt(inv.Task().Link()); ok {
			_, x := result.Unwrap(r.Out())
			if x != nil {
				var model edm.ErrorModel
				datamodel.Rebind(datamodel.NewAny(x), &model)
				if model.ErrorName == execution.HandlerExecutionErrorName {
					l.Logger.Error(
						"handler execution error",
						zap.Stringer("task", inv.Task().Link()),
						zap.Stringer("command", inv.Command()),
						zap.Any("args", inv.Arguments()),
						zap.Error(model),
					)
				}
			}
		}
	}
	return nil
}

type AgentMessageLogger struct {
	Logger     *zap.Logger
	AgentStore agent.Store
}

var _ server.RequestDecodeListener = (*AgentMessageLogger)(nil)
var _ server.ResponseEncodeListener = (*AgentMessageLogger)(nil)

func (r *AgentMessageLogger) OnRequestDecode(ctx context.Context, msg ucan.Container) error {
	err := r.AgentStore.Write(ctx, msg, agent.Index(msg))
	if err != nil {
		r.Logger.Error("failed to write incoming agent message to store", zap.Error(err))
		return fmt.Errorf("writing incoming agent message to agent store: %w", err)
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
