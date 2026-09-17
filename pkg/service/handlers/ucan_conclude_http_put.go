package handlers

import (
	"bytes"
	"context"
	"fmt"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	httpcmds "github.com/fil-forge/libforge/commands/http"
	ucancmds "github.com/fil-forge/libforge/commands/ucan"
	"github.com/fil-forge/libforge/digestutil"
	ucanlib "github.com/fil-forge/libforge/ucan"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/store/agent"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	edm "github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/ipfs/go-cid"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// allocationLookupConcurrency bounds the parallel agent-store reads that
// resolve each concluded put to its allocation. On the postgres backend every
// read is an object-store GET, so a batch of them is worth overlapping; the
// limit keeps a large batch from swamping the store.
const allocationLookupConcurrency = 16

// containerTokenBudget leaves headroom under container.MaxTokens for the tokens
// the server wraps around ours — the conclusion's own receipt, and whatever a
// sibling conclusion handler contributes to the same response. A package-level
// var so tests can shrink it: reaching the real budget takes a couple of
// thousand blobs, which is far too slow to drive through a unit test.
var containerTokenBudget = container.MaxTokens - 256

// concludedPut is one delivered /http/put receipt resolved to the allocation
// it fulfils — the space, blob and provider its acceptance needs.
type concludedPut struct {
	putInv    ucan.Invocation
	provider  did.DID
	space     did.DID
	blob      blobcmds.Blob
	cause     cid.Cid
	acceptReq *piriclient.AcceptRequest
}

func NewHTTPPutConcludeHandler(
	router *routing.Service,
	nodeProvider piriclient.Provider,
	agentStore agent.Store,
	blobRegistry blobregistry.Store,
	logger *zap.Logger,
) ConclusionHandler {
	log := logger.With(
		zap.Stringer("handler", ucancmds.Conclude),
		zap.Stringer("conclude", httpcmds.Put),
	)
	return ConclusionHandler{
		Command: httpcmds.Put.Command,
		Handler: func(ctx context.Context, conclusions []Conclusion) (ucan.Container, error) {
			if len(conclusions) == 0 {
				return nil, nil
			}
			log := log.With(zap.Int("puts", len(conclusions)))
			log.Debug("handling conclude")

			// A failed /http/put means the bytes never landed, so there is
			// nothing to accept. The delivery itself is still valid — the
			// client is reporting a real outcome — so the failure is skipped
			// rather than failing the whole conclusion.
			delivered := make([]Conclusion, 0, len(conclusions))
			for _, c := range conclusions {
				if c.Receipt.Out().IsErr() {
					log.Warn("skipping conclusion of a failed put",
						zap.Stringer("ran", c.Receipt.Ran()))
					continue
				}
				delivered = append(delivered, c)
			}
			if len(delivered) == 0 {
				return nil, nil
			}

			puts, err := resolveAllocations(ctx, agentStore, delivered, log)
			if err != nil {
				return nil, err
			}

			// One request per provider rather than per blob: the accepts of a
			// batch usually land on very few nodes.
			var providers []did.DID
			byProvider := map[did.DID][]*concludedPut{}
			for _, put := range puts {
				if _, seen := byProvider[put.provider]; !seen {
					providers = append(providers, put.provider)
				}
				byProvider[put.provider] = append(byProvider[put.provider], put)
			}

			var invocations []ucan.Invocation
			var receipts []ucan.Receipt
			for _, provider := range providers {
				invs, rcpts, err := acceptOnProvider(
					ctx, router, nodeProvider, agentStore, blobRegistry,
					provider, byProvider[provider], log)
				invocations = append(invocations, invs...)
				receipts = append(receipts, rcpts...)
				if err != nil {
					return concludeResponse(invocations, receipts, log), err
				}
			}
			return concludeResponse(invocations, receipts, log), nil
		},
	}
}

// resolveAllocations turns each delivered put receipt into the allocation it
// fulfils. The allocate invocation names the provider (its subject) and
// carries the space, blob and cause; without it there is no acceptance to
// make, so a failure here fails the conclusion.
func resolveAllocations(ctx context.Context, agentStore agent.Store, conclusions []Conclusion, log *zap.Logger) ([]*concludedPut, error) {
	puts := make([]*concludedPut, len(conclusions))
	grp, ctx := errgroup.WithContext(ctx)
	grp.SetLimit(allocationLookupConcurrency)
	for i, conclusion := range conclusions {
		grp.Go(func() error {
			log := log.With(zap.Stringer("ran", conclusion.Receipt.Ran()))

			var putArgs httpcmds.PutArguments
			if err := putArgs.UnmarshalCBOR(bytes.NewReader(conclusion.Invocation.ArgumentsBytes())); err != nil {
				log.Error("failed to unmarshal HTTP PUT arguments", zap.Error(err))
				return fmt.Errorf("unmarshaling HTTP PUT arguments: %w", err)
			}

			allocTaskLink := putArgs.Destination.Task
			log = log.With(zap.Stringer("allocation", allocTaskLink))

			allocInv, err := agentStore.GetInvocation(ctx, allocTaskLink)
			if err != nil {
				log.Error("failed to get allocation invocation", zap.Error(err))
				return fmt.Errorf("getting allocation invocation: %w", err)
			}

			var allocArgs blobcmds.AllocateArguments
			if err := allocArgs.UnmarshalCBOR(bytes.NewReader(allocInv.ArgumentsBytes())); err != nil {
				log.Error("failed to unmarshal allocate arguments", zap.Error(err))
				return fmt.Errorf("unmarshaling allocate arguments: %w", err)
			}

			puts[i] = &concludedPut{
				putInv: conclusion.Invocation,
				// The allocate invocation's subject and audience are both the
				// storage provider (its proofs are rooted at the provider).
				// The space travels in the allocate arguments rather than on
				// the subject.
				provider: allocInv.Subject(),
				space:    allocArgs.Space,
				blob:     allocArgs.Blob,
				cause:    allocArgs.Cause,
				acceptReq: &piriclient.AcceptRequest{
					Space:  allocArgs.Space,
					Digest: allocArgs.Blob.Digest,
					Size:   allocArgs.Blob.Size,
					Put:    conclusion.Invocation.Task().Link(),
				},
			}
			return nil
		})
	}
	if err := grp.Wait(); err != nil {
		return nil, err
	}
	return puts, nil
}

// concludeResponse packs the acceptances into the conclusion's response.
// Returning them is a courtesy that saves the deliverer a poll per blob, not
// part of the result, so a conclusion too large to answer in one container
// answers with none: the artifacts are all persisted either way, and the
// deliverer falls back to the receipts endpoint. Trimming is all-or-nothing
// on purpose — handing back an accept receipt without the location commitment
// it names would look like a malformed acceptance rather than a missing one.
func concludeResponse(invs []ucan.Invocation, rcpts []ucan.Receipt, log *zap.Logger) ucan.Container {
	ct := container.New(
		container.WithInvocations(invs...),
		container.WithReceipts(rcpts...),
	)
	// Measure the container rather than the slices handed in. It deduplicates
	// by link, and an accept receipt arrives twice — once as the result of its
	// own invocation and again in the node's response metadata — so the raw
	// lengths overstate what actually gets encoded.
	tokens := len(ct.Invocations()) + len(ct.Receipts()) + len(ct.Delegations())
	if tokens > containerTokenBudget {
		log.Warn("conclusion too large to answer in one container; the deliverer must poll for its receipts",
			zap.Int("tokens", tokens), zap.Int("budget", containerTokenBudget))
		return nil
	}
	return ct
}

// acceptance is one accepted blob together with the artifacts the node
// attached to it: the location commitment its receipt names and the PDP
// promise it awaits.
type acceptance struct {
	invs  []ucan.Invocation
	rcpts []ucan.Receipt
}

func (a acceptance) tokens() int { return len(a.invs) + len(a.rcpts) }

// groupAcceptances pairs each accept receipt with the artifacts the node
// attached to it. The node attaches them per request rather than per blob, so
// an acceptance claims its own by the links its receipt names. Whatever no
// acceptance claims is kept as a group of its own so it is still persisted.
func groupAcceptances(results []piriclient.AcceptResult, metas []ucan.Container) []acceptance {
	invsByLink := make(map[cid.Cid]ucan.Invocation)
	invsByTask := make(map[cid.Cid]ucan.Invocation)
	rcptsByRan := make(map[cid.Cid][]ucan.Receipt)
	for _, meta := range metas {
		if meta == nil {
			continue
		}
		for _, inv := range meta.Invocations() {
			invsByLink[inv.Link()] = inv
			invsByTask[inv.Task().Link()] = inv
		}
		for _, rcpt := range meta.Receipts() {
			rcptsByRan[rcpt.Ran()] = append(rcptsByRan[rcpt.Ran()], rcpt)
		}
	}

	claimedInvs := make(map[cid.Cid]bool)
	claimedRcpts := make(map[cid.Cid]bool)
	var acceptances []acceptance
	for _, res := range results {
		// A chunk that never ran has no receipt; persisting its invocation
		// alone would index an accept task that was never executed.
		if res.Receipt == nil {
			continue
		}
		a := acceptance{
			invs:  []ucan.Invocation{res.Invocation},
			rcpts: []ucan.Receipt{res.Receipt},
		}
		claimedRcpts[res.Receipt.Link()] = true
		for _, link := range attachedLinks(res.Receipt) {
			// A node names its location commitment by the invocation's own
			// link and its PDP promise by a task, so both indexes are tried.
			inv, ok := invsByLink[link]
			if !ok {
				inv, ok = invsByTask[link]
			}
			if !ok || claimedInvs[inv.Link()] {
				continue
			}
			claimedInvs[inv.Link()] = true
			a.invs = append(a.invs, inv)
			for _, rcpt := range rcptsByRan[inv.Task().Link()] {
				if claimedRcpts[rcpt.Link()] {
					continue
				}
				claimedRcpts[rcpt.Link()] = true
				a.rcpts = append(a.rcpts, rcpt)
			}
		}
		acceptances = append(acceptances, a)
	}

	// In the order the node sent them, so what lands in which message does
	// not depend on map iteration order.
	for _, meta := range metas {
		if meta == nil {
			continue
		}
		for _, inv := range meta.Invocations() {
			if !claimedInvs[inv.Link()] {
				claimedInvs[inv.Link()] = true
				acceptances = append(acceptances, acceptance{invs: []ucan.Invocation{inv}})
			}
		}
		for _, rcpt := range meta.Receipts() {
			if !claimedRcpts[rcpt.Link()] {
				claimedRcpts[rcpt.Link()] = true
				acceptances = append(acceptances, acceptance{rcpts: []ucan.Receipt{rcpt}})
			}
		}
	}
	return acceptances
}

// attachedLinks reports what an accept receipt names: its location commitment
// and the PDP task it promises. A failure receipt names nothing.
func attachedLinks(rcpt ucan.Receipt) []cid.Cid {
	if rcpt.Out().IsErr() {
		return nil
	}
	out, _ := rcpt.Out().Unpack()
	var acceptOK blobcmds.AcceptOK
	if err := acceptOK.UnmarshalCBOR(bytes.NewReader(out)); err != nil {
		return nil
	}
	return []cid.Cid{acceptOK.Site, acceptOK.PDP.Task}
}

// writeAgentMessages persists acceptances as one or more agent messages, each
// within a container's token budget. A single message per call would fail to
// encode once a conclusion is large enough, losing the record of acceptances
// the node has already performed.
//
// An acceptance is never split across messages. The receipts endpoint answers
// a poll from the messages the store indexes under the polled task, and a
// location commitment is indexed under its own task, not the acceptance's, so
// a commitment in another message is unreachable from the accept task the
// deliverer polls — leaving it an acceptance it cannot use.
func writeAgentMessages(ctx context.Context, agentStore agent.Store, acceptances []acceptance) error {
	var invs []ucan.Invocation
	var rcpts []ucan.Receipt
	flush := func() error {
		err := writeAgentMessage(ctx, agentStore, invs, rcpts)
		invs, rcpts = nil, nil
		return err
	}
	for _, a := range acceptances {
		if len(invs)+len(rcpts) > 0 && len(invs)+len(rcpts)+a.tokens() > containerTokenBudget {
			if err := flush(); err != nil {
				return err
			}
		}
		invs = append(invs, a.invs...)
		rcpts = append(rcpts, a.rcpts...)
	}
	if len(invs)+len(rcpts) > 0 {
		return flush()
	}
	return nil
}

// acceptOnProvider accepts every concluded put that landed on one provider,
// persists the results, and registers the blobs that were accepted. It
// returns the accept invocations and receipts (with the extras the node
// attached — the location commitments and PDP promises) for the conclude
// response, so a deliverer reads each blob's outcome from the answer rather
// than polling for it.
//
// A blob whose acceptance failed is reported by its own failure receipt and
// is not registered; that is not an error for the rest of the batch. An error
// means the provider could not be reached at all.
func acceptOnProvider(
	ctx context.Context,
	router *routing.Service,
	nodeProvider piriclient.Provider,
	agentStore agent.Store,
	blobRegistry blobregistry.Store,
	provider did.DID,
	puts []*concludedPut,
	log *zap.Logger,
) ([]ucan.Invocation, []ucan.Receipt, error) {
	log = log.With(zap.Stringer("provider", provider), zap.Int("blobs", len(puts)))

	info, err := router.GetProviderInfo(ctx, provider)
	if err != nil {
		log.Error("failed to get storage provider info", zap.Error(err))
		return nil, nil, fmt.Errorf("getting storage provider info: %w", err)
	}
	client, err := nodeProvider.Client(info.ID, info.Endpoint)
	if err != nil {
		log.Error("failed to create piri node", zap.Error(err))
		return nil, nil, fmt.Errorf("creating client: %w", err)
	}
	proofStore := ucanlib.NewContainerProofStore(info.Proofs)

	reqs := make([]*piriclient.AcceptRequest, len(puts))
	for i, put := range puts {
		reqs[i] = put.acceptReq
	}
	// Must match the accInv constructed in blob_add.go maybeAccept:
	// (1) Put = putInv.Task().Link() and
	// (2) WithNoNonce, so this invocation's CID matches the one whose
	// task link was returned to the client as AddOK.Site.Task and is
	// what the client polls the receipts endpoint for. A divergence
	// here means the receipt is stored under a CID nobody polls for,
	// producing "receipt not found after N attempts" client-side.
	// AcceptBatch may fail partway and still return the chunks it completed.
	// Those acceptances exist on the node, so they are persisted and
	// registered below before the error is reported; dropping them would
	// leave the node holding blobs sprue has no record of.
	results, metas, acceptErr := client.AcceptBatch(ctx, reqs, proofStore, invocation.WithNoNonce())
	if acceptErr != nil {
		log.Error("failed to execute blob accept", zap.Error(acceptErr))
		acceptErr = fmt.Errorf("executing blob accept: %w", acceptErr)
	}

	// The accept receipts and the artifacts piri attached to them (location
	// commitments, PDP promises) are persisted together, which is what makes
	// them retrievable by task link from the receipts endpoint.
	//
	// Written in container-sized messages: one container cannot hold more
	// than containerTokenBudget tokens, and a large conclusion produces more
	// than that. Persistence has to succeed whatever the batch size, since it
	// is what makes these receipts retrievable by task link afterwards.
	acceptances := groupAcceptances(results, metas)
	var accInvs []ucan.Invocation
	var accRcpts []ucan.Receipt
	for _, a := range acceptances {
		accInvs = append(accInvs, a.invs...)
		accRcpts = append(accRcpts, a.rcpts...)
	}
	if err := writeAgentMessages(ctx, agentStore, acceptances); err != nil {
		log.Error("failed to write agent message", zap.Error(err))
		return accInvs, accRcpts, fmt.Errorf("writing agent message: %w", err)
	}

	for i, res := range results {
		put := puts[i]
		log := log.With(
			zap.Stringer("space", put.space),
			zap.String("digest", digestutil.Format(put.blob.Digest)),
		)
		if res.Receipt == nil {
			// The request carrying this accept never completed — it failed, or
			// an earlier chunk did and this one was never issued. Nothing
			// attests the blob was accepted, so it is not registered; the
			// deliverer sees a missing receipt and can retry.
			log.Error("blob accept was not executed")
			continue
		}
		log = log.With(zap.Stringer("accept", res.Invocation.Task().Link()))
		if res.Receipt.Out().IsErr() {
			_, x := res.Receipt.Out().Unpack()
			var model edm.ErrorModel
			if err := model.UnmarshalCBOR(bytes.NewReader(x)); err != nil {
				log.Error("failed to unmarshal blob accept execution failure", zap.Error(err), zap.Binary("input", x))
				continue
			}
			log.Error("failed execution of blob accept", zap.String("name", model.ErrorName), zap.Error(model))
			continue
		}
		log.Debug("accept success")
		err := blobRegistry.Register(ctx, put.space, put.blob, put.cause)
		// it's ok if there's already a registration of this blob in this space
		if err != nil && !errors.Is(err, blobregistry.ErrEntryExists) {
			return accInvs, accRcpts, err
		}
	}
	return accInvs, accRcpts, acceptErr
}
