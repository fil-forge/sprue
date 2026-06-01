package attested_test

import (
	"bytes"
	"testing"

	"github.com/fil-forge/libforge/commands/ucan/attest"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/attested"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/ucan/command"
	"github.com/fil-forge/ucantone/ucan/delegation"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"
)

func TestSigner(t *testing.T) {
	authority := testutil.RandomSigner(t)
	alice, err := did.Parse("did:mailto:example.com:alice")
	if err != nil {
		t.Fatalf("failed to parse DID: %v", err)
	}

	signer := attested.NewSigner(alice, authority)

	del, err := delegation.Delegate(
		signer,
		testutil.RandomDID(t),
		signer.DID(),
		command.MustParse("/example/command"),
	)
	require.NoError(t, err)

	t.Run("signs data correctly", func(t *testing.T) {
		require.Equal(t, del.Signature().Header().SignatureAlgorithm().Code(), attested.Code)
		sigBytes := del.Signature().Bytes()
		require.NotEmpty(t, sigBytes)

		inv, err := invocation.Decode(sigBytes)
		require.NoError(t, err)

		require.Equal(t, authority.DID(), inv.Issuer())
		require.Equal(t, did.Undef, inv.Audience())
		require.Equal(t, authority.DID(), inv.Subject())
		require.Equal(t, attest.Proof.Command, inv.Command())

		msgDigest, err := mh.Sum(del.SignedBytes(), mh.SHA2_256, -1)
		require.NoError(t, err)
		var proofArgs attest.ProofArguments
		err = proofArgs.UnmarshalCBOR(bytes.NewReader(inv.ArgumentsBytes()))
		require.NoError(t, err)
		require.Equal(t, attest.ProofArguments{Proof: cid.NewCidV1(cid.Raw, msgDigest)}, proofArgs)
	})

	t.Run("delegation round-trips through CBOR and verifies", func(t *testing.T) {
		encoded, err := delegation.Encode(del)
		require.NoError(t, err)

		decoded, err := delegation.Decode(encoded)
		require.NoError(t, err)

		resolver := attested.NewDIDVerifierResolver(authority.Verifier())
		v, err := resolver(t.Context(), alice)
		require.NoError(t, err)

		require.True(t, v.Verify(decoded.SignedBytes(), decoded.Signature().Bytes()))
	})
}
