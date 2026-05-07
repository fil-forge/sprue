package handlers_test

import (
	"context"
	"testing"

	ucancap "github.com/fil-forge/go-libstoracha/capabilities/ucan"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/receipt"
	"github.com/fil-forge/go-ucanto/core/receipt/ran"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/ok"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	agent_store "github.com/fil-forge/sprue/pkg/store/agent/memory"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// issueConclude creates a ucan/conclude invocation with the receipt blocks attached.
func issueConclude(t *testing.T, signer ucan.Signer, rcpt receipt.AnyReceipt) invocation.Invocation {
	t.Helper()
	inv, err := ucancap.Conclude.Invoke(
		signer, signer,
		signer.DID().String(),
		ucancap.ConcludeCaveats{
			Receipt: rcpt.Root().Link(),
		},
	)
	require.NoError(t, err)

	for blk, err := range rcpt.Blocks() {
		require.NoError(t, err)
		require.NoError(t, inv.Attach(blk))
	}
	return inv
}

func TestUCANConcludeHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	t.Run("receipt not readable from blocks", func(t *testing.T) {
		agentStore := agent_store.New()
		handlerMap := map[ucan.Ability]handlers.ConclusionHandlerFunc{}

		handler := handlers.UCANConcludeHandler(
			&identity.Identity{Signer: uploadService}, agentStore, handlerMap, logger,
		)

		// Create a conclude invocation WITHOUT attaching the receipt blocks
		taskInv, err := invocation.Invoke(
			uploadService, uploadService,
			ucan.NewCapability("test/thing", uploadService.DID().String(), ucan.NoCaveats{}),
		)
		require.NoError(t, err)

		rcpt, err := receipt.Issue(
			uploadService,
			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
			ran.FromInvocation(taskInv),
		)
		require.NoError(t, err)

		// Invoke conclude but don't attach receipt blocks
		concludeInv, err := ucancap.Conclude.Invoke(
			uploadService, uploadService,
			uploadService.DID().String(),
			ucancap.ConcludeCaveats{
				Receipt: rcpt.Root().Link(),
			},
		)
		require.NoError(t, err)

		concludeCap := ucancap.Conclude.New(
			uploadService.DID().String(),
			ucancap.ConcludeCaveats{Receipt: rcpt.Root().Link()},
		)
		_, _, err = handler(ctx, concludeCap, concludeInv, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "reading receipt")
	})

	t.Run("unknown invocation returns success", func(t *testing.T) {
		agentStore := agent_store.New()
		handlerMap := map[ucan.Ability]handlers.ConclusionHandlerFunc{}

		handler := handlers.UCANConcludeHandler(
			&identity.Identity{Signer: uploadService}, agentStore, handlerMap, logger,
		)

		// Create a receipt for some task that is NOT in the agent store
		// and is NOT embedded in the receipt as an invocation
		taskInv, err := invocation.Invoke(
			uploadService, uploadService,
			ucan.NewCapability("test/thing", uploadService.DID().String(), ucan.NoCaveats{}),
		)
		require.NoError(t, err)

		rcpt, err := receipt.Issue(
			uploadService,
			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
			ran.FromInvocation(taskInv),
		)
		require.NoError(t, err)

		concludeInv := issueConclude(t, uploadService, rcpt)
		concludeCap := ucancap.Conclude.New(
			uploadService.DID().String(),
			ucancap.ConcludeCaveats{Receipt: rcpt.Root().Link()},
		)
		res, _, err := handler(ctx, concludeCap, concludeInv, nil)
		require.NoError(t, err)

		o, x := result.Unwrap(res)
		require.Nil(t, x)
		require.NotNil(t, o)
	})

	t.Run("dispatches to registered handler", func(t *testing.T) {
		agentStore := agent_store.New()

		var called bool
		handlerMap := map[ucan.Ability]handlers.ConclusionHandlerFunc{
			"test/thing": func(_ context.Context, _ invocation.Invocation, _ receipt.AnyReceipt, _ server.InvocationContext) error {
				called = true
				return nil
			},
		}

		handler := handlers.UCANConcludeHandler(
			&identity.Identity{Signer: uploadService}, agentStore, handlerMap, logger,
		)

		// Create a task invocation with "test/thing" ability
		taskInv, err := invocation.Invoke(
			uploadService, uploadService,
			ucan.NewCapability("test/thing", uploadService.DID().String(), ucan.NoCaveats{}),
		)
		require.NoError(t, err)

		rcpt, err := receipt.Issue(
			uploadService,
			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
			ran.FromInvocation(taskInv),
		)
		require.NoError(t, err)

		// Store the task invocation so the handler can find it
		writeAgentMessage(t, agentStore, []invocation.Invocation{taskInv}, []receipt.AnyReceipt{rcpt})

		concludeInv := issueConclude(t, uploadService, rcpt)
		concludeCap := ucancap.Conclude.New(
			uploadService.DID().String(),
			ucancap.ConcludeCaveats{Receipt: rcpt.Root().Link()},
		)
		res, _, err := handler(ctx, concludeCap, concludeInv, nil)
		require.NoError(t, err)
		require.True(t, called)

		o, x := result.Unwrap(res)
		require.Nil(t, x)
		require.NotNil(t, o)
	})

	t.Run("no handler for ability returns success", func(t *testing.T) {
		agentStore := agent_store.New()
		// Register no handlers
		handlerMap := map[ucan.Ability]handlers.ConclusionHandlerFunc{}

		handler := handlers.UCANConcludeHandler(
			&identity.Identity{Signer: uploadService}, agentStore, handlerMap, logger,
		)

		taskInv, err := invocation.Invoke(
			uploadService, uploadService,
			ucan.NewCapability("test/unhandled", uploadService.DID().String(), ucan.NoCaveats{}),
		)
		require.NoError(t, err)

		rcpt, err := receipt.Issue(
			uploadService,
			result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
			ran.FromInvocation(taskInv),
		)
		require.NoError(t, err)

		writeAgentMessage(t, agentStore, []invocation.Invocation{taskInv}, []receipt.AnyReceipt{rcpt})

		concludeInv := issueConclude(t, uploadService, rcpt)
		concludeCap := ucancap.Conclude.New(
			uploadService.DID().String(),
			ucancap.ConcludeCaveats{Receipt: rcpt.Root().Link()},
		)
		res, _, err := handler(ctx, concludeCap, concludeInv, nil)
		require.NoError(t, err)

		o, x := result.Unwrap(res)
		require.Nil(t, x)
		require.NotNil(t, o)
	})
}
