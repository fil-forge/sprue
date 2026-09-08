package handlers_test

import (
	"context"
	"testing"

	"github.com/fil-forge/libforge/attestation/didmailto"
	routingcmds "github.com/fil-forge/libforge/commands/routing"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	routingpolicy "github.com/fil-forge/sprue/pkg/store/routing_policy"
	subscription_store "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// invokeRoutingUse builds a /routing/use invocation issued by the space itself
// and returns the request + signed response.
func invokeRoutingUse(
	t *testing.T,
	ctx context.Context,
	space ucan.Issuer,
	uploadService ucan.Issuer,
	policy *did.DID,
) (execution.Request, *execution.ExecResponse) {
	t.Helper()
	inv, err := routingcmds.Use.Invoke(
		space,
		space.DID(),
		&routingcmds.UseArguments{Policy: policy},
		invocation.WithAudience(uploadService.DID()),
	)
	require.NoError(t, err)
	req := execution.NewRequest(ctx, inv)
	res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
	require.NoError(t, err)
	return req, res
}

func TestRoutingUseHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()
	uploadService := testutil.WebService

	newProvisioning := func() *provisioning.Service {
		return provisioning.NewService([]did.DID{uploadService.DID()}, consumer_store.New(), subscription_store.New())
	}
	provision := func(t *testing.T, provisioningSvc *provisioning.Service, space did.DID) {
		account := testutil.Must(didmailto.New("alice@example.com"))(t)
		_, err := provisioningSvc.Provision(ctx, account, space, uploadService.DID(), testutil.RandomCID(t))
		require.NoError(t, err)
	}
	putPolicy := func(t *testing.T, deps *routingTestDeps) did.DID {
		policy := testutil.RandomDID(t)
		require.NoError(t, deps.router.PutPolicy(ctx, policy, []did.DID{registerNode(t, deps)}, testutil.RandomCID(t)))
		return policy
	}

	t.Run("sets the space policy", func(t *testing.T) {
		deps := newRoutingTestDeps(t)
		provisioningSvc := newProvisioning()
		handler := handlers.NewRoutingUseHandler(provisioningSvc, deps.router, logger)
		space := testutil.RandomIssuer(t)
		provision(t, provisioningSvc, space.DID())
		policy := putPolicy(t, deps)

		req, res := invokeRoutingUse(t, ctx, space, uploadService, &policy)
		require.NoError(t, handler.Handler(req, res))

		_, err := routingcmds.Use.Unpack(res.Receipt())
		require.NoError(t, err)

		rec, err := deps.policyStore.GetSpacePolicy(ctx, space.DID())
		require.NoError(t, err)
		require.Equal(t, policy, rec.Policy)
	})

	t.Run("clears the space policy", func(t *testing.T) {
		deps := newRoutingTestDeps(t)
		provisioningSvc := newProvisioning()
		handler := handlers.NewRoutingUseHandler(provisioningSvc, deps.router, logger)
		space := testutil.RandomIssuer(t)
		provision(t, provisioningSvc, space.DID())
		policy := putPolicy(t, deps)
		require.NoError(t, deps.router.UseSpacePolicy(ctx, space.DID(), policy, testutil.RandomCID(t)))

		req, res := invokeRoutingUse(t, ctx, space, uploadService, nil)
		require.NoError(t, handler.Handler(req, res))

		_, err := routingcmds.Use.Unpack(res.Receipt())
		require.NoError(t, err)

		_, err = deps.policyStore.GetSpacePolicy(ctx, space.DID())
		require.ErrorIs(t, err, routingpolicy.ErrSpacePolicyNotFound)
	})

	t.Run("rejects an unprovisioned space", func(t *testing.T) {
		deps := newRoutingTestDeps(t)
		handler := handlers.NewRoutingUseHandler(newProvisioning(), deps.router, logger)
		space := testutil.RandomIssuer(t)
		policy := putPolicy(t, deps)

		req, res := invokeRoutingUse(t, ctx, space, uploadService, &policy)
		require.NoError(t, handler.Handler(req, res))

		_, err := routingcmds.Use.Unpack(res.Receipt())
		var errModel datamodel.ErrorModel
		require.ErrorAs(t, err, &errModel)
		require.Equal(t, routingcmds.SpaceNotProvisionedErrorName, errModel.Name())
	})

	t.Run("rejects an unknown policy", func(t *testing.T) {
		deps := newRoutingTestDeps(t)
		provisioningSvc := newProvisioning()
		handler := handlers.NewRoutingUseHandler(provisioningSvc, deps.router, logger)
		space := testutil.RandomIssuer(t)
		provision(t, provisioningSvc, space.DID())
		unknown := testutil.RandomDID(t)

		req, res := invokeRoutingUse(t, ctx, space, uploadService, &unknown)
		require.NoError(t, handler.Handler(req, res))

		_, err := routingcmds.Use.Unpack(res.Receipt())
		var errModel datamodel.ErrorModel
		require.ErrorAs(t, err, &errModel)
		require.Equal(t, routingcmds.UnknownPolicyErrorName, errModel.Name())
	})
}
