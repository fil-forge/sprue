package handlers_test

import (
	"testing"

	"github.com/fil-forge/go-libstoracha/capabilities/space"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure/datamodel"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/lib/didmailto"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	consumermemory "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	subscriptionmemory "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestSpaceInfoHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	t.Run("returns providers for a provisioned space", func(t *testing.T) {
		provisioningSvc := provisioning.NewService(
			[]provisioning.ServiceDID{uploadService.DID()},
			consumermemory.New(),
			subscriptionmemory.New(),
		)

		handler := handlers.SpaceInfoHandler(provisioningSvc, logger)

		spaceSigner := testutil.RandomSigner(t)
		account := testutil.Must(didmailto.Parse("did:mailto:example.com:alice"))(t)

		_, err := provisioningSvc.Provision(ctx, account, spaceSigner.DID(), uploadService.DID(), testutil.RandomCID(t))
		require.NoError(t, err)

		caveats := space.InfoCaveats{}
		inv, err := space.Info.Invoke(testutil.Alice, uploadService, spaceSigner.DID().String(), caveats)
		require.NoError(t, err)

		cap := space.Info.New(spaceSigner.DID().String(), caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		ok, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.Equal(t, spaceSigner.DID().String(), ok.Did)
		require.Len(t, ok.Providers, 1)
		require.Equal(t, uploadService.DID().String(), ok.Providers[0])
	})

	t.Run("returns SpaceUnknown for unprovisioned space", func(t *testing.T) {
		provisioningSvc := provisioning.NewService(
			[]provisioning.ServiceDID{uploadService.DID()},
			consumermemory.New(),
			subscriptionmemory.New(),
		)

		handler := handlers.SpaceInfoHandler(provisioningSvc, logger)

		spaceSigner := testutil.RandomSigner(t)

		caveats := space.InfoCaveats{}
		inv, err := space.Info.Invoke(testutil.Alice, uploadService, spaceSigner.DID().String(), caveats)
		require.NoError(t, err)

		cap := space.Info.New(spaceSigner.DID().String(), caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)

		model := datamodel.Bind(testutil.Must(fail.ToIPLD())(t))
		require.NotNil(t, model.Name)
		require.Equal(t, handlers.SpaceUnknownErrorName, *model.Name)
	})

	t.Run("returns SpaceUnknown for non did:key space", func(t *testing.T) {
		provisioningSvc := provisioning.NewService(
			[]provisioning.ServiceDID{},
			consumermemory.New(),
			subscriptionmemory.New(),
		)

		handler := handlers.SpaceInfoHandler(provisioningSvc, logger)

		caveats := space.InfoCaveats{}
		inv, err := space.Info.Invoke(testutil.Alice, uploadService, "did:web:example.com", caveats)
		require.NoError(t, err)

		cap := space.Info.New("did:web:example.com", caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)

		model := datamodel.Bind(testutil.Must(fail.ToIPLD())(t))
		require.NotNil(t, model.Name)
		require.Equal(t, handlers.SpaceUnknownErrorName, *model.Name)
	})

	t.Run("invalid space DID", func(t *testing.T) {
		provisioningSvc := provisioning.NewService(
			[]provisioning.ServiceDID{},
			consumermemory.New(),
			subscriptionmemory.New(),
		)

		handler := handlers.SpaceInfoHandler(provisioningSvc, logger)

		caveats := space.InfoCaveats{}
		inv, err := space.Info.Invoke(testutil.Alice, uploadService, "not-a-did", caveats)
		require.NoError(t, err)

		cap := space.Info.New("not-a-did", caveats)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)
	})
}
