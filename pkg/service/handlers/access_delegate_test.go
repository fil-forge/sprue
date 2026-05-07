package handlers

import (
	"context"
	"testing"

	"github.com/fil-forge/go-libstoracha/capabilities/access"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/provisioning"
	consumermemory "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	dlgmemory "github.com/fil-forge/sprue/pkg/store/delegation/memory"
	subscriptionmemory "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func newTestProvisioningService(t *testing.T, providers []did.DID) *provisioning.Service {
	t.Helper()
	return provisioning.NewService(
		providers,
		consumermemory.New(),
		subscriptionmemory.New(),
	)
}

func newProvisionedService(t *testing.T, serviceDID did.DID, space did.DID) *provisioning.Service {
	t.Helper()

	consumerStore := consumermemory.New()
	subscriptionStore := subscriptionmemory.New()
	account := mustMailtoDID(t, "test@example.com")

	ps := provisioning.NewService(
		[]did.DID{serviceDID},
		consumerStore,
		subscriptionStore,
	)

	cause := testutil.RandomCID(t)
	_, err := ps.Provision(context.Background(), account, space, serviceDID, cause)
	require.NoError(t, err)

	return ps
}

func TestAccessDelegateHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)

	t.Run("invalid resource DID", func(t *testing.T) {
		id := newTestIdentity(t)
		dlgStore := dlgmemory.New()
		ps := newTestProvisioningService(t, nil)
		handler := AccessDelegateHandler(dlgStore, ps, logger)

		agent, err := identity.New("")
		require.NoError(t, err)

		cap := ucan.NewCapability(
			access.DelegateAbility,
			"not-a-did",
			access.DelegateCaveats{
				Delegations: access.DelegationLinksModel{
					Keys:   []string{},
					Values: map[string]ucan.Link{},
				},
			},
		)

		inv, err := invocation.Invoke(agent.Signer, id.Signer, cap)
		require.NoError(t, err)

		_, _, err = handler(context.Background(), cap, inv, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid resource")
	})

	t.Run("no providers for space", func(t *testing.T) {
		id := newTestIdentity(t)
		dlgStore := dlgmemory.New()
		// No providers provisioned
		ps := newTestProvisioningService(t, nil)
		handler := AccessDelegateHandler(dlgStore, ps, logger)

		agent, err := identity.New("")
		require.NoError(t, err)

		cap := ucan.NewCapability(
			access.DelegateAbility,
			agent.DID(),
			access.DelegateCaveats{
				Delegations: access.DelegationLinksModel{
					Keys:   []string{},
					Values: map[string]ucan.Link{},
				},
			},
		)

		inv, err := invocation.Invoke(agent.Signer, id.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)
	})

	t.Run("success with delegation", func(t *testing.T) {
		id := newTestIdentity(t)
		dlgStore := dlgmemory.New()

		space, err := identity.New("")
		require.NoError(t, err)

		ps := newProvisionedService(t, id.Signer.DID(), space.Signer.DID())
		handler := AccessDelegateHandler(dlgStore, ps, logger)

		agent, err := identity.New("")
		require.NoError(t, err)

		// Create a delegation from space to agent
		dlg, err := delegation.Delegate(
			space.Signer,
			agent.Signer,
			[]ucan.Capability[ucan.NoCaveats]{
				ucan.NewCapability("space/blob/add", space.DID(), ucan.NoCaveats{}),
			},
		)
		require.NoError(t, err)

		k := dlg.Link().String()
		cap := ucan.NewCapability(
			access.DelegateAbility,
			space.DID(),
			access.DelegateCaveats{
				Delegations: access.DelegationLinksModel{
					Keys:   []string{k},
					Values: map[string]ucan.Link{k: dlg.Link()},
				},
			},
		)

		// Create invocation and attach delegation blocks so extractDelegations can find them
		inv, err := invocation.Invoke(
			agent.Signer,
			id.Signer,
			cap,
			delegation.WithProof(delegation.FromDelegation(dlg)),
		)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.Nil(t, fail)

		// Verify the delegation was stored
		page, err := dlgStore.ListByAudience(context.Background(), agent.Signer.DID())
		require.NoError(t, err)
		require.Len(t, page.Results, 1)
	})

	t.Run("empty delegations with provisioned space", func(t *testing.T) {
		id := newTestIdentity(t)
		dlgStore := dlgmemory.New()

		space, err := identity.New("")
		require.NoError(t, err)

		ps := newProvisionedService(t, id.Signer.DID(), space.Signer.DID())
		handler := AccessDelegateHandler(dlgStore, ps, logger)

		agent, err := identity.New("")
		require.NoError(t, err)

		cap := ucan.NewCapability(
			access.DelegateAbility,
			space.DID(),
			access.DelegateCaveats{
				Delegations: access.DelegationLinksModel{
					Keys:   []string{},
					Values: map[string]ucan.Link{},
				},
			},
		)

		inv, err := invocation.Invoke(agent.Signer, id.Signer, cap)
		require.NoError(t, err)

		res, _, err := handler(context.Background(), cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.Nil(t, fail)
	})
}
