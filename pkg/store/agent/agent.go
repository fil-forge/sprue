package agent

import (
	"context"

	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/message"
	"github.com/fil-forge/go-ucanto/core/receipt"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/ipfs/go-cid"
)

const (
	InvocationNotFoundErrorName = "InvocationNotFound"
	ReceiptNotFoundErrorName    = "ReceiptNotFound"
)

var (
	// ErrInvocationNotFound indicates an invocation was not found that matches the passed details.
	ErrInvocationNotFound = errors.New(InvocationNotFoundErrorName, "invocation not found")
	// ErrReceiptNotFound indicates a receipt was not found that matches the passed details.
	ErrReceiptNotFound = errors.New(ReceiptNotFoundErrorName, "receipt not found")
)

type InvocationSource struct {
	Task       cid.Cid
	Invocation invocation.Invocation
	Message    cid.Cid
}

type ReceiptSource struct {
	Task    cid.Cid
	Receipt receipt.AnyReceipt
	Message cid.Cid
}

// IndexEntry is either an indexed invocation OR an indexed receipt.
type IndexEntry struct {
	Invocation *InvocationSource
	Receipt    *ReceiptSource
}

type Store interface {
	// Write an agent message to the store.
	Write(ctx context.Context, message message.AgentMessage, index []IndexEntry, source []byte) error
	// GetInvocation retrieves an invocation by its task CID. May return [ErrInvocationNotFound].
	GetInvocation(ctx context.Context, task cid.Cid) (invocation.Invocation, error)
	// GetReceipt retrieves a receipt by its task CID. May return [ErrReceiptNotFound].
	GetReceipt(ctx context.Context, task cid.Cid) (receipt.AnyReceipt, error)
}
