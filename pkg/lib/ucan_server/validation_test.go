package ucan_server

import (
	"testing"

	"github.com/fil-forge/libforge/capabilities/ucan/attest"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/principal/absentee"
	"github.com/fil-forge/ucantone/principal/ed25519"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/delegation"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
)

func TestNewAttestationVerifier(t *testing.T) {
	authority := testutil.WebService
	agent := testutil.Alice
	space := testutil.Must(ed25519.Generate())(t)
	other := testutil.Must(ed25519.Generate())(t)

	account := absentee.From(testutil.Must(did.Parse("did:mailto:web.mail:alice"))(t))

	dlg, err := delegation.Delegate(account, agent.DID(), space.DID(), "/blob/add")
	require.NoError(t, err)

	verify := NewAttestationVerifier(authority.Verifier())

	t.Run("token is not a delegation", func(t *testing.T) {
		inv, err := invocation.Invoke(agent, space.DID(), "/blob/add", datamodel.Map{})
		require.NoError(t, err)

		err = verify(t.Context(), inv, container.New())
		require.Error(t, err)
		require.Contains(t, err.Error(), "not a delegation")
	})

	t.Run("no attestations in container", func(t *testing.T) {
		err := verify(t.Context(), dlg, container.New())
		require.Error(t, err)
		require.Contains(t, err.Error(), "no valid attestation")
	})

	t.Run("invocation with non-attest command is ignored", func(t *testing.T) {
		inv, err := invocation.Invoke(authority, authority.DID(), "/some/other", datamodel.Map{})
		require.NoError(t, err)

		err = verify(t.Context(), dlg, container.New(container.WithInvocations(inv)))
		require.Error(t, err)
		require.Contains(t, err.Error(), "no valid attestation")
	})

	t.Run("attestation issued by non-authority is ignored", func(t *testing.T) {
		inv, err := attest.Proof.Invoke(other, other.DID(), &attest.ProofArguments{Proof: dlg.Link()})
		require.NoError(t, err)

		err = verify(t.Context(), dlg, container.New(container.WithInvocations(inv)))
		require.Error(t, err)
		require.Contains(t, err.Error(), "no valid attestation")
	})

	t.Run("attestation with subject other than authority is ignored", func(t *testing.T) {
		inv, err := attest.Proof.Invoke(authority, other.DID(), &attest.ProofArguments{Proof: dlg.Link()})
		require.NoError(t, err)

		err = verify(t.Context(), dlg, container.New(container.WithInvocations(inv)))
		require.Error(t, err)
		require.Contains(t, err.Error(), "no valid attestation")
	})

	t.Run("attestation proof for a different delegation is ignored", func(t *testing.T) {
		otherDlg, err := delegation.Delegate(account, agent.DID(), space.DID(), "/blob/list")
		require.NoError(t, err)

		inv, err := attest.Proof.Invoke(authority, authority.DID(), &attest.ProofArguments{Proof: otherDlg.Link()})
		require.NoError(t, err)

		err = verify(t.Context(), dlg, container.New(container.WithInvocations(inv)))
		require.Error(t, err)
		require.Contains(t, err.Error(), "no valid attestation")
	})

	t.Run("attestation with malformed arguments is ignored", func(t *testing.T) {
		inv, err := invocation.Invoke(
			authority,
			authority.DID(),
			attest.ProofCommand,
			datamodel.Map{"unrelated": "foo"},
		)
		require.NoError(t, err)

		err = verify(t.Context(), dlg, container.New(container.WithInvocations(inv)))
		require.Error(t, err)
		require.Contains(t, err.Error(), "no valid attestation")
	})

	t.Run("valid attestation passes verification", func(t *testing.T) {
		inv, err := attest.Proof.Invoke(authority, authority.DID(), &attest.ProofArguments{Proof: dlg.Link()})
		require.NoError(t, err)

		err = verify(t.Context(), dlg, container.New(container.WithInvocations(inv)))
		require.NoError(t, err)
	})

	t.Run("valid attestation found among invalid ones", func(t *testing.T) {
		untrusted, err := attest.Proof.Invoke(other, other.DID(), &attest.ProofArguments{Proof: dlg.Link()})
		require.NoError(t, err)
		wrongCmd, err := invocation.Invoke(authority, authority.DID(), "/some/other", datamodel.Map{})
		require.NoError(t, err)
		valid, err := attest.Proof.Invoke(authority, authority.DID(), &attest.ProofArguments{Proof: dlg.Link()})
		require.NoError(t, err)

		err = verify(
			t.Context(),
			dlg,
			container.New(container.WithInvocations(untrusted, wrongCmd, valid)),
		)
		require.NoError(t, err)
	})
}
