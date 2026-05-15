package handlers_test

import (
	"bytes"
	"context"
	"testing"

	ucancaps "github.com/fil-forge/libforge/capabilities/ucan"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	"github.com/fil-forge/sprue/pkg/store/agent"
	agent_store "github.com/fil-forge/sprue/pkg/store/agent/memory"
	edm "github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/receipt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestUCANConcludeHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	// Build a "task" invocation and a receipt for it.
	newTaskAndReceipt := func(t *testing.T, cmd ucan.Command) (ucan.Invocation, ucan.Receipt) {
		t.Helper()
		taskInv, err := invocation.Invoke(uploadService, uploadService, cmd, datamodel.Map{})
		require.NoError(t, err)
		rcpt, err := receipt.IssueOK(
			uploadService,
			taskInv.Task().Link(),
			datamodel.NewAny(int64(1)),
		)
		require.NoError(t, err)
		return taskInv, rcpt
	}

	t.Run("receipt not in metadata", func(t *testing.T) {
		agentStore := agent_store.New()
		handlerMap := map[ucan.Command]handlers.ConclusionHandlerFunc{}

		handler := handlers.NewUCANConcludeHandler(
			&identity.Identity{Signer: uploadService}, agentStore, handlerMap, logger,
		)

		_, rcpt := newTaskAndReceipt(t, "/test/thing")

		concludeInv, err := ucancaps.Conclude.Invoke(
			uploadService,
			uploadService,
			&ucancaps.ConcludeArguments{Receipt: rcpt.Link()},
			invocation.WithAudience(uploadService),
		)
		require.NoError(t, err)

		// The receipt is referenced in args but NOT attached to the request
		// metadata, so the handler can't find it.
		req := execution.NewRequest(ctx, concludeInv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, x := res.Receipt().Out().Unpack()
		require.NotNil(t, x)

		var model edm.ErrorModel
		require.NoError(t, model.UnmarshalCBOR(bytes.NewReader(x)))
		require.Equal(t, ucancaps.ConclusionReceiptNotFoundErrorName, model.Name())
	})

	t.Run("unknown invocation returns success", func(t *testing.T) {
		agentStore := agent_store.New()
		handlerMap := map[ucan.Command]handlers.ConclusionHandlerFunc{}

		handler := handlers.NewUCANConcludeHandler(
			&identity.Identity{Signer: uploadService}, agentStore, handlerMap, logger,
		)

		// Receipt is supplied but the ran invocation is neither in the request
		// metadata nor in the agent store — the handler treats this as a no-op.
		_, rcpt := newTaskAndReceipt(t, "/test/thing")

		concludeInv, err := ucancaps.Conclude.Invoke(
			uploadService,
			uploadService,
			&ucancaps.ConcludeArguments{Receipt: rcpt.Link()},
			invocation.WithAudience(uploadService),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, concludeInv, execution.WithReceipts(rcpt))
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		require.False(t, res.Receipt().Out().IsErr())
	})

	t.Run("dispatches to registered handler", func(t *testing.T) {
		agentStore := agent_store.New()

		var (
			called  bool
			gotInv  ucan.Invocation
			gotRcpt ucan.Receipt
		)
		handlerMap := map[ucan.Command]handlers.ConclusionHandlerFunc{
			"/test/thing": func(_ context.Context, inv ucan.Invocation, rcpt ucan.Receipt, _ ucan.Container) error {
				called = true
				gotInv = inv
				gotRcpt = rcpt
				return nil
			},
		}

		handler := handlers.NewUCANConcludeHandler(
			&identity.Identity{Signer: uploadService}, agentStore, handlerMap, logger,
		)

		taskInv, rcpt := newTaskAndReceipt(t, "/test/thing")

		// Persist the task invocation in the agent store so the handler can
		// look it up by the receipt's ran CID.
		msg := container.New(
			container.WithInvocations(taskInv),
			container.WithReceipts(rcpt),
		)
		require.NoError(t, agentStore.Write(ctx, msg, agent.Index(msg)))

		concludeInv, err := ucancaps.Conclude.Invoke(
			uploadService,
			uploadService,
			&ucancaps.ConcludeArguments{Receipt: rcpt.Link()},
			invocation.WithAudience(uploadService),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, concludeInv, execution.WithReceipts(rcpt))
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		require.False(t, res.Receipt().Out().IsErr())

		require.True(t, called)
		require.Equal(t, taskInv.Task().Link(), gotInv.Task().Link())
		require.Equal(t, rcpt.Link(), gotRcpt.Link())
	})

	t.Run("no handler for command returns success", func(t *testing.T) {
		agentStore := agent_store.New()
		// No handlers registered.
		handlerMap := map[ucan.Command]handlers.ConclusionHandlerFunc{}

		handler := handlers.NewUCANConcludeHandler(
			&identity.Identity{Signer: uploadService}, agentStore, handlerMap, logger,
		)

		taskInv, rcpt := newTaskAndReceipt(t, "/test/unhandled")

		msg := container.New(
			container.WithInvocations(taskInv),
			container.WithReceipts(rcpt),
		)
		require.NoError(t, agentStore.Write(ctx, msg, agent.Index(msg)))

		concludeInv, err := ucancaps.Conclude.Invoke(
			uploadService,
			uploadService,
			&ucancaps.ConcludeArguments{Receipt: rcpt.Link()},
			invocation.WithAudience(uploadService),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, concludeInv, execution.WithReceipts(rcpt))
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		require.False(t, res.Receipt().Out().IsErr())
	})

	t.Run("invocation supplied via metadata", func(t *testing.T) {
		agentStore := agent_store.New()

		var called bool
		handlerMap := map[ucan.Command]handlers.ConclusionHandlerFunc{
			"/test/thing": func(_ context.Context, _ ucan.Invocation, _ ucan.Receipt, _ ucan.Container) error {
				called = true
				return nil
			},
		}

		handler := handlers.NewUCANConcludeHandler(
			&identity.Identity{Signer: uploadService}, agentStore, handlerMap, logger,
		)

		taskInv, rcpt := newTaskAndReceipt(t, "/test/thing")

		// The ran invocation is supplied directly in the request metadata —
		// no agent-store lookup required.
		concludeInv, err := ucancaps.Conclude.Invoke(
			uploadService,
			uploadService,
			&ucancaps.ConcludeArguments{Receipt: rcpt.Link()},
			invocation.WithAudience(uploadService),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, concludeInv,
			execution.WithReceipts(rcpt),
			execution.WithInvocations(taskInv),
		)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		require.False(t, res.Receipt().Out().IsErr())
		require.True(t, called)
	})
}
