package handlers

import (
	"context"
	"crypto/ed25519"
	"fmt"

	blobcaps "github.com/fil-forge/libforge/capabilities/blob"
	httpcaps "github.com/fil-forge/libforge/capabilities/http"
	"github.com/fil-forge/libforge/digestutil"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/lib/ucan_server"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/store/agent"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	"github.com/fil-forge/ucantone/execution/bindexec"
	"github.com/fil-forge/ucantone/ipld"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/principal"
	ed25519signer "github.com/fil-forge/ucantone/principal/ed25519"
	"github.com/fil-forge/ucantone/result"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/promise"
	"github.com/fil-forge/ucantone/ucan/receipt"
	"github.com/multiformats/go-multihash"
	"go.uber.org/zap"
)

func NewBlobAddHandler(id *identity.Identity, provisioningSvc *provisioning.Service, router *routing.Service, nodeProvider piriclient.Provider, agentStore agent.Store, blobRegistry blobregistry.Store, logger *zap.Logger) Handler {
	log := logger.With(zap.String("handler", blobcaps.AddCommand))
	return Handler{
		Capability: blobcaps.Add,
		Handler: bindexec.NewHandler(func(
			req *bindexec.Request[*blobcaps.AddArguments],
			res *bindexec.Response[*blobcaps.AddOK],
		) error {
			args := req.Task().BindArguments()
			blob := args.Blob
			space := req.Invocation().Subject().DID()
			b58digest := digestutil.Format(blob.Digest)

			log := log.With(
				zap.Stringer("space", space),
				zap.Dict(
					"blob",
					zap.String("digest", b58digest),
					zap.Uint64("size", blob.Size),
				),
			)
			log.Debug("adding blob")

			providers, err := provisioningSvc.ListServiceProviders(req.Context(), space)
			if err != nil {
				log.Error("failed to list service providers", zap.Error(err))
				return fmt.Errorf("listing service providers: %w", err)
			}
			if len(providers) == 0 {
				return res.SetFailure(errors.New(InsufficientStorageErrorName, "space has no storage provider"))
			}

			reg, err := blobRegistry.Get(req.Context(), space, blob.Digest)
			if err != nil {
				if !errors.Is(err, blobregistry.ErrEntryNotFound) {
					log.Error("failed to get blob registration", zap.Error(err))
					return err
				}
			}

			// If blob is already registered in the space, we can skip allocation and
			// return the information from the original receipt, plus the invocations
			// and receipts for the /blob/allocate /http/put and /blob/accept tasks
			// that happened.
			if err == nil {
				log.Debug("blob already registered in space")

				// blob registration cause is the CID of the `/space/blob/add` task
				addRcpt, err := agentStore.GetReceipt(req.Context(), reg.Cause)
				if err != nil {
					log.Error("failed to get receipt for blob registration", zap.Error(err))
					return err
				}

				addOK, err := result.MatchResultR2(
					addRcpt.Out(),
					func(o ipld.Any) (*blobcaps.AddOK, error) {
						var addOK blobcaps.AddOK
						err := datamodel.Rebind(datamodel.NewAny(o), &addOK)
						if err != nil {
							log.Error("failed to rebind add OK result", zap.Error(err))
							return nil, fmt.Errorf("rebinding add OK result: %w", err)
						}
						return &addOK, nil
					},
					func(x ipld.Any) (*blobcaps.AddOK, error) {
						// should not have been registered on error
						log.Error("blob registration receipt contains failure", zap.Any("error", x))
						return nil, fmt.Errorf("blob registration receipt contains failure: %v", x)
					},
				)
				if err != nil {
					log.Error("failed to match blob add receipt result", zap.Error(err))
					return fmt.Errorf("matching blob add receipt result: %w", err)
				}

				accRcpt, err := agentStore.GetReceipt(req.Context(), addOK.Site.Task)
				if err != nil {
					log.Error("failed to get receipt for blob accept", zap.Error(err))
					return fmt.Errorf("getting receipt for blob accept: %w", err)
				}

				accInv, err := agentStore.GetInvocation(req.Context(), addOK.Site.Task)
				if err != nil {
					log.Error("failed to get invocation for blob accept", zap.Error(err))
					return fmt.Errorf("getting invocation for blob accept: %w", err)
				}

				accArgs := blobcaps.AcceptArguments{}
				err = datamodel.Rebind(datamodel.NewAny(accInv.Arguments()), &accArgs)
				if err != nil {
					log.Error("failed to rebind accept OK result", zap.Error(err))
					return fmt.Errorf("rebinding accept OK result: %w", err)
				}

				putRcpt, err := agentStore.GetReceipt(req.Context(), accArgs.Put.Task)
				if err != nil {
					log.Error("failed to get receipt for HTTP PUT", zap.Error(err))
					return fmt.Errorf("getting receipt for HTTP PUT: %w", err)
				}

				putInv, err := agentStore.GetInvocation(req.Context(), accArgs.Put.Task)
				if err != nil {
					log.Error("failed to get invocation for HTTP PUT", zap.Error(err))
					return fmt.Errorf("getting invocation for HTTP PUT: %w", err)
				}

				putArgs := httpcaps.PutArguments{}
				err = datamodel.Rebind(datamodel.NewAny(putInv.Arguments()), &putArgs)
				if err != nil {
					log.Error("failed to rebind HTTP PUT arguments", zap.Error(err))
					return fmt.Errorf("rebinding HTTP PUT arguments: %w", err)
				}

				allocRcpt, err := agentStore.GetReceipt(req.Context(), putArgs.Destination.Task)
				if err != nil {
					log.Error("failed to get receipt for allocation", zap.Error(err))
					return fmt.Errorf("getting receipt for allocation: %w", err)
				}

				allocInv, err := agentStore.GetInvocation(req.Context(), putArgs.Destination.Task)
				if err != nil {
					log.Error("failed to get invocation for allocation", zap.Error(err))
					return fmt.Errorf("getting invocation for allocation: %w", err)
				}

				res.SetMetadata(container.New(
					container.WithInvocations(allocInv, putInv, accInv),
					container.WithReceipts(allocRcpt, putRcpt, accRcpt),
				))

				return res.SetSuccess(addOK)
			}

			cause := req.Invocation().Task().Link()
			proofStore := ucan_server.NewContainerProofStore(req.Metadata())
			provider, allocInv, allocRcpt, allocOK, err := doAllocate(req.Context(), router, nodeProvider, agentStore, space, blob, cause, proofStore, log)
			if err != nil {
				if errors.Is(err, routing.ErrCandidateUnavailable) {
					return res.SetFailure(routing.ErrCandidateUnavailable)
				}
				log.Error("allocation failed", zap.Error(err))
				return fmt.Errorf("allocating space: %w", err)
			}
			log = log.With(zap.Stringer("provider", provider.ID.DID()))

			putInv, putRcpt, err := genPut(blob, allocInv, allocOK, log)
			if err != nil {
				log.Error("failed to generate put invocation", zap.Error(err))
				return fmt.Errorf("generating put invocation: %w", err)
			}

			accInv, accRcpt, err := maybeAccept(req.Context(), agentStore, blobRegistry, nodeProvider, provider, space, blob, cause, putInv, putRcpt, proofStore, log)
			if err != nil {
				return err
			}

			metaOpts := []container.Option{container.WithInvocations(allocInv, putInv, accInv)}
			for _, rcpt := range []ucan.Receipt{allocRcpt, putRcpt, accRcpt} {
				if rcpt != nil {
					metaOpts = append(metaOpts, container.WithReceipts(rcpt))
				}
			}
			res.SetMetadata(container.New(metaOpts...))

			return res.SetSuccess(&blobcaps.AddOK{
				Site: promise.AwaitOK{
					Task: accInv.Task().Link(),
				},
			})
		}),
	}
}

