package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/fil-forge/go-ucanto/core/dag/blockstore"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/message"
	"github.com/fil-forge/go-ucanto/core/receipt"
	"github.com/fil-forge/sprue/pkg/internal/ipldutil"
	"github.com/fil-forge/sprue/pkg/store/agent"
	"github.com/ipfs/go-cid"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
)

type carModel struct {
	roots  []cid.Cid
	blocks []ipld.Block
}

type indexModel struct {
	// the root CID of the invocation or receipt
	root cid.Cid
	// the agent message this invocation or receipt was found in
	at cid.Cid
}

type Store struct {
	mutex sync.RWMutex
	// agent message CID -> carModel
	store map[cid.Cid]carModel
	// "/<task_cid>/<invocation|receipt>/" -> list of invocation/receipt roots found in that message
	index map[string][]indexModel
}

var _ agent.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		store: map[cid.Cid]carModel{},
		index: map[string][]indexModel{},
	}
}

func (s *Store) GetInvocation(ctx context.Context, task cid.Cid) (invocation.Invocation, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	key := fmt.Sprintf("/%s/invocation/", task)
	records, ok := s.index[key]
	if !ok || len(records) == 0 {
		return nil, agent.ErrInvocationNotFound
	}
	archive := s.store[records[0].at]
	root := cidlink.Link{Cid: records[0].root}
	bs, err := blockstore.NewBlockStore(blockstore.WithBlocks(archive.blocks))
	if err != nil {
		return nil, fmt.Errorf("creating blockstore: %w", err)
	}
	return invocation.NewInvocationView(root, bs)
}

func (s *Store) GetReceipt(ctx context.Context, task cid.Cid) (receipt.AnyReceipt, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	key := fmt.Sprintf("/%s/receipt/", task)
	records, ok := s.index[key]
	if !ok || len(records) == 0 {
		return nil, agent.ErrReceiptNotFound
	}
	archive := s.store[records[0].at]
	root := cidlink.Link{Cid: records[0].root}
	bs, err := blockstore.NewBlockStore(blockstore.WithBlocks(archive.blocks))
	if err != nil {
		return nil, fmt.Errorf("creating blockstore: %w", err)
	}
	return receipt.NewAnyReceipt(root, bs)
}

func (s *Store) Write(ctx context.Context, message message.AgentMessage, index []agent.IndexEntry, source []byte) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	at, err := ipldutil.ToCID(message.Root().Link())
	if err != nil {
		return err
	}
	model, err := sourceToCARModel(source)
	if err != nil {
		return fmt.Errorf("converting to CAR model: %w", err)
	}
	s.store[at] = model
	for _, idx := range index {
		if idx.Invocation != nil {
			root := idx.Invocation.Task
			key := fmt.Sprintf("/%s/invocation/", root)
			s.index[key] = append(s.index[key], indexModel{root: root, at: at})
		}
		if idx.Receipt != nil {
			key := fmt.Sprintf("/%s/receipt/", idx.Receipt.Task)
			receiptRoot, err := ipldutil.ToCID(idx.Receipt.Receipt.Root().Link())
			if err != nil {
				return fmt.Errorf("converting receipt root to CID: %w", err)
			}
			s.index[key] = append(s.index[key], indexModel{root: receiptRoot, at: at})
		}
	}
	return nil
}
