package handlers

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"io"

	blobcap "github.com/fil-forge/go-libstoracha/capabilities/blob"
	httpcap "github.com/fil-forge/go-libstoracha/capabilities/http"
	spaceblobcap "github.com/fil-forge/go-libstoracha/capabilities/space/blob"
	"github.com/fil-forge/go-libstoracha/capabilities/types"
	ucancap "github.com/fil-forge/go-libstoracha/capabilities/ucan"
	"github.com/fil-forge/go-libstoracha/digestutil"
	"github.com/fil-forge/go-ucanto/core/car"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/message"
	"github.com/fil-forge/go-ucanto/core/receipt"
	"github.com/fil-forge/go-ucanto/core/receipt/fx"
	"github.com/fil-forge/go-ucanto/core/receipt/ran"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/failure"
	"github.com/fil-forge/go-ucanto/did"
	"github.com/fil-forge/go-ucanto/principal"
	ed25519signer "github.com/fil-forge/go-ucanto/principal/ed25519/signer"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/ipld/go-ipld-prime/fluent/qp"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/multiformats/go-multihash"
	"go.uber.org/zap"

	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/internal/ipldutil"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/store/agent"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
)

func WithSpaceBlobAddMethod(id *identity.Identity, router *routing.Service, nodeProvider piriclient.Provider, agentStore agent.Store, blobRegistry blobregistry.Store, logger *zap.Logger) server.Option {
	return server.WithServiceMethod(
		spaceblobcap.AddAbility,
		server.Provide(
			spaceblobcap.Add,
			SpaceBlobAddHandler(id, router, nodeProvider, agentStore, blobRegistry, logger),
		),
	)
}

func SpaceBlobAddHandler(id *identity.Identity, router *routing.Service, nodeProvider piriclient.Provider, agentStore agent.Store, blobRegistry blobregistry.Store, logger *zap.Logger) server.HandlerFunc[spaceblobcap.AddCaveats, spaceblobcap.AddOk, failure.IPLDBuilderFailure] {
	log := logger.With(zap.String("handler", spaceblobcap.AddAbility))
	return func(ctx context.Context,
		cap ucan.Capability[spaceblobcap.AddCaveats],
		inv invocation.Invocation,
		iCtx server.InvocationContext,
	) (result.Result[spaceblobcap.AddOk, failure.IPLDBuilderFailure], fx.Effects, error) {
		blob := cap.Nb().Blob
		b58digest := digestutil.Format(blob.Digest)

		log := log.With(
			zap.String("space", cap.With()),
			zap.Dict(
				"blob",
				zap.String("digest", b58digest),
				zap.Uint64("size", blob.Size),
			),
		)
		log.Debug("adding blob")

		space, err := did.Parse(cap.With())
		if err != nil {
			return result.Error[spaceblobcap.AddOk, failure.IPLDBuilderFailure](
				errors.New(InvalidSpaceErrorName, "invalid space DID: %v", err),
			), nil, nil
		}

		provider, allocInv, allocRcpt, allocOK, err := doAllocate(ctx, router, nodeProvider, agentStore, space, blob, inv.Link(), log)
		if err != nil {
			if errors.Is(err, routing.ErrCandidateUnavailable) {
				return result.Error[spaceblobcap.AddOk, failure.IPLDBuilderFailure](
					routing.ErrCandidateUnavailable,
				), nil, nil
			}
			log.Error("allocation failed", zap.Error(err))
			return nil, nil, fmt.Errorf("allocating space: %w", err)
		}
		log = log.With(zap.Stringer("provider", provider.ID.DID()))

		putInv, putRcpt, err := genPut(blob, allocInv, allocOK, log)
		if err != nil {
			log.Error("failed to generate put invocation", zap.Error(err))
			return nil, nil, fmt.Errorf("generating put invocation: %w", err)
		}

		accInv, accRcpt, err := maybeAccept(ctx, agentStore, blobRegistry, nodeProvider, provider, space, blob, putInv, putRcpt, log)
		if err != nil {
			return nil, nil, err
		}

		forks := []fx.Effect{
			fx.FromInvocation(allocInv),
			fx.FromInvocation(putInv),
			fx.FromInvocation(accInv),
		}

		// As a temporary solution we fork all add effects that add inline
		// receipts so they can be delivered to the client.
		for _, rcpt := range []receipt.AnyReceipt{allocRcpt, putRcpt, accRcpt} {
			if rcpt == nil {
				continue
			}
			conclude, err := issueConclude(id.Signer, rcpt)
			if err != nil {
				log.Error("failed to create conclude invocation for receipt", zap.Error(err))
				return nil, nil, fmt.Errorf("creating conclude invocation: %w", err)
			}
			forks = append(forks, fx.FromInvocation(conclude))
		}

		fx := fx.NewEffects(fx.WithFork(forks...))

		return result.Ok[spaceblobcap.AddOk, failure.IPLDBuilderFailure](spaceblobcap.AddOk{
			Site: types.Promise{
				UcanAwait: types.Await{
					Selector: ".out.ok.site",
					Link:     accInv.Link(),
				},
			},
		}), fx, nil
	}
}

