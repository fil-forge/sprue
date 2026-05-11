package ucan_server

import (
	"context"
	"fmt"

	"github.com/fil-forge/libforge/capabilities/ucan/attest"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/principal"
	ed_verifier "github.com/fil-forge/ucantone/principal/ed25519/verifier"
	secp_verifier "github.com/fil-forge/ucantone/principal/secp256k1/verifier"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/validator"
)

// PrincipalParser is a [validator.PrincipalParserFunc] that enables support for
// both ed25519 and secp256k1 principals in UCANs.
func PrincipalParser(str string) (principal.Verifier, error) {
	if v, err := ed_verifier.Parse(str); err == nil {
		return v, nil
	}
	if v, err := secp_verifier.Parse(str); err == nil {
		return v, nil
	}
	return nil, fmt.Errorf("unknown principal type: %s", str)
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
			if inv.Command() != attest.ProofCommand {
				continue
			}
			// only trust attestations we issued
			if inv.Issuer().DID() != authority.DID() || inv.Subject() == nil || inv.Subject().DID() != authority.DID() {
				continue
			}
			args := attest.ProofArguments{}
			err := datamodel.Rebind(datamodel.NewAny(inv.Arguments()), &args)
			if err != nil {
				continue
			}
			// make sure the attestation is for the delegation in question
			if args.Proof != dlg.Link() {
				continue
			}
			// finally, make sure the signature is valid
			ok, err := invocation.VerifySignature(inv, authority)
			if !ok || err != nil {
				continue
			}
			return nil
		}
		return fmt.Errorf("no valid attestation found for delegation")
	}
}
