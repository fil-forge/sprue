package handlers_test

import (
	"testing"

	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure/datamodel"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/capabilities/admin/provider"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/service/handlers"
	storage_provider_store "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// issueRegisterInvocation creates an admin/provider/register invocation with
// the proof delegation blocks attached.
func issueRegisterInvocation(
	t *testing.T,
	issuer ucan.Signer,
	audience ucan.Principal,
	caveats provider.RegisterCaveats,
	proof delegation.Delegation,
) (ucan.Capability[provider.RegisterCaveats], invocation.Invocation) {
	t.Helper()

	inv, err := provider.Register.Invoke(
		issuer, audience,
		audience.DID().String(),
		caveats,
	)
	require.NoError(t, err)

	// Attach proof delegation blocks to the invocation
	for blk, err := range proof.Blocks() {
		require.NoError(t, err)
		require.NoError(t, inv.Attach(blk))
	}

	cap := provider.Register.New(
		audience.DID().String(),
		caveats,
	)

	return cap, inv
}

func TestAdminProviderRegisterHandler(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := t.Context()

	uploadService := testutil.WebService

	t.Run("unauthorized issuer", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.AdminProviderRegisterHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)
		unauthorizedIssuer := testutil.RandomSigner(t)

		// Create a proof delegation from storageProvider to uploadService
		proof, err := delegation.Delegate(
			storageProvider, uploadService,
			[]ucan.Capability[ucan.NoCaveats]{
				ucan.NewCapability("blob/allocate", storageProvider.DID().String(), ucan.NoCaveats{}),
			},
		)
		require.NoError(t, err)

		caveats := provider.RegisterCaveats{
			Endpoint: "https://piri.example.com",
			Proof:    proof.Link(),
		}

		// Issuer is neither the service nor the provider
		cap, inv := issueRegisterInvocation(t, unauthorizedIssuer, uploadService, caveats, proof)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		_, fail := result.Unwrap(res)
		require.NotNil(t, fail)

		model := datamodel.Bind(testutil.Must(fail.ToIPLD())(t))
		require.NotNil(t, model.Name)
		require.Equal(t, "Unauthorized", *model.Name)
	})

	t.Run("provider already registered", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.AdminProviderRegisterHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)

		// Create a proof delegation
		proof, err := delegation.Delegate(
			storageProvider, uploadService,
			[]ucan.Capability[ucan.NoCaveats]{
				ucan.NewCapability("blob/allocate", storageProvider.DID().String(), ucan.NoCaveats{}),
			},
		)
		require.NoError(t, err)

		caveats := provider.RegisterCaveats{
			Endpoint: "https://piri.example.com",
			Proof:    proof.Link(),
		}

		// First registration by service identity (authorized)
		cap, inv := issueRegisterInvocation(t, uploadService, uploadService, caveats, proof)
		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		o, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.NotNil(t, o)

		// Second registration should fail
		cap2, inv2 := issueRegisterInvocation(t, uploadService, uploadService, caveats, proof)
		res2, _, err := handler(ctx, cap2, inv2, nil)
		require.NoError(t, err)

		_, fail2 := result.Unwrap(res2)
		require.NotNil(t, fail2)

		model := datamodel.Bind(testutil.Must(fail2.ToIPLD())(t))
		require.NotNil(t, model.Name)
		require.Equal(t, "ProviderAlreadyRegistered", *model.Name)
	})

	t.Run("service identity can register", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.AdminProviderRegisterHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)

		proof, err := delegation.Delegate(
			storageProvider, uploadService,
			[]ucan.Capability[ucan.NoCaveats]{
				ucan.NewCapability("blob/allocate", storageProvider.DID().String(), ucan.NoCaveats{}),
			},
		)
		require.NoError(t, err)

		caveats := provider.RegisterCaveats{
			Endpoint: "https://piri.example.com",
			Proof:    proof.Link(),
		}

		cap, inv := issueRegisterInvocation(t, uploadService, uploadService, caveats, proof)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		o, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.NotNil(t, o)

		// Verify provider was stored
		rec, err := spStore.Get(ctx, storageProvider.DID())
		require.NoError(t, err)
		require.Equal(t, "https://piri.example.com", rec.Endpoint.String())
	})

	t.Run("provider itself can register", func(t *testing.T) {
		spStore := storage_provider_store.New()

		handler := handlers.AdminProviderRegisterHandler(
			&identity.Identity{Signer: uploadService}, spStore, logger,
		)

		storageProvider := testutil.RandomSigner(t)

		proof, err := delegation.Delegate(
			storageProvider, uploadService,
			[]ucan.Capability[ucan.NoCaveats]{
				ucan.NewCapability("blob/allocate", storageProvider.DID().String(), ucan.NoCaveats{}),
			},
		)
		require.NoError(t, err)

		caveats := provider.RegisterCaveats{
			Endpoint: "https://piri.example.com",
			Proof:    proof.Link(),
		}

		// Issued by the provider itself
		cap, inv := issueRegisterInvocation(t, storageProvider, uploadService, caveats, proof)

		res, _, err := handler(ctx, cap, inv, nil)
		require.NoError(t, err)

		o, fail := result.Unwrap(res)
		require.Nil(t, fail)
		require.NotNil(t, o)

		rec, err := spStore.Get(ctx, storageProvider.DID())
		require.NoError(t, err)
		require.Equal(t, "https://piri.example.com", rec.Endpoint.String())
	})
}
