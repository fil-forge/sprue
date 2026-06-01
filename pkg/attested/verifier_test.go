package attested_test

import (
	"testing"

	"github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"

	"github.com/fil-forge/libforge/commands/ucan/attest"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/attested"
	"github.com/fil-forge/ucantone/did"
)

func TestNewDIDVerifierResolver(t *testing.T) {
	authority := testutil.RandomSigner(t)
	resolver := attested.NewDIDVerifierResolver(authority.Verifier())
	require.NotNil(t, resolver)
	didWeb, err := did.Parse("did:web:example.com")
	require.NoError(t, err)
	didMailto, err := did.Parse("did:mailto:example.com:alice")
	require.NoError(t, err)

	t.Run("cannot resolve a non-mailto DID", func(t *testing.T) {
		v, err := resolver(t.Context(), didWeb)
		require.Nil(t, v)
		require.ErrorContains(t, err, "mailto resolver cannot resolve non-mailto DID did:web:example.com")
	})

	t.Run("resolves a mailto DID to a verifier which", func(t *testing.T) {
		v, err := resolver(t.Context(), didMailto)
		require.NoError(t, err)
		require.NotNil(t, v)

		msg := []byte("hello world")
		digest, err := mh.Sum(msg, mh.SHA2_256, -1)
		require.NoError(t, err)

		t.Run("verifies a correct signature from the authority", func(t *testing.T) {
			sigInv, err := attest.Proof.Invoke(
				authority,
				authority.DID(),
				&attest.ProofArguments{
					Proof: cid.NewCidV1(cid.Raw, digest),
				},
			)
			require.NoError(t, err)
			require.NotNil(t, sigInv)

			verified := v.Verify(msg, sigInv.Bytes())
			require.True(t, verified)
		})

		t.Run("rejects an incorrect signature", func(t *testing.T) {
			t.Run("(bad format)", func(t *testing.T) {
				badSig := []byte("this is not a valid signature")
				verified := v.Verify(msg, badSig)
				require.False(t, verified)
			})

			t.Run("(wrong digest)", func(t *testing.T) {
				wrongDigest := testutil.RandomMultihash(t)

				sigInv, err := attest.Proof.Invoke(
					authority,
					authority.DID(),
					&attest.ProofArguments{
						Proof: cid.NewCidV1(cid.Raw, wrongDigest),
					},
				)
				require.NoError(t, err)
				require.NotNil(t, sigInv)

				verified := v.Verify(msg, sigInv.Bytes())
				require.False(t, verified)
			})

			t.Run("(wrong authority)", func(t *testing.T) {
				wrongAuthority := testutil.RandomSigner(t)

				sigInv, err := attest.Proof.Invoke(
					wrongAuthority,
					wrongAuthority.DID(),
					&attest.ProofArguments{
						Proof: cid.NewCidV1(cid.Raw, digest),
					},
				)
				require.NoError(t, err)
				require.NotNil(t, sigInv)

				verified := v.Verify(msg, sigInv.Bytes())
				require.False(t, verified)
			})

			t.Run("(invalid invocation)", func(t *testing.T) {
				rando := testutil.RandomSigner(t)

				sigInv, err := attest.Proof.Invoke(
					rando,
					authority.DID(),
					&attest.ProofArguments{
						Proof: cid.NewCidV1(cid.Raw, digest),
					},
				)
				require.NoError(t, err)
				require.NotNil(t, sigInv)

				verified := v.Verify(msg, sigInv.Bytes())
				require.False(t, verified)
			})
		})
	})
}