func doAllocate(
	ctx context.Context,
	router *routing.Service,
	nodeProvider piriclient.Provider,
	agentStore agent.Store,
	space did.DID,
	blob blobcaps.Blob,
	cause ucan.Link,
	proofStore ucan_server.ProofStore,
	logger *zap.Logger,
) (routing.StorageProviderInfo, ucan.Invocation, ucan.Receipt, blobcaps.AllocateOK, error) {
	log := logger.With(zap.Stringer("cause", cause))
	log.Debug("doing allocation")

	var exclusions []ucan.Principal
	for {
		candidate, err := router.SelectStorageProvider(ctx, blob, routing.WithExclusions(exclusions...))
		if err != nil {
			log.Error("failed to select storage node", zap.Error(err))
			return routing.StorageProviderInfo{}, nil, nil, blobcaps.AllocateOK{}, err
		}
		log := logger.With(zap.Stringer("candidate", candidate.ID.DID()), zap.String("endpoint", candidate.Endpoint.String()))
		log.Debug("selected storage provider candidate")

		client, err := nodeProvider.Client(candidate.ID, candidate.Endpoint)
		if err != nil {
			log.Error("failed to create piri node", zap.Error(err))
			return routing.StorageProviderInfo{}, nil, nil, blobcaps.AllocateOK{}, err
		}

		res, inv, rcpt, err := client.Allocate(ctx, &piriclient.AllocateRequest{
			Space:  space,
			Digest: blob.Digest,
			Size:   blob.Size,
			Cause:  cause,
		}, proofStore)
		if err != nil {
			log.Warn("failed to allocate blob", zap.Error(err))
			exclusions = append(exclusions, candidate.ID)
			continue
		}

		err = writeAgentMessage(ctx, agentStore, []ucan.Invocation{inv}, []ucan.Receipt{rcpt})
		if err != nil {
			log.Error("failed to write agent message", zap.Error(err))
			exclusions = append(exclusions, candidate.ID)
			continue
		}

		return candidate, inv, rcpt, *res, nil
	}
}

