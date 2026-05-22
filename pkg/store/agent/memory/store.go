package memory

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/agent"
	"github.com/fil-forge/ucantone/ipld/codec/dagcbor"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"
)

type Store struct {
	mutex sync.RWMutex
	// agent message CID -> ucan.Container
	store map[cid.Cid]ucan.Container
	// "/<task_cid>/<invocation|receipt>/" -> list of agent messages invocation/receipt can be found in
	index map[string][]cid.Cid
}

var _ agent.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		store: map[cid.Cid]ucan.Container{},
		index: map[string][]cid.Cid{},
	}
}

func (s *Store) GetInvocation(ctx context.Context, task cid.Cid) (ucan.Invocation, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	key := fmt.Sprintf("/%s/invocation/", task)
	records, ok := s.index[key]
	if !ok || len(records) == 0 {
		return nil, agent.ErrInvocationNotFound
	}
	ct := s.store[records[0]]
	for _, inv := range ct.Invocations() {
		if inv.Task().Link() == task {
			return inv, nil
		}
	}
	return nil, agent.ErrInvocationNotFound
}

func (s *Store) GetReceipt(ctx context.Context, task cid.Cid) (ucan.Receipt, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	key := fmt.Sprintf("/%s/receipt/", task)
	records, ok := s.index[key]
	if !ok || len(records) == 0 {
		return nil, agent.ErrReceiptNotFound
	}
	ct := s.store[records[0]]
	rcpt, ok := ct.Receipt(task)
	if !ok {
		return nil, agent.ErrReceiptNotFound
	}
	return rcpt, nil
}

func (s *Store) Write(ctx context.Context, message ucan.Container, index []agent.IndexEntry) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	c, ok := message.(*container.Container)
	if !ok {
		c = container.New(
			container.WithInvocations(message.Invocations()...),
			container.WithReceipts(message.Receipts()...),
			container.WithDelegations(message.Delegations()...),
		)
	}

	var buf bytes.Buffer
	if err := c.MarshalCBOR(&buf); err != nil {
		return fmt.Errorf("marshaling agent message to CBOR: %w", err)
	}

	at, err := cid.V1Builder{Codec: dagcbor.Code, MhType: multihash.SHA2_256}.Sum(buf.Bytes())
	if err != nil {
		return fmt.Errorf("hashing agent message: %w", err)
	}

	s.store[at] = message
	for _, idx := range index {
		if idx.Invocation != nil {
			key := fmt.Sprintf("/%s/invocation/", idx.Invocation.Task)
			s.index[key] = append(s.index[key], at)
		}
		if idx.Receipt != nil {
			key := fmt.Sprintf("/%s/receipt/", idx.Receipt.Task)
			s.index[key] = append(s.index[key], at)
		}
	}
	return nil
}

func (s *Store) List(ctx context.Context, task cid.Cid, options ...agent.ListOption) (store.Page[ucan.Container], error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	cfg := &agent.ListConfig{}
	for _, opt := range options {
		opt(cfg)
	}

	var msgLinks []cid.Cid
	msgLinks = append(msgLinks, s.index[fmt.Sprintf("/%s/invocation/", task)]...)
	msgLinks = append(msgLinks, s.index[fmt.Sprintf("/%s/receipt/", task)]...)

	slices.SortFunc(msgLinks, func(a, b cid.Cid) int {
		return bytes.Compare(a.Bytes(), b.Bytes())
	})

	resultLinks := slices.Clone(msgLinks)
	results := make([]ucan.Container, 0, len(msgLinks))
	for _, l := range msgLinks {
		results = append(results, s.store[l])
	}

	if cfg.Cursor != nil {
		index := slices.IndexFunc(msgLinks, func(c cid.Cid) bool {
			return c.String() == *cfg.Cursor
		})
		if index == -1 {
			return store.Page[ucan.Container]{}, fmt.Errorf("invalid cursor: %s", *cfg.Cursor)
		}
		results = results[index+1:]
		resultLinks = resultLinks[index+1:]
	}

	if cfg.Limit != nil && len(results) > *cfg.Limit {
		results = results[:*cfg.Limit]
		resultLinks = resultLinks[:*cfg.Limit]
	}

	var nextCursor *string
	if len(results) > 0 && resultLinks[len(results)-1] != msgLinks[len(msgLinks)-1] {
		cursor := resultLinks[len(resultLinks)-1].String()
		nextCursor = &cursor
	}

	return store.Page[ucan.Container]{
		Results: results,
		Cursor:  nextCursor,
	}, nil
}
