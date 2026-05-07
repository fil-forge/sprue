package handlers_test

import (
	"testing"

	spaceindexcap "github.com/fil-forge/go-libstoracha/capabilities/space/index"
	"github.com/fil-forge/go-libstoracha/capabilities/types"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure/datamodel"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/lib/didmailto"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	subscription_store "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	ipldprime "github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/ipld/go-ipld-prime/node/basicnode"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestSpaceIndexAddHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	alice := testutil.Alice
	aliceAccount := testutil.Must(didmailto.Parse("did:mailto:example.com:alice"))(t)
	uploadService := testutil.WebService

	t.Run("invalid space DID", func(t *testing.T) {
		consumerStore := consumer_store.New()
		subscriptionStore := subscription_store.New()
		provisioningSvc := provisioning.NewService(nil, consumerStore, subscriptionStore)
		blobReg, _ := newBlobRegistry()

		handler := handlers.SpaceIndexAddHandler(provisioningSvc, blobReg, nil, logger)

		index := cidlink.Link{Cid: testutil.RandomCID(t)}
		content := cidlink.Link{Cid: testutil.RandomCID(t)}
		cap := ucan.NewCapability(
			spaceindexcap.AddAbility,
			"not-a-did",
			spaceindexcap.AddCaveats{Index: index, Content: content},
		)

		inv, err := invocation.Invoke(alice, uploadService, cap)
		require.NoError(t, err)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)

		model := datamodel.Bind(testutil.Must(fail.ToIPLD())(t))
		require.NotNil(t, model.Name)
		require.Equal(t, handlers.InvalidSpaceErrorName, *model.Name)
	})

	t.Run("no service providers", func(t *testing.T) {
		consumerStore := consumer_store.New()
		subscriptionStore := subscription_store.New()
		provisioningSvc := provisioning.NewService(nil, consumerStore, subscriptionStore)
		blobReg, _ := newBlobRegistry()

		handler := handlers.SpaceIndexAddHandler(provisioningSvc, blobReg, nil, logger)

		space := testutil.RandomSigner(t)
		index := cidlink.Link{Cid: testutil.RandomCID(t)}
		content := cidlink.Link{Cid: testutil.RandomCID(t)}
		cap := ucan.NewCapability(
			spaceindexcap.AddAbility,
			space.DID().String(),
			spaceindexcap.AddCaveats{Index: index, Content: content},
		)

		inv, err := invocation.Invoke(alice, uploadService, cap)
		require.NoError(t, err)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)

		model := datamodel.Bind(testutil.Must(fail.ToIPLD())(t))
		require.NotNil(t, model.Name)
		require.Equal(t, handlers.InsufficientStorageErrorName, *model.Name)
	})

	t.Run("index not found in space", func(t *testing.T) {
		consumerStore := consumer_store.New()
		subscriptionStore := subscription_store.New()
		provisioningSvc := provisioning.NewService(nil, consumerStore, subscriptionStore)
		blobReg, _ := newBlobRegistry()

		// provision the space
		err := consumerStore.Add(ctx, uploadService.DID(), testutil.RandomSigner(t).DID(), aliceAccount, "", testutil.RandomCID(t))
		require.NoError(t, err)

		space := testutil.RandomSigner(t)
		err = consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "", testutil.RandomCID(t))
		require.NoError(t, err)

		handler := handlers.SpaceIndexAddHandler(provisioningSvc, blobReg, nil, logger)

		index := cidlink.Link{Cid: testutil.RandomCID(t)}
		content := cidlink.Link{Cid: testutil.RandomCID(t)}
		cap := ucan.NewCapability(
			spaceindexcap.AddAbility,
			space.DID().String(),
			spaceindexcap.AddCaveats{Index: index, Content: content},
		)

		inv, err := invocation.Invoke(alice, uploadService, cap)
		require.NoError(t, err)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)

		model := datamodel.Bind(testutil.Must(fail.ToIPLD())(t))
		require.NotNil(t, model.Name)
		require.Equal(t, handlers.IndexNotFoundErrorName, *model.Name)
	})

	t.Run("missing retrieval auth fact", func(t *testing.T) {
		blobReg, consumerSt := newBlobRegistry()
		subscriptionStore := subscription_store.New()
		provisioningSvc := provisioning.NewService(nil, consumerSt, subscriptionStore)

		space := testutil.RandomSigner(t)
		err := consumerSt.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "", testutil.RandomCID(t))
		require.NoError(t, err)

		// register the index blob in the space
		indexCID := testutil.RandomCID(t)
		indexBlob := types.Blob{Digest: indexCID.Hash(), Size: 512}
		err = blobReg.Register(ctx, space.DID(), indexBlob, testutil.RandomCID(t))
		require.NoError(t, err)

		handler := handlers.SpaceIndexAddHandler(provisioningSvc, blobReg, nil, logger)

		index := cidlink.Link{Cid: indexCID}
		content := cidlink.Link{Cid: testutil.RandomCID(t)}
		cap := ucan.NewCapability(
			spaceindexcap.AddAbility,
			space.DID().String(),
			spaceindexcap.AddCaveats{Index: index, Content: content},
		)

		// invocation without retrievalAuth fact
		inv, err := invocation.Invoke(alice, uploadService, cap)
		require.NoError(t, err)

		_, _, err = handler(ctx, cap, inv, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "retrievalAuth")
	})

	t.Run("retrieval auth fact present", func(t *testing.T) {
		blobReg, consumerSt := newBlobRegistry()
		subscriptionStore := subscription_store.New()
		provisioningSvc := provisioning.NewService(nil, consumerSt, subscriptionStore)

		space := testutil.RandomSigner(t)
		err := consumerSt.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "", testutil.RandomCID(t))
		require.NoError(t, err)

		// register the index blob in the space
		indexCID := testutil.RandomCID(t)
		indexBlob := types.Blob{Digest: indexCID.Hash(), Size: 512}
		err = blobReg.Register(ctx, space.DID(), indexBlob, testutil.RandomCID(t))
		require.NoError(t, err)

		handler := handlers.SpaceIndexAddHandler(provisioningSvc, blobReg, nil, logger)

		index := cidlink.Link{Cid: indexCID}
		content := cidlink.Link{Cid: testutil.RandomCID(t)}
		cap := ucan.NewCapability(
			spaceindexcap.AddAbility,
			space.DID().String(),
			spaceindexcap.AddCaveats{Index: index, Content: content},
		)

		// create a retrieval auth delegation to include as a fact
		retrievalAuth, err := delegation.Delegate(
			alice,
			uploadService,
			[]ucan.Capability[ucan.NoCaveats]{
				ucan.NewCapability("space/content/retrieve", space.DID().String(), ucan.NoCaveats{}),
			},
		)
		require.NoError(t, err)

		inv, err := invocation.Invoke(alice, uploadService, cap,
			delegation.WithFacts([]ucan.FactBuilder{
				retrievalAuthFact{link: retrievalAuth.Link()},
			}),
			delegation.WithProof(delegation.FromDelegation(retrievalAuth)),
		)
		require.NoError(t, err)

		// handler will fail at indexerClient.PublishIndexClaim since client is nil,
		// but it should get past the extractRetrievalAuth step
		_, _, err = handler(ctx, cap, inv, nil)
		require.Error(t, err)
		// should NOT be a retrievalAuth error
		require.NotContains(t, err.Error(), "retrievalAuth")
	})
}

// retrievalAuthFact implements ucan.FactBuilder for test invocations.
type retrievalAuthFact struct {
	link ucan.Link
}

func (f retrievalAuthFact) ToIPLD() (map[string]ipldprime.Node, error) {
	return map[string]ipldprime.Node{
		"retrievalAuth": basicnode.NewLink(f.link),
	}, nil
}
