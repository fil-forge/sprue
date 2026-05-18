package handlers_test

import (
	"bytes"
	"context"
	"testing"

	blobcaps "github.com/fil-forge/libforge/capabilities/blob"
	"github.com/fil-forge/libforge/didmailto"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	blob_registry "github.com/fil-forge/sprue/pkg/store/blob_registry/memory"
	consumer_store "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	metrics_store "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	spacediff_store "github.com/fil-forge/sprue/pkg/store/space_diff/memory"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/principal"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func newBlobRegistry(t *testing.T) (*blob_registry.Store, *consumer_store.Store) {
	t.Helper()
	consumerStore := consumer_store.New()
	return blob_registry.New(
		spacediff_store.New(),
		consumerStore,
		metrics_store.NewSpaceStore(),
		metrics_store.New(),
	), consumerStore
}

// invokeBlobList builds the invocation/request/response trio used by every
// subtest below.
func invokeBlobList(
	t *testing.T,
	ctx context.Context,
	agent principal.Signer,
	uploadService principal.Signer,
	space principal.Signer,
	args *blobcaps.ListArguments,
) (execution.Request, *execution.ExecResponse) {
	t.Helper()
	inv, err := blobcaps.List.Invoke(
		agent,
		space.DID(),
		args,
		invocation.WithAudience(uploadService.DID()),
	)
	require.NoError(t, err)
	req := execution.NewRequest(ctx, inv)
	res, err := execution.NewResponse(req.Invocation().Task().Link(), execution.WithSigner(uploadService))
	require.NoError(t, err)
	return req, res
}

func TestBlobListHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService
	alice := testutil.Alice
	aliceAccount := testutil.Must(didmailto.New("alice@example.com"))(t)

	t.Run("empty list", func(t *testing.T) {
		blobReg, _ := newBlobRegistry(t)
		handler := handlers.NewBlobListHandler(blobReg, logger)

		space := testutil.RandomSigner(t)

		req, res := invokeBlobList(t, ctx, alice, uploadService, space, &blobcaps.ListArguments{})

		err := handler.Handler(req, res)
		require.NoError(t, err)

		o, x := res.Receipt().Out().Unpack()
		require.Nil(t, x)
		require.NotNil(t, o)

		var ok blobcaps.ListOK
		require.NoError(t, ok.UnmarshalCBOR(bytes.NewReader(o)))
		require.Empty(t, ok.Results)
	})

	t.Run("lists blobs", func(t *testing.T) {
		blobReg, consumerStore := newBlobRegistry(t)
		handler := handlers.NewBlobListHandler(blobReg, logger)

		space := testutil.RandomSigner(t)
		require.NoError(t, consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "sub-1", testutil.RandomCID(t)))

		blob1 := blobcaps.Blob{Digest: testutil.RandomMultihash(t), Size: 100}
		blob2 := blobcaps.Blob{Digest: testutil.RandomMultihash(t), Size: 200}
		require.NoError(t, blobReg.Register(ctx, space.DID(), blob1, testutil.RandomCID(t)))
		require.NoError(t, blobReg.Register(ctx, space.DID(), blob2, testutil.RandomCID(t)))

		req, res := invokeBlobList(t, ctx, alice, uploadService, space, &blobcaps.ListArguments{})

		err := handler.Handler(req, res)
		require.NoError(t, err)

		o, x := res.Receipt().Out().Unpack()
		require.Nil(t, x)
		var ok blobcaps.ListOK
		require.NoError(t, ok.UnmarshalCBOR(bytes.NewReader(o)))
		require.Len(t, ok.Results, 2)
	})

	t.Run("with size limit", func(t *testing.T) {
		blobReg, consumerStore := newBlobRegistry(t)
		handler := handlers.NewBlobListHandler(blobReg, logger)

		space := testutil.RandomSigner(t)
		require.NoError(t, consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "sub-1", testutil.RandomCID(t)))

		for i := range 3 {
			require.NoError(t, blobReg.Register(
				ctx, space.DID(),
				blobcaps.Blob{Digest: testutil.RandomMultihash(t), Size: uint64(i + 1)},
				testutil.RandomCID(t),
			))
		}

		size := uint64(2)
		req, res := invokeBlobList(t, ctx, alice, uploadService, space, &blobcaps.ListArguments{Size: &size})

		err := handler.Handler(req, res)
		require.NoError(t, err)

		o, x := res.Receipt().Out().Unpack()
		require.Nil(t, x)
		var ok blobcaps.ListOK
		require.NoError(t, ok.UnmarshalCBOR(bytes.NewReader(o)))
		require.Len(t, ok.Results, 2)
		require.NotNil(t, ok.Cursor)
	})

	t.Run("with cursor pagination", func(t *testing.T) {
		blobReg, consumerStore := newBlobRegistry(t)
		handler := handlers.NewBlobListHandler(blobReg, logger)

		space := testutil.RandomSigner(t)
		require.NoError(t, consumerStore.Add(ctx, uploadService.DID(), space.DID(), aliceAccount, "sub-1", testutil.RandomCID(t)))

		for i := range 3 {
			require.NoError(t, blobReg.Register(
				ctx, space.DID(),
				blobcaps.Blob{Digest: testutil.RandomMultihash(t), Size: uint64(i + 1)},
				testutil.RandomCID(t),
			))
		}

		size := uint64(1)
		req1, res1 := invokeBlobList(t, ctx, alice, uploadService, space, &blobcaps.ListArguments{Size: &size})
		require.NoError(t, handler.Handler(req1, res1))

		o1, x := res1.Receipt().Out().Unpack()
		require.Nil(t, x)
		var ok1 blobcaps.ListOK
		require.NoError(t, ok1.UnmarshalCBOR(bytes.NewReader(o1)))
		require.Len(t, ok1.Results, 1)
		require.NotNil(t, ok1.Cursor)

		// Second page using cursor.
		cursor := *ok1.Cursor
		req2, res2 := invokeBlobList(t, ctx, alice, uploadService, space, &blobcaps.ListArguments{Cursor: &cursor, Size: &size})
		require.NoError(t, handler.Handler(req2, res2))

		o2, x := res2.Receipt().Out().Unpack()
		require.Nil(t, x)
		var ok2 blobcaps.ListOK
		require.NoError(t, ok2.UnmarshalCBOR(bytes.NewReader(o2)))
		require.Len(t, ok2.Results, 1)
		require.NotEqual(t, ok1.Results[0].Blob.Digest.HexString(), ok2.Results[0].Blob.Digest.HexString())
	})

	t.Run("does not list blobs from other spaces", func(t *testing.T) {
		blobReg, consumerStore := newBlobRegistry(t)
		handler := handlers.NewBlobListHandler(blobReg, logger)

		space1 := testutil.RandomSigner(t)
		space2 := testutil.RandomSigner(t)
		require.NoError(t, consumerStore.Add(ctx, uploadService.DID(), space1.DID(), aliceAccount, "sub-1", testutil.RandomCID(t)))

		require.NoError(t, blobReg.Register(
			ctx, space1.DID(),
			blobcaps.Blob{Digest: testutil.RandomMultihash(t), Size: 100},
			testutil.RandomCID(t),
		))

		// Query space2 — should be empty.
		req, res := invokeBlobList(t, ctx, alice, uploadService, space2, &blobcaps.ListArguments{})
		require.NoError(t, handler.Handler(req, res))

		o, x := res.Receipt().Out().Unpack()
		require.Nil(t, x)
		var ok blobcaps.ListOK
		require.NoError(t, ok.UnmarshalCBOR(bytes.NewReader(o)))
		require.Empty(t, ok.Results)
	})
}