// TODO(ash): move this into the client
func writeAgentMessage(ctx context.Context, agentStore agent.Store, invs []ucan.Invocation, rcpts []ucan.Receipt) error {
	msg := container.New(container.WithInvocations(invs...), container.WithReceipts(rcpts...))
	idx := agent.Index(msg)
	return agentStore.Write(ctx, msg, idx)
}

// Generates an invocation to put the blob to the storage provider. It MAY
// return a receipt if the allocation result indicates that the provider already
// has the blob.
func genPut(blob blobcaps.Blob, allocInv ucan.Invocation, allocOK blobcaps.AllocateOK, logger *zap.Logger) (ucan.Invocation, ucan.Receipt, error) {
	log := logger
	log.Debug("generating put invocation")

	// Derive the principal that will provide the blob from the blob digest.
	// we do this so that any actor with a blob could issue a receipt for the
	// `/http/put` invocation.
	blobProvider, err := deriveDID(blob.Digest)
	if err != nil {
		return nil, nil, err
	}

	putInv, err := httpcaps.Put.Invoke(
		blobProvider,
		blobProvider,
		&httpcaps.PutArguments{
			Body:        blob,
			Destination: promise.AwaitOK{Task: allocInv.Task().Link()},
		},
		invocation.WithAudience(blobProvider),
		// We encode the keys for the blob provider principal that can be used
		// by the client to use in order to sign a receipt. Client could
		// actually derive the same principal from the blob digest like we did
		// above, however by embedding the keys we make API more flexible and
		// could in the future generate one-off principals instead.
		invocation.WithMetadata(
			datamodel.Map{
				"keys": datamodel.Map{
					"id": blobProvider.DID().String(),
					"keys": datamodel.Map{
						blobProvider.DID().String(): blobProvider.Bytes(),
					},
				},
			},
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("invoking %q: %w", httpcaps.PutCommand, err)
	}

	var putRcpt ucan.Receipt

	// If no address was provided we have a blob in store already and we can issue
	// a receipt for the `/http/put` without requiring blob to be provided.
	if allocOK.Address == nil {
		log.Debug("blob present on provider, issuing receipt for put")
		var ok datamodel.Map
		err = datamodel.Rebind(&httpcaps.PutOK{}, &ok)
		if err != nil {
			return nil, nil, fmt.Errorf("rebinding %q OK: %w", httpcaps.PutCommand, err)
		}
		putRcpt, err = receipt.Issue(
			blobProvider,
			putInv.Task().Link(),
			result.OK[ipld.Map, ipld.Any](ok),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("issuing %q receipt: %w", httpcaps.PutCommand, err)
		}
	}

	return putInv, putRcpt, nil
}

// Derives did:key principal from (blob) multihash that can be used to
// sign ucan invocations/receipts for the the subject (blob) multihash.
func deriveDID(digest multihash.Multihash) (principal.Signer, error) {
	if len(digest) < ed25519.SeedSize {
		return nil, fmt.Errorf("expected []byte with length %d, got %d", ed25519.SeedSize, len(digest))
	}
	seed := digest[len(digest)-ed25519.SeedSize:]
	return ed25519signer.FromRaw(seed)
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
	blob blobcaps.Blob,
	cause ucan.Link, // original /space/blob/add task
	putInv ucan.Invocation,
	putRcpt ucan.Receipt,
	proofStore ucan_server.ProofStore,
	logger *zap.Logger,
) (ucan.Invocation, ucan.Receipt, error) {
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

	accInv, _, _, err := c.AcceptInvocation(ctx, &accReq, proofStore, invocation.WithNoNonce())
	if err != nil {
		log.Error("failed to create accept invocation", zap.Error(err))
		return nil, nil, err
	}

	var accRcpt ucan.Receipt

	// If put has already succeeded, we can execute `/blob/accept` right away.
	if putRcpt != nil {
		_, x := result.Unwrap(putRcpt.Out())
		if x == nil {
			res, inv, rcpt, err := c.Accept(ctx, &accReq, proofStore, invocation.WithNoNonce())
			if err != nil {
				log.Error("failed to execute accept on piri", zap.Error(err))
				return nil, nil, err
			}
			log.Debug("blob accepted", zap.Stringer("site", res.Site))

			err = writeAgentMessage(ctx, agentStore, []ucan.Invocation{inv}, []ucan.Receipt{rcpt})
			if err != nil {
				log.Error("failed to write agent message for accept", zap.Error(err))
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
