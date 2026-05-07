package ucans

import (
	"bytes"
	"io"

	"github.com/fil-forge/go-ucanto/core/car"
	"github.com/fil-forge/go-ucanto/core/dag/blockstore"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/sprue/pkg/internal/ipldutil"
	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multibase"
)

// ArchiveDelegations takes a list of delegations and returns CAR file bytes.
// This is the legacy format used in w3infra.
func ArchiveDelegations(delegations ...delegation.Delegation) ([]byte, error) {
	var roots []ipld.Link
	blocks := map[cid.Cid]ipld.Block{}
	for _, d := range delegations {
		roots = append(roots, d.Link())
		for b, err := range d.Export() {
			if err != nil {
				return nil, err
			}
			root, err := ipldutil.ToCID(b.Link())
			if err != nil {
				return nil, err
			}
			blocks[root] = b
		}
	}
	r := car.Encode(roots, func(yield func(ipld.Block, error) bool) {
		for _, b := range blocks {
			if !yield(b, nil) {
				return
			}
		}
	})
	return io.ReadAll(r)
}

// FormatDelegations takes a list of delegations and returns a string that can
// be included in a URL.
func FormatDelegations(delegations ...delegation.Delegation) (string, error) {
	carBytes, err := ArchiveDelegations(delegations...)
	if err != nil {
		return "", err
	}
	return multibase.Encode(multibase.Base64url, carBytes)
}

func ParseDelegations(s string) ([]delegation.Delegation, error) {
	_, carBytes, err := multibase.Decode(s)
	if err != nil {
		return nil, err
	}
	return ExtractDelegations(carBytes)
}

// ExtractDelegations extracts a set of delegations from a CAR file encoded
// in legacy format.
func ExtractDelegations(b []byte) ([]delegation.Delegation, error) {
	roots, blocks, err := car.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	bs, err := blockstore.NewBlockStore(blockstore.WithBlocksIterator(blocks))
	if err != nil {
		return nil, err
	}
	dlgs := make([]delegation.Delegation, 0, len(roots))
	for _, root := range roots {
		d, err := delegation.NewDelegationView(root, bs)
		if err != nil {
			return nil, err
		}
		dlgs = append(dlgs, d)
	}
	return dlgs, nil
}
