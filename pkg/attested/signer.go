package attested

import (
	"fmt"

	"github.com/fil-forge/libforge/commands/ucan/attest"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/varsig"
	"github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"
)

type Signer struct {
	did       did.DID
	authority ucan.Signer
}

var _ ucan.Signer = Signer{}

func NewSigner(did did.DID, authority ucan.Signer) Signer {
	return Signer{did: did, authority: authority}
}

func (s Signer) DID() did.DID {
	return s.did
}

func (s Signer) Sign(data []byte) []byte {
	msgDigest, err := mh.Sum(data, mh.SHA2_256, -1)
	if err != nil {
		panic(fmt.Sprintf("failed to compute message digest: %v", err))
	}

	inv, err := attest.Proof.Invoke(
		s.authority,
		s.authority.DID(),
		&attest.ProofArguments{Proof: cid.NewCidV1(cid.Raw, msgDigest)},
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create invocation: %v", err))
	}
	return inv.Bytes()
}

func (s Signer) SignatureAlgorithm() varsig.SignatureAlgorithm {
	return SignatureAlgorithm{}
}

type SignatureAlgorithm struct{}

var _ varsig.SignatureAlgorithm = SignatureAlgorithm{}

func (SignatureAlgorithm) Code() uint64 {
	return 0x300001
}

func (SignatureAlgorithm) Segments() []uint64 {
	return []uint64{}
}

func (SignatureAlgorithm) Decode([]byte) (SignatureAlgorithm, int, error) {
	return SignatureAlgorithm{}, 0, nil
}
func (SignatureAlgorithm) Encode() ([]byte, error) {
	return []byte{}, nil
}

func init() {
	varsig.RegisterSignatureAlgorithm(SignatureAlgorithm{})
}
