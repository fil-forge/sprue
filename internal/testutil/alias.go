package testutil

import (
	"testing"

	"github.com/fil-forge/libforge/testutil"
	"github.com/ipfs/go-cid"
)

var (
	Alice           = testutil.Alice
	Bob             = testutil.Bob
	Carol           = testutil.Carol
	Mallory         = testutil.Mallory
	Service         = testutil.Service
	WebService      = testutil.WebService
	RandomBytes     = testutil.RandomBytes
	RandomDID       = testutil.RandomDID
	RandomMultihash = testutil.RandomMultihash
	RandomSigner    = testutil.RandomSigner
)

func RandomCID(t *testing.T) cid.Cid {
	return cid.NewCidV1(cid.Raw, RandomMultihash(t))
}

func Must[T any](val T, err error) func(*testing.T) T {
	return testutil.Must(val, err)
}
