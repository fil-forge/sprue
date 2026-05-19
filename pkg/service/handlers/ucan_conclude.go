package handlers

import (
	"context"
	"fmt"
	"maps"
	"slices"

	ucancaps "github.com/fil-forge/libforge/commands/ucan"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/store/agent"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/execution/bindexec"
	"github.com/fil-forge/ucantone/ucan"
	"go.uber.org/zap"
)

type ConclusionHandlerFunc func(context.Context, ucan.Invocation, ucan.Receipt, ucan.Container) error

// ConclusionHandler is the definition of a handler for an invocation conclusion
// - a receiver for a receipt attesting to an invocation result.
type ConclusionHandler struct {
	// Command is the invoked command this handler is expecting to receive
	// conclusions for.
	Command ucan.Command
	// Handler is the function that receives the conclusion for the invocation.
	Handler ConclusionHandlerFunc
}

// NewUCANConcludeHandler creates a handler for /ucan/conclude invocations.
// This handler processes receipt conclusions from clients.
// When it receives an /http/put receipt, it calls /blob/accept on piri
// and stores the accept receipt for later retrieval.
func NewUCANConcludeHandler(id *identity.Identity, agentStore agent.Store, handlers map[ucan.Command]ConclusionHandlerFunc, logger *zap.Logger) Handler {
	log := logger.With(zap.String("handler", string(ucancaps.Conclude)))
	log.Info("registered conclude handlers", zap.Stringers("commands", slices.Collect(maps.Keys(handlers))))
	return Handler{
		Command: ucan.Command(ucancaps.Conclude),
		Handler: bindexec.NewHandler(func(
			req *bindexec.Request[*ucancaps.ConcludeArguments],
			res *bindexec.Response[*ucancaps.ConcludeOK],
		) error {
			args := req.Task().Arguments()
			rcptRoot := args.Receipt

			log := log.With(zap.Stringer("receipt", rcptRoot))

			log.Debug("concluding received receipt", zap.Stringer("receipt", rcptRoot))

			var rcpt ucan.Receipt
			if req.Metadata() != nil {
				for _, r := range req.Metadata().Receipts() {
					if r.Link() == rcptRoot {
						rcpt = r
					}
				}
			}
			if rcpt == nil {
				log.Warn("receipt not found in invocation metadata")
				return res.SetFailure(ucancaps.ErrConclusionReceiptNotFound)
			}
			log = log.With(zap.Stringer("task", rcpt.Ran()))

			var ranInv ucan.Invocation
			// check if the invocation was included in the invocation metadata
			for _, inv := range req.Metadata().Invocations() {
				if inv.Task().Link() == rcpt.Ran() {
					ranInv = inv
				}
			}
			// if not included in invocation, check our store
			if ranInv == nil {
				inv, err := agentStore.GetInvocation(req.Context(), rcpt.Ran())
				if err != nil {
					// If can not find invocation for this receipt there is nothing to do
					// here, if it was a receipt for something we care about we would have
					// an invocation recorded.
					if errors.Is(err, agent.ErrInvocationNotFound) {
						return res.SetSuccess(&ucancaps.ConcludeOK{})
					}
					log.Error("failed to get invocation from agent store", zap.Error(err))
					return fmt.Errorf("getting invocation: %w", err)
				}
				ranInv = inv
			}

			log = log.With(zap.Stringer("command", ranInv.Command()))
			log.Debug("found invocation for conclusion")

			if handler, ok := handlers[ranInv.Command()]; ok {
				err := handler(req.Context(), ranInv, rcpt, req.Metadata())
				if err != nil {
					log.Error("failed to conclude invocation", zap.Error(err))
					return fmt.Errorf("concluding %q: %w", ranInv.Command(), err)
				}
			}

			return res.SetSuccess(&ucancaps.ConcludeOK{})
		}),
	}
}
