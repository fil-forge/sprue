package agent

import (
	"fmt"
	"iter"

	"github.com/fil-forge/go-libstoracha/capabilities/ucan"
	"github.com/fil-forge/go-ucanto/core/dag/blockstore"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/message"
	"github.com/fil-forge/go-ucanto/core/receipt"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/validator"
	"github.com/ipfs/go-cid"
	logging "github.com/ipfs/go-log/v2"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
)

var log = logging.Logger("store/agent")

type member struct {
	invocation invocation.Invocation
	receipt    receipt.AnyReceipt
}

// Iterates all embedded invocations & receipts of the given invocation.
func iterateInvocation(source cid.Cid, blocks blockstore.BlockReader, inv invocation.Invocation) iter.Seq2[member, error] {
	return func(yield func(member, error) bool) {
		caps := inv.Capabilities()
		if len(caps) > 0 && caps[0].Can() == ucan.ConcludeAbility {
			var err error
			match, err := ucan.Conclude.Match(validator.NewSource(caps[0], inv))
			if err != nil {
				log.Warnw("invalid invocation", "can", ucan.ConcludeAbility, "source", source, "error", err)
				return
			}
			rcpt, err := receipt.NewAnyReceipt(match.Value().Nb().Receipt, blocks)
			if err != nil {
				log.Warnw("creating receipt", "source", source, "error", err)
				return
			}
			if !yield(member{receipt: rcpt}, nil) {
				return
			}
			for m, err := range iterateReceipt(source, blocks, rcpt) {
				if err != nil {
					yield(member{}, err)
					return
				}
				if !yield(m, nil) {
					return
				}
			}
		}
	}
}

// Iterates all embedded invocations & receipts of the given receipt.
func iterateReceipt(source cid.Cid, blocks blockstore.BlockReader, rcpt receipt.AnyReceipt) iter.Seq2[member, error] {
	return func(yield func(member, error) bool) {
		invs := []invocation.Invocation{}
		inv, ok := rcpt.Ran().Invocation()
		if ok {
			invs = append(invs, inv)
		}

		if rcpt.Fx() != nil {
			for _, fx := range rcpt.Fx().Fork() {
				inv, ok := fx.Invocation()
				if ok {
					invs = append(invs, inv)
				}
			}
			if rcpt.Fx().Join() != (fx.Effect{}) {
				inv, ok := rcpt.Fx().Join().Invocation()
				if ok {
					invs = append(invs, inv)
				}
			}
		}

		for _, inv := range invs {
			if !yield(member{invocation: inv}, nil) {
				return
			}
			for m, err := range iterateInvocation(source, blocks, inv) {
				if err != nil {
					yield(member{}, err)
					return
				}
				if !yield(m, nil) {
					return
				}
			}
		}
	}
}

func Index(message message.AgentMessage) iter.Seq2[IndexEntry, error] {
	return func(yield func(IndexEntry, error) bool) {
		source, err := toCID(message.Root().Link())
		if err != nil {
			yield(IndexEntry{}, fmt.Errorf("converting message root link to CID: %w", err))
			return
		}

		blocks, err := blockstore.NewBlockReader(blockstore.WithBlocksIterator(message.Blocks()))
		if err != nil {
			yield(IndexEntry{}, err)
			return
		}
		for _, root := range message.Invocations() {
			inv, err := invocation.NewInvocationView(root, blocks)
			if err != nil {
				log.Warnw("creating invocation", "source", source, "error", err)
				continue
			}
			task, err := toCID(root)
			if err != nil {
				log.Warnw("converting invocation link to CID", "source", source, "link", root, "error", err)
				continue
			}
			entry := IndexEntry{
				Invocation: &InvocationSource{
					Task:       task,
					Invocation: inv,
					Message:    source,
				},
			}
			if !yield(entry, nil) {
				return
			}

			for m, err := range iterateInvocation(source, blocks, inv) {
				if err != nil {
					yield(IndexEntry{}, err)
					return
				}
				var entry IndexEntry
				if m.invocation != nil {
					task, err := toCID(m.invocation.Link())
					if err != nil {
						log.Warnw("converting invocation link to CID", "source", source, "link", m.invocation.Link(), "error", err)
						continue
					}
					entry.Invocation = &InvocationSource{
						Task:       task,
						Invocation: m.invocation,
						Message:    source,
					}
				} else if m.receipt != nil {
					task, err := toCID(m.receipt.Ran().Link())
					if err != nil {
						log.Warnw("converting receipt link to CID", "source", source, "link", m.receipt.Ran().Link(), "error", err)
						continue
					}
					entry.Receipt = &ReceiptSource{
						Task:    task,
						Receipt: m.receipt,
						Message: source,
					}
				} else {
					yield(IndexEntry{}, fmt.Errorf("unexpected member with neither invocation nor receipt: %v", m))
					return
				}
				if !yield(entry, nil) {
					return
				}
			}
		}

		for _, root := range message.Receipts() {
			rcpt, err := receipt.NewAnyReceipt(root, blocks)
			if err != nil {
				log.Warnw("creating receipt", "source", source, "error", err)
				continue
			}
			task, err := toCID(rcpt.Ran().Link())
			if err != nil {
				log.Warnw("converting receipt ran link to CID", "source", source, "link", rcpt.Ran().Link(), "error", err)
				continue
			}
			entry := IndexEntry{
				Receipt: &ReceiptSource{
					Task:    task,
					Receipt: rcpt,
					Message: source,
				},
			}
			if !yield(entry, nil) {
				return
			}
			for m, err := range iterateReceipt(source, blocks, rcpt) {
				if err != nil {
					yield(IndexEntry{}, err)
					return
				}
				var entry IndexEntry
				if m.invocation != nil {
					task, err := toCID(m.invocation.Link())
					if err != nil {
						log.Warnw("converting invocation link to CID", "source", source, "link", m.invocation.Link(), "error", err)
						continue
					}
					entry.Invocation = &InvocationSource{
						Task:       task,
						Invocation: m.invocation,
						Message:    source,
					}
				} else if m.receipt != nil {
					task, err := toCID(m.receipt.Ran().Link())
					if err != nil {
						log.Warnw("converting receipt link to CID", "source", source, "link", m.receipt.Ran().Link(), "error", err)
						continue
					}
					entry.Receipt = &ReceiptSource{
						Task:    task,
						Receipt: m.receipt,
						Message: source,
					}
				} else {
					yield(IndexEntry{}, fmt.Errorf("unexpected member with neither invocation nor receipt: %v", m))
					return
				}
				if !yield(entry, nil) {
					return
				}
			}
		}
	}
}

func toCID(l ipld.Link) (cid.Cid, error) {
	if c, ok := l.(cidlink.Link); ok {
		return c.Cid, nil
	}
	return cid.Parse(l.String())
}
