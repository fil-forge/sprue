package handlers

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	captypes "github.com/fil-forge/go-libstoracha/capabilities/types"
	ucancap "github.com/fil-forge/go-libstoracha/capabilities/ucan"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/receipt"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/internal/ipldutil"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/fil-forge/sprue/pkg/store/agent"
	"go.uber.org/zap"
)

const InvalidInvocationErrorName = "InvalidInvocation"

type ConclusionHandlerFunc func(context.Context, invocation.Invocation, receipt.AnyReceipt, server.InvocationContext) error

// ConclusionHandler is the definition of a handler for an invocation conclusion
// - a receiver for a receipt attesting to an invocation result.
type ConclusionHandler struct {
	// Ability is the invoked ability this handler is expecting to receive
	// conclusions for.
	Ability ucan.Ability
	// Handler is the function that receives the conclusion for the invocation.
	Handler ConclusionHandlerFunc
}

// WithUCANConcludeMethod registers the ucan/conclude handler.
// This handler processes receipt conclusions from clients.
// When it receives an http/put receipt, it calls blob/accept on piri
// and stores the accept receipt for later retrieval.
func WithUCANConcludeMethod(id *identity.Identity, agentStore agent.Store, handlers map[ucan.Ability]ConclusionHandlerFunc, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		ucancap.ConcludeAbility,
		server.Provide(
			ucancap.Conclude,
			UCANConcludeHandler(id, agentStore, handlers, logger),
		),
	)
}

func UCANConcludeHandler(id *identity.Identity, agentStore agent.Store, handlers map[ucan.Ability]ConclusionHandlerFunc, logger *zap.Logger) server.HandlerFunc[ucancap.ConcludeCaveats, ucancap.ConcludeOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", ucancap.ConcludeAbility))
	log.Info("registered conclude handlers", zap.Strings("abilities", slices.Collect(maps.Keys(handlers))))
	return func(ctx context.Context,
		cap ucan.Capability[ucancap.ConcludeCaveats],
		inv invocation.Invocation,
		iCtx server.InvocationContext,
	) (result.Result[ucancap.ConcludeOk, failure.IPLDBuilderFailure], fx.Effects, error) {
		rcptRoot := cap.Nb().Receipt

		log := log.With(zap.Stringer("receipt", rcptRoot))

		log.Debug("concluding received receipt", zap.String("receipt", rcptRoot.String()))

		// Read the concluded receipt from the invocation's attached blocks
		anyReader := receipt.NewAnyReceiptReader(captypes.Converters...)
		rcpt, err := anyReader.Read(rcptRoot, inv.Blocks())
		if err != nil {
			log.Error("failed to read concluded receipt", zap.Error(err))
			return nil, nil, fmt.Errorf("reading receipt: %w", err)
		}

		task, err := ipldutil.ToCID(rcpt.Ran().Link())
		if err != nil {
			return nil, nil, err
		}

		// Get the invocation that the receipt is for
		ranInv, ok := rcpt.Ran().Invocation()
		if !ok {
			inv, err := agentStore.GetInvocation(ctx, task)
			if err != nil {
				// If can not find task for this receipt there is nothing to do here, if
				// it was a receipt for something we care about we would have an
				// invocation recorded.
				if errors.Is(err, agent.ErrInvocationNotFound) {
					return result.Ok[ucancap.ConcludeOk, failure.IPLDBuilderFailure](ucancap.ConcludeOk{Time: time.Now()}), nil, nil
				}
				log.Error("failed to get invocation from agent store", zap.Error(err))
				return nil, nil, fmt.Errorf("getting invocation: %w", err)
			}
			ranInv = inv
		}
		if len(ranInv.Capabilities()) == 0 {
			log.Warn("invocation has no capabilities")
			return nil, nil, errors.New(InvalidInvocationErrorName, "invocation has no capabilities")
		}

		ability := ranInv.Capabilities()[0].Can()
		log = log.With(
			zap.Stringer("ran", ranInv.Link()),
			zap.String("ability", ability),
		)
		log.Debug("found invocation for conclusion")

		if handler, ok := handlers[ability]; ok {
			err := handler(ctx, ranInv, rcpt, iCtx)
			if err != nil {
				log.Error("failed to conclude receipt", zap.Error(err))
				return nil, nil, fmt.Errorf("concluding %q: %w", ability, err)
			}
		}

		return result.Ok[ucancap.ConcludeOk, failure.IPLDBuilderFailure](
			ucancap.ConcludeOk{Time: time.Now()},
		), nil, nil
	}
}
