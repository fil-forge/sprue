package ucan_server

// import (
// 	"context"

// 	"github.com/fil-forge/ucantone/did"
// 	"github.com/fil-forge/ucantone/ucan"
// 	secp256k1_verifier "github.com/fil-forge/ucantone/multikey/secp256k1/verifier"
// 	"github.com/fil-forge/ucantone/multikey/verifier"
// )

// func init() {
// 	verifier.Register(secp256k1_verifier.Code, func(b []byte) (verifier.Verifier, error) {
// 		return secp256k1_verifier.Decode(b)
// 	})
// }

// func ResolveDIDKey(ctx context.Context, did did.DID) (ucan.Verifier, error) {
// 	return verifier.FromDIDKey(did)
// }
