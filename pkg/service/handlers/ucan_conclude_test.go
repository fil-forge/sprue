package handlers_test

import (
	"context"
	"testing"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	ucancmds "github.com/fil-forge/libforge/commands/ucan"
	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	"github.com/fil-forge/sprue/pkg/store/agent"
	agent_store "github.com/fil-forge/sprue/pkg/store/agent/memory"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/command"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/receipt"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// linksOf is a receipt's link as the one-element list a conclude argument
// always takes.
func linksOf(rcpts ...ucan.Receipt) []cid.Cid {
	links := make([]cid.Cid, len(rcpts))
	for i, r := range rcpts {
		links[i] = r.Link()
	}
	return links
}

func TestUCANConcludeHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	// Build a "task" invocation and a receipt for it.
	newTaskAndReceipt := func(t *testing.T, cmd ucan.Command) (ucan.Invocation, ucan.Receipt) {
		t.Helper()
		taskInv, err := invocation.Invoke(uploadService, uploadService.DID(), cmd, datamodel.Map{})
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
			identity.Identity{Issuer: uploadService}, agentStore, handlerMap, logger,
		)

		_, rcpt := newTaskAndReceipt(t, command.MustParse("/test/thing"))

		concludeInv, err := ucancmds.Conclude.Invoke(
			uploadService,
			uploadService.DID(),
			&ucancmds.ConcludeArguments{Receipts: linksOf(rcpt)},
			invocation.WithAudience(uploadService.DID()),
		)
		require.NoError(t, err)

		// The receipt is referenced in args but NOT attached to the request
		// metadata, so the handler can't find it.
		req := execution.NewRequest(ctx, concludeInv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, err = ucancmds.Conclude.Unpack(res.Receipt())
		require.ErrorIs(t, err, ucancmds.ErrConclusionReceiptNotFound)
	})

	t.Run("unknown invocation returns success", func(t *testing.T) {
		agentStore := agent_store.New()
		handlerMap := map[ucan.Command]handlers.ConclusionHandlerFunc{}

		handler := handlers.NewUCANConcludeHandler(
			identity.Identity{Issuer: uploadService}, agentStore, handlerMap, logger,
		)

		// Receipt is supplied but the ran invocation is neither in the request
		// metadata nor in the agent store — the handler treats this as a no-op.
		_, rcpt := newTaskAndReceipt(t, command.MustParse("/test/thing"))

		concludeInv, err := ucancmds.Conclude.Invoke(
			uploadService,
			uploadService.DID(),
			&ucancmds.ConcludeArguments{Receipts: linksOf(rcpt)},
			invocation.WithAudience(uploadService.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, concludeInv, execution.WithReceipts(rcpt))
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, err = blobcmds.Allocate.Unpack(res.Receipt())
		require.NoError(t, err)
	})

	t.Run("dispatches to registered handler", func(t *testing.T) {
		agentStore := agent_store.New()

		var (
			called  bool
			gotInv  ucan.Invocation
			gotRcpt ucan.Receipt
		)
		handlerMap := map[ucan.Command]handlers.ConclusionHandlerFunc{
			command.MustParse("/test/thing"): func(_ context.Context, cs []handlers.Conclusion) (ucan.Container, error) {
				called = true
				require.Len(t, cs, 1)
				gotInv = cs[0].Invocation
				gotRcpt = cs[0].Receipt
				return nil, nil
			},
		}

		handler := handlers.NewUCANConcludeHandler(
			identity.Identity{Issuer: uploadService}, agentStore, handlerMap, logger,
		)

		taskInv, rcpt := newTaskAndReceipt(t, command.MustParse("/test/thing"))

		// Persist the task invocation in the agent store so the handler can
		// look it up by the receipt's ran CID.
		msg := container.New(
			container.WithInvocations(taskInv),
			container.WithReceipts(rcpt),
		)
		require.NoError(t, agentStore.Write(ctx, msg, agent.Index(msg)))

		concludeInv, err := ucancmds.Conclude.Invoke(
			uploadService,
			uploadService.DID(),
			&ucancmds.ConcludeArguments{Receipts: linksOf(rcpt)},
			invocation.WithAudience(uploadService.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, concludeInv, execution.WithReceipts(rcpt))
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, err = blobcmds.Allocate.Unpack(res.Receipt())
		require.NoError(t, err)

		require.True(t, called)
		require.Equal(t, taskInv.Task().Link(), gotInv.Task().Link())
		require.Equal(t, rcpt.Link(), gotRcpt.Link())
	})

	t.Run("no handler for command returns success", func(t *testing.T) {
		agentStore := agent_store.New()
		// No handlers registered.
		handlerMap := map[ucan.Command]handlers.ConclusionHandlerFunc{}

		handler := handlers.NewUCANConcludeHandler(
			identity.Identity{Issuer: uploadService}, agentStore, handlerMap, logger,
		)

		taskInv, rcpt := newTaskAndReceipt(t, command.MustParse("/test/unhandled"))

		msg := container.New(
			container.WithInvocations(taskInv),
			container.WithReceipts(rcpt),
		)
		require.NoError(t, agentStore.Write(ctx, msg, agent.Index(msg)))

		concludeInv, err := ucancmds.Conclude.Invoke(
			uploadService,
			uploadService.DID(),
			&ucancmds.ConcludeArguments{Receipts: linksOf(rcpt)},
			invocation.WithAudience(uploadService.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, concludeInv, execution.WithReceipts(rcpt))
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, err = blobcmds.Allocate.Unpack(res.Receipt())
		require.NoError(t, err)
	})

	t.Run("delivers many receipts in one invocation", func(t *testing.T) {
		agentStore := agent_store.New()

		var got [][]handlers.Conclusion
		handlerMap := map[ucan.Command]handlers.ConclusionHandlerFunc{
			command.MustParse("/test/thing"): func(_ context.Context, cs []handlers.Conclusion) (ucan.Container, error) {
				got = append(got, cs)
				return nil, nil
			},
		}
		handler := handlers.NewUCANConcludeHandler(
			identity.Identity{Issuer: uploadService}, agentStore, handlerMap, logger,
		)

		const n = 3
		var links []cid.Cid
		var invs []ucan.Invocation
		var rcpts []ucan.Receipt
		for range n {
			taskInv, rcpt := newTaskAndReceipt(t, command.MustParse("/test/thing"))
			links = append(links, rcpt.Link())
			invs = append(invs, taskInv)
			rcpts = append(rcpts, rcpt)
		}

		concludeInv, err := ucancmds.Conclude.Invoke(
			uploadService,
			uploadService.DID(),
			&ucancmds.ConcludeArguments{Receipts: links},
			invocation.WithAudience(uploadService.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, concludeInv,
			execution.WithReceipts(rcpts...),
			execution.WithInvocations(invs...),
		)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
		require.NoError(t, err)

		require.NoError(t, handler.Handler(req, res))
		_, err = ucancmds.Conclude.Unpack(res.Receipt())
		require.NoError(t, err)

		// One call carrying every receipt, not one call per receipt.
		require.Len(t, got, 1)
		require.Len(t, got[0], n)
	})

	t.Run("receipts a handler produced travel in the response", func(t *testing.T) {
		agentStore := agent_store.New()

		taskInv, rcpt := newTaskAndReceipt(t, command.MustParse("/test/thing"))
		// What the handler ran as a consequence of the conclusion.
		onwardInv, onwardRcpt := newTaskAndReceipt(t, command.MustParse("/test/onward"))

		handlerMap := map[ucan.Command]handlers.ConclusionHandlerFunc{
			command.MustParse("/test/thing"): func(_ context.Context, _ []handlers.Conclusion) (ucan.Container, error) {
				return container.New(
					container.WithInvocations(onwardInv),
					container.WithReceipts(onwardRcpt),
				), nil
			},
		}
		handler := handlers.NewUCANConcludeHandler(
			identity.Identity{Issuer: uploadService}, agentStore, handlerMap, logger,
		)

		concludeInv, err := ucancmds.Conclude.Invoke(
			uploadService,
			uploadService.DID(),
			&ucancmds.ConcludeArguments{Receipts: linksOf(rcpt)},
			invocation.WithAudience(uploadService.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, concludeInv,
			execution.WithReceipts(rcpt),
			execution.WithInvocations(taskInv),
		)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
		require.NoError(t, err)

		require.NoError(t, handler.Handler(req, res))
		require.NotNil(t, res.Metadata())
		require.Len(t, res.Metadata().Receipts(), 1)
		require.Equal(t, onwardRcpt.Link(), res.Metadata().Receipts()[0].Link())
		require.Len(t, res.Metadata().Invocations(), 1)
	})

	t.Run("one missing receipt fails the whole conclusion", func(t *testing.T) {
		agentStore := agent_store.New()
		handler := handlers.NewUCANConcludeHandler(
			identity.Identity{Issuer: uploadService}, agentStore,
			map[ucan.Command]handlers.ConclusionHandlerFunc{}, logger,
		)

		taskInv, rcpt := newTaskAndReceipt(t, command.MustParse("/test/thing"))
		_, absent := newTaskAndReceipt(t, command.MustParse("/test/thing"))

		concludeInv, err := ucancmds.Conclude.Invoke(
			uploadService,
			uploadService.DID(),
			&ucancmds.ConcludeArguments{Receipts: []cid.Cid{rcpt.Link(), absent.Link()}},
			invocation.WithAudience(uploadService.DID()),
		)
		require.NoError(t, err)

		// Only the first receipt travels in the container.
		req := execution.NewRequest(ctx, concludeInv,
			execution.WithReceipts(rcpt),
			execution.WithInvocations(taskInv),
		)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
		require.NoError(t, err)

		require.NoError(t, handler.Handler(req, res))
		_, err = ucancmds.Conclude.Unpack(res.Receipt())
		require.ErrorIs(t, err, ucancmds.ErrConclusionReceiptNotFound)
	})

	t.Run("invocation supplied via metadata", func(t *testing.T) {
		agentStore := agent_store.New()

		var called bool
		handlerMap := map[ucan.Command]handlers.ConclusionHandlerFunc{
			command.MustParse("/test/thing"): func(_ context.Context, _ []handlers.Conclusion) (ucan.Container, error) {
				called = true
				return nil, nil
			},
		}

		handler := handlers.NewUCANConcludeHandler(
			identity.Identity{Issuer: uploadService}, agentStore, handlerMap, logger,
		)

		taskInv, rcpt := newTaskAndReceipt(t, command.MustParse("/test/thing"))

		// The ran invocation is supplied directly in the request metadata —
		// no agent-store lookup required.
		concludeInv, err := ucancmds.Conclude.Invoke(
			uploadService,
			uploadService.DID(),
			&ucancmds.ConcludeArguments{Receipts: linksOf(rcpt)},
			invocation.WithAudience(uploadService.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(ctx, concludeInv,
			execution.WithReceipts(rcpt),
			execution.WithInvocations(taskInv),
		)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, err = blobcmds.Allocate.Unpack(res.Receipt())
		require.NoError(t, err)
		require.True(t, called)
	})
}
