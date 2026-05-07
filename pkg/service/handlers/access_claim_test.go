package handlers

import (
	"context"
	"testing"

	"github.com/fil-forge/go-libstoracha/capabilities/access"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/identity"
	dlgmemory "github.com/fil-forge/sprue/pkg/store/delegation/memory"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestAccessClaimHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)

	t.Run("invalid audience DID", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := AccessClaimHandler(id, store, logger)

		cap := ucan.NewCapability(
			access.ClaimAbility,
			"not-a-did",
			access.ClaimCaveats{},
		)

		agent, err := identity.New("")
		require.NoError(t, err)

		inv, err := invocation.Invoke(agent.Signer, id.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)
	})

	t.Run("no delegations", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := AccessClaimHandler(id, store, logger)

		agent, err := identity.New("")
		require.NoError(t, err)

		cap := ucan.NewCapability(
			access.ClaimAbility,
			agent.DID(),
			access.ClaimCaveats{},
		)

		inv, err := invocation.Invoke(agent.Signer, id.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Empty(t, ok.Delegations.Keys)
	})

	t.Run("returns stored delegations", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := AccessClaimHandler(id, store, logger)

		agent, err := identity.New("")
		require.NoError(t, err)

		// Create a delegation to the agent
		dlg, err := delegation.Delegate(
			testutil.Alice,
			agent.Signer,
			[]ucan.Capability[ucan.NoCaveats]{
				ucan.NewCapability("test/thing", testutil.Alice.DID().String(), ucan.NoCaveats{}),
			},
		)
		require.NoError(t, err)

		cause := testutil.RandomCID(t)
		err = store.PutMany(context.Background(), []delegation.Delegation{dlg}, cause)
		require.NoError(t, err)

		cap := ucan.NewCapability(
			access.ClaimAbility,
			agent.DID(),
			access.ClaimCaveats{},
		)

		inv, err := invocation.Invoke(agent.Signer, id.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Len(t, ok.Delegations.Keys, 1)
		require.Equal(t, dlg.Link().String(), ok.Delegations.Keys[0])
		require.NotEmpty(t, ok.Delegations.Values[ok.Delegations.Keys[0]])
	})

	t.Run("returns multiple delegations", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := AccessClaimHandler(id, store, logger)

		agent, err := identity.New("")
		require.NoError(t, err)

		dlg1, err := delegation.Delegate(
			testutil.Alice,
			agent.Signer,
			[]ucan.Capability[ucan.NoCaveats]{
				ucan.NewCapability("test/one", testutil.Alice.DID().String(), ucan.NoCaveats{}),
			},
		)
		require.NoError(t, err)

		dlg2, err := delegation.Delegate(
			testutil.Bob,
			agent.Signer,
			[]ucan.Capability[ucan.NoCaveats]{
				ucan.NewCapability("test/two", testutil.Bob.DID().String(), ucan.NoCaveats{}),
			},
		)
		require.NoError(t, err)

		cause := testutil.RandomCID(t)
		err = store.PutMany(context.Background(), []delegation.Delegation{dlg1, dlg2}, cause)
		require.NoError(t, err)

		cap := ucan.NewCapability(
			access.ClaimAbility,
			agent.DID(),
			access.ClaimCaveats{},
		)

		inv, err := invocation.Invoke(agent.Signer, id.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Len(t, ok.Delegations.Keys, 2)
	})

	t.Run("does not return delegations for other audiences", func(t *testing.T) {
		id := newTestIdentity(t)
		store := dlgmemory.New()
		handler := AccessClaimHandler(id, store, logger)

		agent, err := identity.New("")
		require.NoError(t, err)

		otherAgent, err := identity.New("")
		require.NoError(t, err)

		// Delegate to a different agent
		dlg, err := delegation.Delegate(
			testutil.Alice,
			otherAgent.Signer,
			[]ucan.Capability[ucan.NoCaveats]{
				ucan.NewCapability("test/thing", testutil.Alice.DID().String(), ucan.NoCaveats{}),
			},
		)
		require.NoError(t, err)

		cause := testutil.RandomCID(t)
		err = store.PutMany(context.Background(), []delegation.Delegation{dlg}, cause)
		require.NoError(t, err)

		// Claim as the original agent — should get nothing
		cap := ucan.NewCapability(
			access.ClaimAbility,
			agent.DID(),
			access.ClaimCaveats{},
		)

		inv, err := invocation.Invoke(agent.Signer, id.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Empty(t, ok.Delegations.Keys)
	})
}
