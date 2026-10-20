package proofs

import "github.com/fil-forge/ucantone/did"

type SetArguments struct {
	Provider did.DID `cborgen:"provider" dagjsongen:"provider"`
	Proofs   []byte  `cborgen:"proofs" dagjsongen:"proofs"`
}
