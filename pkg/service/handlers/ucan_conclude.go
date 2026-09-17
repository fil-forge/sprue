package handlers

import (
	"context"
	"fmt"
	"maps"
	"slices"

	ucancmds "github.com/fil-forge/libforge/commands/ucan"
	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/pkg/store/agent"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/ipfs/go-cid"
	"go.uber.org/zap"
)

// Conclusion is one delivered receipt together with the invocation it ran.
type Conclusion struct {
	Invocation ucan.Invocation
	Receipt    ucan.Receipt
}

// ConclusionHandlerFunc receives every conclusion of one command delivered by
// a single /ucan/conclude invocation. It returns a container of the artifacts
// the conclusions produced — the receipts of any tasks it ran, and the
// invocations they reference — which travel back in the conclude response so
// the deliverer learns each outcome without polling for it. A task that
// failed is reported by its own failure receipt in that container, not by an
// error; an error means the conclusion itself could not be processed.
type ConclusionHandlerFunc func(context.Context, []Conclusion) (ucan.Container, error)

// ConclusionHandler is the definition of a handler for an invocation conclusion
// - a receiver for a receipt attesting to an invocation result.
type ConclusionHandler struct {
	// Command is the invoked command this handler is expecting to receive
	// conclusions for.
	Command ucan.Command
	// Handler is the function that receives the conclusions for the invocation.
	Handler ConclusionHandlerFunc
}

// NewUCANConcludeHandler creates a handler for /ucan/conclude invocations.
// This handler processes receipt conclusions from clients.
// When it receives /http/put receipts, it calls /blob/accept on piri
// and stores the accept receipts for later retrieval.
//
// One invocation may deliver many receipts: they are grouped by the command
// they ran and each group is handed to its handler in one call, so a client
// with a receipt per blob pays one round trip instead of one per blob.
func NewUCANConcludeHandler(id identity.Identity, agentStore agent.Store, handlers map[ucan.Command]ConclusionHandlerFunc, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", ucancmds.Conclude))
	log.Info("registered conclude handlers", zap.Stringers("commands", slices.Collect(maps.Keys(handlers))))
	return ucancmds.Conclude.Route(
		func(req *binding.Request[*ucancmds.ConcludeArguments], res *binding.Response[*ucancmds.ConcludeOK]) error {
			rcptRoots := req.Task().Arguments().Receipts
			log := log.With(zap.Int("receipts", len(rcptRoots)))
			log.Debug("concluding received receipts")

			// Index the request container once: matching each delivered
			// receipt to its artifacts by scanning would be quadratic in the
			// number of receipts.
			rcptsByLink := map[cid.Cid]ucan.Receipt{}
			invsByTask := map[cid.Cid]ucan.Invocation{}
			if req.Metadata() != nil {
				for _, r := range req.Metadata().Receipts() {
					rcptsByLink[r.Link()] = r
				}
				for _, inv := range req.Metadata().Invocations() {
					invsByTask[inv.Task().Link()] = inv
				}
			}

			// Group by ran command, preserving delivery order within a group.
			var commands []ucan.Command
			byCommand := map[ucan.Command][]Conclusion{}
			for _, rcptRoot := range rcptRoots {
				log := log.With(zap.Stringer("receipt", rcptRoot))
				rcpt, ok := rcptsByLink[rcptRoot]
				if !ok {
					log.Warn("receipt not found in invocation metadata")
					return res.SetFailure(ucancmds.ErrConclusionReceiptNotFound)
				}
				log = log.With(zap.Stringer("task", rcpt.Ran()))

				ranInv, ok := invsByTask[rcpt.Ran()]
				if !ok {
					// Not delivered alongside the receipt, so look it up.
					inv, err := agentStore.GetInvocation(req.Context(), rcpt.Ran())
					if err != nil {
						// If we cannot find an invocation for this receipt
						// there is nothing to do: had it been for something we
						// care about, we would have recorded the invocation.
						if errors.Is(err, agent.ErrInvocationNotFound) {
							continue
						}
						log.Error("failed to get invocation from agent store", zap.Error(err))
						return fmt.Errorf("getting invocation: %w", err)
					}
					ranInv = inv
				}

				cmd := ranInv.Command()
				if _, seen := byCommand[cmd]; !seen {
					commands = append(commands, cmd)
				}
				byCommand[cmd] = append(byCommand[cmd], Conclusion{Invocation: ranInv, Receipt: rcpt})
				log.Debug("found invocation for conclusion", zap.Stringer("command", cmd))
			}

			var invocations []ucan.Invocation
			var receipts []ucan.Receipt
			var delegations []ucan.Delegation
			for _, cmd := range commands {
				handler, ok := handlers[cmd]
				if !ok {
					continue
				}
				meta, err := handler(req.Context(), byCommand[cmd])
				if meta != nil {
					invocations = append(invocations, meta.Invocations()...)
					receipts = append(receipts, meta.Receipts()...)
					delegations = append(delegations, meta.Delegations()...)
				}
				if err != nil {
					log.Error("failed to conclude invocations", zap.Stringer("command", cmd), zap.Error(err))
					return fmt.Errorf("concluding %q: %w", cmd, err)
				}
			}

			if len(invocations) > 0 || len(receipts) > 0 || len(delegations) > 0 {
				if err := res.SetMetadata(container.New(
					container.WithInvocations(invocations...),
					container.WithReceipts(receipts...),
					container.WithDelegations(delegations...),
				)); err != nil {
					return fmt.Errorf("setting response metadata: %w", err)
				}
			}
			return res.SetSuccess(&ucancmds.ConcludeOK{})
		},
	)
}
