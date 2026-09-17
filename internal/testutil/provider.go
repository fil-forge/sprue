package testutil

import (
	"crypto/ed25519"
	"net/http/httptest"
	"testing"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/did/key"
	"github.com/fil-forge/ucantone/did/resolver"
	"github.com/fil-forge/ucantone/multikey"
	ed25519signer "github.com/fil-forge/ucantone/multikey/ed25519"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/delegation"
	"github.com/fil-forge/ucantone/validator"
	"github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"
)

// ProviderProofs builds the proof container a storage provider grants the
// upload service at registration: self-issued delegations (subject = provider)
// authorizing `/blob/allocate` and `/blob/accept`.
func ProviderProofs(t *testing.T, storageProvider, uploadService ucan.Issuer) ucan.Container {
	t.Helper()
	// No expiration: delegations default to 30 seconds, which a test slow
	// enough to outlive them would fail against in a way that looks like a
	// protocol bug rather than a stale fixture.
	allocProof := Must(blobcmds.Allocate.Delegate(storageProvider, uploadService.DID(), storageProvider.DID(), delegation.WithNoExpiration()))(t)
	acceptProof := Must(blobcmds.Accept.Delegate(storageProvider, uploadService.DID(), storageProvider.DID(), delegation.WithNoExpiration()))(t)
	return container.New(container.WithDelegations(allocProof, acceptProof))
}

// NewMockPiriServer stands up a UCAN HTTP server that handles /blob/allocate &
// /blob/accept by returning the canned responses. Wraps the upload service's
// did:web identity so signatures verify against the underlying did:key.
func NewMockPiriServer(
	t *testing.T,
	storageProvider ucan.Issuer,
	uploadService identity.Identity,
	allocateOK *blobcmds.AllocateOK,
	acceptOK *blobcmds.AcceptOK,
) *httptest.Server {
	t.Helper()

	srv := server.NewHTTP(
		storageProvider,
		server.WithValidationOptions(validator.WithDIDResolver(resolver.Tiered{
			resolver.WellKnown{uploadService.DID(): Must(uploadService.DIDDocument())(t)},
			key.Resolver,
		})),
	)

	srv.Handle(blobcmds.Allocate.Command, blobcmds.Allocate.Handler(func(
		req *binding.Request[*blobcmds.AllocateArguments],
		res *binding.Response[*blobcmds.AllocateOK],
	) error {
		return res.SetSuccess(allocateOK)
	}))

	srv.Handle(blobcmds.Accept.Command, blobcmds.Accept.Handler(func(
		req *binding.Request[*blobcmds.AcceptArguments],
		res *binding.Response[*blobcmds.AcceptOK],
	) error {
		return res.SetSuccess(acceptOK)
	}))

	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)
	return httpSrv
}

// DeriveBlobProvider mirrors the production handler's logic for deriving a
// signer from a blob's digest, used to sign /http/put invocations and receipts.
func DeriveBlobProvider(t *testing.T, digest multihash.Multihash) ucan.Issuer {
	t.Helper()
	require.GreaterOrEqual(t, len(digest), ed25519.SeedSize)
	seed := digest[len(digest)-ed25519.SeedSize:]
	s, err := ed25519signer.FromRaw(seed)
	require.NoError(t, err)
	return multikey.KeyIssuer(s)
}
