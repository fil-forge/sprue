package ucan_server

import (
	"bytes"
	"context"
	"fmt"

	"github.com/fil-forge/libforge/commands/ucan/attest"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/principal"
	secp256k1_verifier "github.com/fil-forge/ucantone/principal/secp256k1/verifier"
	"github.com/fil-forge/ucantone/principal/verifier"
	"github.com/fil-forge/ucantone/ucan"
	ucan_token "github.com/fil-forge/ucantone/ucan/token"
	"github.com/fil-forge/ucantone/validator"
)

func init() {
	verifier.Register(secp256k1_verifier.Code, func(b []byte) (principal.Verifier, error) {
		return secp256k1_verifier.Decode(b)
	})
}

func ResolveDIDKey(ctx context.Context, did did.DID) (ucan.Verifier, error) {
	return verifier.FromDIDKey(did)
}

// NewAttestationVerifier creates a [validator.NonStandardSignatureVerifierFunc]
// that validates that a delegation is attested by the given authority.
func NewAttestationVerifier(authority principal.Verifier) validator.NonStandardSignatureVerifierFunc {
	return func(ctx context.Context, token ucan.Token, meta ucan.Container) error {
		// We only support attestations as delegations - attested delegation MUST
		// delegate to an agent DID which is then used in the invocation.
		dlg, ok := token.(ucan.Delegation)
		if !ok {
			return fmt.Errorf("token is not a delegation")
		}
		for _, inv := range meta.Invocations() {
			if inv.Command() != ucan.Command(attest.Proof) {
				continue
			}
			// only trust attestations we issued
			if inv.Issuer() != authority.DID() || inv.Subject() == did.Undef || inv.Subject() != authority.DID() {
				continue
			}
			var args attest.ProofArguments
			if err := args.UnmarshalCBOR(bytes.NewReader(inv.ArgumentsBytes())); err != nil {
				continue
			}
			// make sure the attestation is for the delegation in question
			if args.Proof != dlg.Link() {
				continue
			}
			// finally, make sure the signature is valid
			ok, err := ucan_token.VerifySignature(inv, authority)
			if !ok || err != nil {
				continue
			}
			return nil
		}
		return fmt.Errorf("no valid attestation found for delegation")
	}
}
