package memory

import (
	"bytes"
	"fmt"
	"iter"

	"github.com/fil-forge/go-ucanto/core/car"
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/sprue/pkg/internal/ipldutil"
	"github.com/ipfs/go-cid"
)

func collect[T any](seq iter.Seq2[T, error]) ([]T, error) {
	var items []T
	for item, err := range seq {
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func toCARModel(roots []ipld.Link, blocks iter.Seq2[ipld.Block, error]) (carModel, error) {
	rts := make([]cid.Cid, 0, len(roots))
	for _, r := range roots {
		c, err := ipldutil.ToCID(r)
		if err != nil {
			return carModel{}, fmt.Errorf("converting root link to CID: %w", err)
		}
		rts = append(rts, c)
	}
	bs, err := collect(blocks)
	if err != nil {
		return carModel{}, err
	}
	return carModel{rts, bs}, nil
}

func sourceToCARModel(source []byte) (carModel, error) {
	roots, blocks, err := car.Decode(bytes.NewReader(source))
	if err != nil {
		return carModel{}, err
	}
	model, err := toCARModel(roots, blocks)
	if err != nil {
		return carModel{}, fmt.Errorf("converting to CAR model: %w", err)
	}
	return model, nil
}