// issueConclude generates a ucan/conclude invocation for the given receipt.
func issueConclude(id ucan.Signer, receipt receipt.AnyReceipt) (invocation.IssuedInvocation, error) {
	inv, err := ucancap.Conclude.Invoke(
		id,
		id,
		id.DID().String(),
		ucancap.ConcludeCaveats{
			Receipt: receipt.Root().Link(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("creating conclude invocation: %w", err)
	}
	// Attach the receipt blocks to the conclude invocation
	for blk, err := range receipt.Blocks() {
		if err != nil {
			return nil, fmt.Errorf("getting receipt block: %w", err)
		}
		if err := inv.Attach(blk); err != nil {
			return nil, fmt.Errorf("attaching receipt block: %w", err)
		}
	}
	return inv, nil
}

type delegationFetcher struct {
	proof delegation.Delegation
}

// FIXME: this is silly, really the client should authorize the upload service
// to blob/allocate and blob/accept and then the upload service should use that
// delegation to authorize allocate and accept calls to the storage provider.
// This removes the need for storage providers to grant love lived delegations
// to the upload service. We will fix this in UCAN 1.0.
func (df delegationFetcher) GetDelegation(ctx context.Context, audience ucan.Principal) (delegation.Delegation, error) {
	if df.proof.Audience().DID() != audience.DID() {
		return nil, fmt.Errorf("delegation audience is %s, but invocation requires proof with audience %s", df.proof.Audience().DID(), audience.DID())
	}
	return df.proof, nil
}

func doAllocate(
	ctx context.Context,
	router *routing.Service,
	nodeProvider piriclient.Provider,
	agentStore agent.Store,
	space did.DID,
	blob types.Blob,
	cause ucan.Link,
	logger *zap.Logger,
) (routing.StorageProviderInfo, invocation.Invocation, receipt.AnyReceipt, blobcap.AllocateOk, error) {
	log := logger.With(zap.Stringer("cause", cause))
	log.Debug("doing allocation")

	var exclusions []ucan.Principal
	for {
		candidate, err := router.SelectStorageProvider(ctx, blob, routing.WithExclusions(exclusions...))
		if err != nil {
			log.Error("failed to select storage node", zap.Error(err))
			return routing.StorageProviderInfo{}, nil, nil, blobcap.AllocateOk{}, err
		}
		log := logger.With(zap.Stringer("candidate", candidate.ID.DID()), zap.String("endpoint", candidate.Endpoint.String()))
		log.Debug("selected storage provider candidate")

		client, err := nodeProvider.Client(candidate.ID, candidate.Endpoint)
		if err != nil {
			log.Error("failed to create piri node", zap.Error(err))
			return routing.StorageProviderInfo{}, nil, nil, blobcap.AllocateOk{}, err
		}

		res, inv, rcpt, err := client.Allocate(ctx, &piriclient.AllocateRequest{
			Space:  space,
			Digest: blob.Digest,
			Size:   blob.Size,
			Cause:  cause,
		}, delegationFetcher{candidate.Proof})
		if err != nil {
			log.Warn("failed to allocate blob", zap.Error(err))
			exclusions = append(exclusions, candidate.ID)
			continue
		}

		err = writeAgentMessage(ctx, agentStore, []invocation.Invocation{inv}, []receipt.AnyReceipt{rcpt})
		if err != nil {
			log.Error("failed to write agent message", zap.Error(err))
			exclusions = append(exclusions, candidate.ID)
			continue
		}

		return candidate, inv, rcpt, blobcap.AllocateOk{Size: res.Size, Address: res.Address}, nil
	}
}

// TODO(ash): move this into the client
func writeAgentMessage(ctx context.Context, agentStore agent.Store, invs []invocation.Invocation, rcpts []receipt.AnyReceipt) error {
	msg, err := message.Build(invs, rcpts)
	if err != nil {
		return fmt.Errorf("building agent message: %w", err)
	}
	idx := []agent.IndexEntry{}
	for e, err := range agent.Index(msg) {
		if err != nil {
			return fmt.Errorf("indexing agent message: %w", err)
		}
		idx = append(idx, e)
	}
	src, err := io.ReadAll(car.Encode([]ipld.Link{msg.Root().Link()}, msg.Blocks()))
	if err != nil {
		return fmt.Errorf("reading CAR data: %w", err)
	}
	return agentStore.Write(ctx, msg, idx, src)
}

// Generates an invocation to put the blob to the storage provider. It MAY
// return a receipt if the allocation result indicates that the provider already
// has the blob.
func genPut(blob types.Blob, allocInv invocation.Invocation, allocOK blobcap.AllocateOk, logger *zap.Logger) (invocation.Invocation, receipt.AnyReceipt, error) {
	log := logger
	log.Debug("generating put invocation")

	// Derive the principal that will provide the blob from the blob digest.
	// we do this so that any actor with a blob could issue a receipt for the
	// `/http/put` invocation.
	blobProvider, err := deriveDID(blob.Digest)
	if err != nil {
		return nil, nil, err
	}

	// Create http/put invocation
	fct := httpPutFact{
		id:  blobProvider.DID().String(),
		key: blobProvider.Encode(),
	}

	putInv, err := httpcap.Put.Invoke(
		blobProvider,
		blobProvider,
		blobProvider.DID().String(),
		httpcap.PutCaveats{
			URL: types.Promise{
				UcanAwait: types.Await{
					Selector: ".out.ok.address.url",
					Link:     allocInv.Link(),
				},
			},
			Headers: types.Promise{
				UcanAwait: types.Await{
					Selector: ".out.ok.address.headers",
					Link:     allocInv.Link(),
				},
			},
			Body: httpcap.Body{
				Digest: blob.Digest,
				Size:   blob.Size,
			},
		},
		// We encode the keys for the blob provider principal that can be used
		// by the client to use in order to sign a receipt. Client could
		// actually derive the same principal from the blob digest like we did
		// above, however by embedding the keys we make API more flexible and
		// could in the future generate one-off principals instead.
		delegation.WithFacts([]ucan.FactBuilder{fct}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("invoking %q: %w", httpcap.PutAbility, err)
	}

	var putRcpt receipt.AnyReceipt

	// If no address was provided we have a blob in store already and we can issue
	// a receipt for the `/http/put` without requiring blob to be provided.
	if allocOK.Address == nil {
		log.Info("blob present on provider, issuing receipt for put")
		putRcpt, err = receipt.Issue(
			blobProvider,
			result.Ok[httpcap.PutOk, failure.IPLDBuilderFailure](httpcap.PutOk{}),
			ran.FromInvocation(putInv),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("issuing %q receipt: %w", httpcap.PutAbility, err)
		}
	}

	return putInv, putRcpt, nil
}

// Derives did:key principal from (blob) multihash that can be used to
// sign ucan invocations/receipts for the the subject (blob) multihash.
func deriveDID(digest multihash.Multihash) (principal.Signer, error) {
	if len(digest) < 32 {
		return nil, fmt.Errorf("expected []byte with length %d, got %d", ed25519.SeedSize, len(digest))
	}
	seed := digest[len(digest)-32:]
	pk := ed25519.NewKeyFromSeed(seed)
	return ed25519signer.FromRaw(pk)
}

// maybeAccept generates and possibly executes a `/blob/accept` invocation if
// the provided put receipt is non-nil and non-failure.
func maybeAccept(
	ctx context.Context,
	agentStore agent.Store,
	blobRegistry blobregistry.Store,
	nodeProvider piriclient.Provider,
	providerInfo routing.StorageProviderInfo,
	space ucan.Principal,
	blob types.Blob,
	putInv invocation.Invocation,
	putRcpt receipt.AnyReceipt,
	logger *zap.Logger,
) (invocation.Invocation, receipt.AnyReceipt, error) {
	log := logger
	log.Debug("generating accept invocation")

	c, err := nodeProvider.Client(providerInfo.ID, providerInfo.Endpoint)
	if err != nil {
		log.Error("failed to create piri client for accept", zap.Error(err))
		return nil, nil, err
	}

	accReq := piriclient.AcceptRequest{
		Space:  space.DID(),
		Digest: blob.Digest,
		Size:   blob.Size,
		Put:    putInv.Link(),
	}

	accInv, err := c.AcceptInvocation(ctx, &accReq, delegationFetcher{providerInfo.Proof})
	if err != nil {
		log.Error("failed to create accept invocation", zap.Error(err))
		return nil, nil, err
	}

	var accRcpt receipt.AnyReceipt

	// If put has already succeeded, we can execute `/blob/accept` right away.
	if putRcpt != nil {
		_, x := result.Unwrap(putRcpt.Out())
		if x == nil {
			res, inv, rcpt, err := c.Accept(ctx, &accReq, delegationFetcher{providerInfo.Proof})
			if err != nil {
				log.Error("failed to execute accept on piri", zap.Error(err))
				return nil, nil, err
			}
			log.Debug("blob accepted", zap.Stringer("site", res.Site))

			err = writeAgentMessage(ctx, agentStore, []invocation.Invocation{inv}, []receipt.AnyReceipt{rcpt})
			if err != nil {
				log.Error("failed to write agent message for accept", zap.Error(err))
				return nil, nil, err
			}

			cause, err := ipldutil.ToCID(inv.Link())
			if err != nil {
				return nil, nil, err
			}

			err = blobRegistry.Register(ctx, space.DID(), blob, cause)
			if err != nil {
				log.Error("failed to register blob", zap.Error(err))
				return nil, nil, err
			}

			accInv = inv
			accRcpt = rcpt
		}
	}

	return accInv, accRcpt, nil
}

// httpPutFact contains the fact data for the http/put invocation.
// TODO: should move to go-libstoracha
type httpPutFact struct {
	id  string
	key []byte
}

func (hpf httpPutFact) ToIPLD() (map[string]datamodel.Node, error) {
	n, err := qp.BuildMap(basicnode.Prototype.Any, 2, func(ma datamodel.MapAssembler) {
		qp.MapEntry(ma, "id", qp.String(hpf.id))
		qp.MapEntry(ma, "keys", qp.Map(1, func(ma datamodel.MapAssembler) {
			qp.MapEntry(ma, hpf.id, qp.Bytes(hpf.key))
		}))
	})
	if err != nil {
		return nil, err
	}

	return map[string]datamodel.Node{
		"keys": n,
	}, nil
}
