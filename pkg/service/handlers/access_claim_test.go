package handlers

import (
	"testing"

	"github.com/fil-forge/libforge/commands/access"
	"github.com/fil-forge/sprue/internal/testutil"
	dlgmemory "github.com/fil-forge/sprue/pkg/store/delegation/memory"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/command"
	"github.com/fil-forge/ucantone/ucan/delegation"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestAccessClaimHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)

	t.Run("no delegations", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := NewAccessClaimHandler(id, store, logger)

		agent := testutil.RandomIssuer(t)

		args := access.ClaimArguments{}
		inv, err := access.Claim.Invoke(
			agent,
			agent.DID(),
			&args,
			invocation.WithAudience(id.Issuer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(t.Context(), inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(id.Issuer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		ok, err := access.Claim.Unpack(res.Receipt())
		require.NoError(t, err)
		require.Empty(t, ok.Delegations)
	})

	t.Run("returns stored delegations", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := NewAccessClaimHandler(id, store, logger)

		agent := testutil.RandomIssuer(t)

		dlg, err := delegation.Delegate(testutil.Alice, agent.DID(), testutil.Alice.DID(), command.MustParse("/test/thing"))
		require.NoError(t, err)

		err = store.PutMany(t.Context(), []ucan.Token{dlg}, testutil.RandomCID(t))
		require.NoError(t, err)

		args := access.ClaimArguments{}
		inv, err := access.Claim.Invoke(
			agent,
			agent.DID(),
			&args,
			invocation.WithAudience(id.Issuer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(t.Context(), inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(id.Issuer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		ok, err := access.Claim.Unpack(res.Receipt())
		require.NoError(t, err)
		require.Equal(t, []cid.Cid{dlg.Link()}, ok.Delegations)
	})

	t.Run("returns multiple delegations", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := NewAccessClaimHandler(id, store, logger)

		agent := testutil.RandomIssuer(t)

		dlg1, err := delegation.Delegate(testutil.Alice, agent.DID(), testutil.Alice.DID(), command.MustParse("/test/one"))
		require.NoError(t, err)

		dlg2, err := delegation.Delegate(testutil.Bob, agent.DID(), testutil.Bob.DID(), command.MustParse("/test/two"))
		require.NoError(t, err)

		err = store.PutMany(t.Context(), []ucan.Token{dlg1, dlg2}, testutil.RandomCID(t))
		require.NoError(t, err)

		args := access.ClaimArguments{}
		inv, err := access.Claim.Invoke(
			agent,
			agent.DID(),
			&args,
			invocation.WithAudience(id.Issuer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(t.Context(), inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(id.Issuer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		ok, err := access.Claim.Unpack(res.Receipt())
		require.NoError(t, err)
		require.Len(t, ok.Delegations, 2)
		require.ElementsMatch(t, []cid.Cid{dlg1.Link(), dlg2.Link()}, ok.Delegations)
	})

	t.Run("does not return delegations for other audiences", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := NewAccessClaimHandler(id, store, logger)

		agent := testutil.RandomIssuer(t)
		otherAgent := testutil.RandomIssuer(t)

		// Delegation is for otherAgent, not agent.
		dlg, err := delegation.Delegate(testutil.Alice, otherAgent.DID(), testutil.Alice.DID(), command.MustParse("/test/thing"))
		require.NoError(t, err)

		err = store.PutMany(t.Context(), []ucan.Token{dlg}, testutil.RandomCID(t))
		require.NoError(t, err)

		args := access.ClaimArguments{}
		inv, err := access.Claim.Invoke(
			agent,
			agent.DID(),
			&args,
			invocation.WithAudience(id.Issuer.DID()),
		)
		require.NoError(t, err)

		req := execution.NewRequest(t.Context(), inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(id.Issuer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		ok, err := access.Claim.Unpack(res.Receipt())
		require.NoError(t, err)
		require.Empty(t, ok.Delegations)
	})
}
