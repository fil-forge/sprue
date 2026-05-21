package didmailto

import (
	"bytes"
	"context"
	"fmt"

	"github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"

	cmdattest "github.com/fil-forge/libforge/commands/ucan/attest"

	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/validator"
)

func NewDIDMailtoResolver(authority ucan.Verifier) validator.DIDVerifierResolverFunc {
	return func(ctx context.Context, did did.DID) (ucan.Verifier, error) {
		if did.Method() != "mailto" {
			return nil, fmt.Errorf("mailto resolver cannot resolve non-mailto DID %s", did)
		}

		return DIDMailtoVerifier{
			ctx:       ctx,
			authority: authority,
			did:       did,
		}, nil
	}
}

type DIDMailtoVerifier struct {
	ctx       context.Context
	authority ucan.Verifier
	did       did.DID
}

func (v DIDMailtoVerifier) DID() did.DID {
	return v.did
}

func (v DIDMailtoVerifier) Verify(msg []byte, sig []byte) bool {
	inv, err := invocation.Decode(sig)
	if err != nil {
		return false
	}

	var args cmdattest.ProofArguments
	err = args.UnmarshalCBOR(bytes.NewReader(inv.ArgumentsBytes()))
	if err != nil {
		return false
	}

	msgDigest, err := mh.Sum(msg, mh.SHA2_256, -1)
	if err != nil {
		return false
	}

	if args.Proof != cid.NewCidV1(cid.Raw, msgDigest) {
		return false
	}

	if inv.Subject() != v.authority.DID() {
		return false
	}

	if validator.ValidateInvocation(v.ctx, inv) != nil {
		return false
	}

	return true
}
