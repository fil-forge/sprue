package ucan_server

import (
	"context"

	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/principal"
	secp256k1_verifier "github.com/fil-forge/ucantone/principal/secp256k1/verifier"
	"github.com/fil-forge/ucantone/principal/verifier"
	"github.com/fil-forge/ucantone/ucan"
)

func init() {
	verifier.Register(secp256k1_verifier.Code, func(b []byte) (principal.Verifier, error) {
		return secp256k1_verifier.Decode(b)
	})
}

func ResolveDIDKey(ctx context.Context, did did.DID) (ucan.Verifier, error) {
	return verifier.FromDIDKey(did)
}
