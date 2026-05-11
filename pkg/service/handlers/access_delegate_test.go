package handlers

import (
	"context"
	"testing"

	"github.com/fil-forge/libforge/capabilities/access"
	"github.com/fil-forge/libforge/didmailto"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/provisioning"
	consumermemory "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	dlgmemory "github.com/fil-forge/sprue/pkg/store/delegation/memory"
	subscriptionmemory "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/result"
	"github.com/fil-forge/ucantone/ucan/delegation"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/ipfs/go-cid"
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
	account := testutil.Must(didmailto.New("test@example.com"))(t)

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

	t.Run("no providers for space", func(t *testing.T) {
		id := newTestIdentity(t)
		dlgStore := dlgmemory.New()
		ps := newTestProvisioningService(t, nil)
		handler := NewAccessDelegateHandler(dlgStore, ps, logger)

		agent := testutil.RandomSigner(t)
		space := testutil.RandomSigner(t)

		args := access.DelegateArguments{Delegations: []cid.Cid{}}

		inv, err := access.Delegate.Invoke(
			agent,
			space,
			&args,
			invocation.WithAudience(id.Signer),
		)
		require.NoError(t, err)

		req := execution.NewRequest(t.Context(), inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(id.Signer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, fail := result.Unwrap(res.Receipt().Out())
		require.NotNil(t, fail)
	})

	t.Run("success with delegation", func(t *testing.T) {
		id := newTestIdentity(t)
		dlgStore := dlgmemory.New()

		space := testutil.RandomSigner(t)

		ps := newProvisionedService(t, id.Signer.DID(), space.DID())
		handler := NewAccessDelegateHandler(dlgStore, ps, logger)

		agent := testutil.RandomSigner(t)

		// Create a delegation from the space to the agent for some capability.
		dlg, err := delegation.Delegate(space, agent, space, "/space/blob/add")
		require.NoError(t, err)

		args := access.DelegateArguments{
			Delegations: []cid.Cid{dlg.Link()},
		}

		inv, err := access.Delegate.Invoke(
			agent,
			space,
			&args,
			invocation.WithAudience(id.Signer),
		)
		require.NoError(t, err)

		// Attach the delegation to the request metadata so extractDelegations can find it.
		req := execution.NewRequest(t.Context(), inv, execution.WithDelegations(dlg))
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(id.Signer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, fail := result.Unwrap(res.Receipt().Out())
		require.Nil(t, fail)

		// Verify the delegation was stored.
		page, err := dlgStore.ListByAudience(t.Context(), agent.DID())
		require.NoError(t, err)
		require.Len(t, page.Results, 1)
	})

	t.Run("empty delegations with provisioned space", func(t *testing.T) {
		id := newTestIdentity(t)
		dlgStore := dlgmemory.New()

		space := testutil.RandomSigner(t)

		ps := newProvisionedService(t, id.Signer.DID(), space.DID())
		handler := NewAccessDelegateHandler(dlgStore, ps, logger)

		agent := testutil.RandomSigner(t)

		args := access.DelegateArguments{Delegations: []cid.Cid{}}

		inv, err := access.Delegate.Invoke(
			agent,
			space,
			&args,
			invocation.WithAudience(id.Signer),
		)
		require.NoError(t, err)

		req := execution.NewRequest(t.Context(), inv)
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(id.Signer))
		require.NoError(t, err)

		err = handler.Handler(req, res)
		require.NoError(t, err)

		_, fail := result.Unwrap(res.Receipt().Out())
		require.Nil(t, fail)
	})

	t.Run("delegation not found in metadata", func(t *testing.T) {
		id := newTestIdentity(t)
		dlgStore := dlgmemory.New()

		space := testutil.RandomSigner(t)

		ps := newProvisionedService(t, id.Signer.DID(), space.DID())
		handler := NewAccessDelegateHandler(dlgStore, ps, logger)

		agent := testutil.RandomSigner(t)

		// Reference a delegation by CID, but don't include it in the request metadata.
		// We still need at least one delegation in the request so req.Metadata() is non-nil.
		other, err := delegation.Delegate(space, agent, space, "/other")
		require.NoError(t, err)

		missing, err := delegation.Delegate(space, agent, space, "/space/blob/add")
		require.NoError(t, err)

		args := access.DelegateArguments{
			Delegations: []cid.Cid{missing.Link()},
		}

		inv, err := access.Delegate.Invoke(
			agent,
			space,
			&args,
			invocation.WithAudience(id.Signer),
		)
		require.NoError(t, err)

		req := execution.NewRequest(t.Context(), inv, execution.WithDelegations(other))
		res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(id.Signer))
		require.NoError(t, err)

		// extractDelegations returns an error directly (not via SetFailure) when a
		// referenced delegation is missing from the request metadata.
		err = handler.Handler(req, res)
		require.Error(t, err)
		require.Contains(t, err.Error(), "delegation not found")
	})
}
