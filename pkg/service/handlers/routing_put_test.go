package handlers_test

import (
	"context"
	"net/url"
	"testing"

	routingcmds "github.com/fil-forge/libforge/commands/routing"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	routing_policy_store "github.com/fil-forge/sprue/pkg/store/routing_policy/memory"
	storage_provider_store "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type routingTestDeps struct {
	router      *routing.Service
	spStore     *storage_provider_store.Store
	policyStore *routing_policy_store.Store
}

func newRoutingTestDeps(t *testing.T) *routingTestDeps {
	t.Helper()
	spStore := storage_provider_store.New()
	policyStore := routing_policy_store.New()
	return &routingTestDeps{
		router:      routing.NewService(spStore, policyStore, zaptest.NewLogger(t)),
		spStore:     spStore,
		policyStore: policyStore,
	}
}

// registerNode registers a storage provider with a non-zero weight and returns
// its DID.
func registerNode(t *testing.T, deps *routingTestDeps) did.DID {
	t.Helper()
	node := testutil.RandomDID(t)
	endpoint := testutil.Must(url.Parse("https://piri.example.com"))(t)
	require.NoError(t, deps.spStore.Put(t.Context(), node, *endpoint, 100, nil, container.New()))
	return node
}

func candidateSet(nodes ...did.DID) routingcmds.CandidateSet {
	entries := make(map[did.DID]routingcmds.Candidate, len(nodes))
	for _, n := range nodes {
		entries[n] = routingcmds.Candidate{}
	}
	return routingcmds.CandidateSet{Entries: entries}
}

// invokeRoutingPut builds a /routing/put invocation issued by the policy itself
// and returns the request + signed response.
func invokeRoutingPut(
	t *testing.T,
	ctx context.Context,
	policy ucan.Issuer,
	uploadService ucan.Issuer,
	candidates routingcmds.CandidateSet,
) (execution.Request, *execution.ExecResponse) {
	t.Helper()
	inv, err := routingcmds.Put.Invoke(
		policy,
		policy.DID(),
		&routingcmds.PutArguments{Candidates: candidates},
		invocation.WithAudience(uploadService.DID()),
	)
	require.NoError(t, err)
	req := execution.NewRequest(ctx, inv)
	res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithIssuer(uploadService))
	require.NoError(t, err)
	return req, res
}

func TestRoutingPutHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()
	uploadService := testutil.WebService

	t.Run("stores the candidate set", func(t *testing.T) {
		deps := newRoutingTestDeps(t)
		handler := handlers.NewRoutingPutHandler(deps.router, logger)
		nodeA, nodeB := registerNode(t, deps), registerNode(t, deps)
		policy := testutil.RandomIssuer(t)

		req, res := invokeRoutingPut(t, ctx, policy, uploadService, candidateSet(nodeA, nodeB))
		require.NoError(t, handler.Handler(req, res))

		_, err := routingcmds.Put.Unpack(res.Receipt())
		require.NoError(t, err)

		rec, err := deps.policyStore.GetPolicy(ctx, policy.DID())
		require.NoError(t, err)
		require.ElementsMatch(t, []did.DID{nodeA, nodeB}, rec.Candidates)
	})

	t.Run("replaces the candidate set", func(t *testing.T) {
		deps := newRoutingTestDeps(t)
		handler := handlers.NewRoutingPutHandler(deps.router, logger)
		nodeA, nodeB := registerNode(t, deps), registerNode(t, deps)
		policy := testutil.RandomIssuer(t)

		req, res := invokeRoutingPut(t, ctx, policy, uploadService, candidateSet(nodeA))
		require.NoError(t, handler.Handler(req, res))
		req, res = invokeRoutingPut(t, ctx, policy, uploadService, candidateSet(nodeB))
		require.NoError(t, handler.Handler(req, res))

		rec, err := deps.policyStore.GetPolicy(ctx, policy.DID())
		require.NoError(t, err)
		require.Equal(t, []did.DID{nodeB}, rec.Candidates)
	})

	t.Run("rejects an empty candidate set", func(t *testing.T) {
		deps := newRoutingTestDeps(t)
		handler := handlers.NewRoutingPutHandler(deps.router, logger)
		policy := testutil.RandomIssuer(t)

		req, res := invokeRoutingPut(t, ctx, policy, uploadService, candidateSet())
		require.NoError(t, handler.Handler(req, res))

		_, err := routingcmds.Put.Unpack(res.Receipt())
		var errModel datamodel.ErrorModel
		require.ErrorAs(t, err, &errModel)
		require.Equal(t, routingcmds.InvalidCandidatesErrorName, errModel.Name())
	})

	t.Run("rejects an unregistered candidate", func(t *testing.T) {
		deps := newRoutingTestDeps(t)
		handler := handlers.NewRoutingPutHandler(deps.router, logger)
		registered := registerNode(t, deps)
		policy := testutil.RandomIssuer(t)

		req, res := invokeRoutingPut(t, ctx, policy, uploadService, candidateSet(registered, testutil.RandomDID(t)))
		require.NoError(t, handler.Handler(req, res))

		_, err := routingcmds.Put.Unpack(res.Receipt())
		var errModel datamodel.ErrorModel
		require.ErrorAs(t, err, &errModel)
		require.Equal(t, routingcmds.InvalidCandidatesErrorName, errModel.Name())

		_, err = deps.policyStore.GetPolicy(ctx, policy.DID())
		require.Error(t, err)
	})
}
