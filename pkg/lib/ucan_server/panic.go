package ucan_server

import (
	"github.com/fil-forge/sprue/pkg/lib/zapipld"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/execution/dispatcher"
	"go.uber.org/zap"
)

// NewPanicLogger returns a [dispatcher.PanicLogger] that records panics
// recovered while executing an invocation. The dispatcher calls it on the
// panicking goroutine, so the captured stack trace shows where the panic
// happened.
func NewPanicLogger(logger *zap.Logger) dispatcher.PanicLogger {
	return func(req execution.Request, value any) {
		inv := req.Invocation()
		logger.Error(
			"panic executing invocation",
			zap.Stringer("task", inv.Task().Link()),
			zap.Stringer("command", inv.Command()),
			zap.Any("arguments", zapipld.RawMap(inv.ArgumentsBytes())),
			zap.Any("panic", value),
			zap.Stack("stack"),
		)
	}
}
