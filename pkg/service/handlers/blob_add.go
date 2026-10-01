package handlers

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"fmt"

	accesscmds "github.com/fil-forge/libforge/commands/access"
	blobcmds "github.com/fil-forge/libforge/commands/blob"
	httpcmds "github.com/fil-forge/libforge/commands/http"
	"github.com/fil-forge/libforge/digestutil"
	"github.com/fil-forge/libforge/identity"
	ucanlib "github.com/fil-forge/libforge/ucan"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/sprue/pkg/provisioning"
	"github.com/fil-forge/sprue/pkg/routing"
	"github.com/fil-forge/sprue/pkg/store/agent"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/did"
	"github.com/fil-forge/ucantone/errors"
	edm "github.com/fil-forge/ucantone/errors/datamodel"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/multikey"
	ed25519signer "github.com/fil-forge/ucantone/multikey/ed25519"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/promise"
	"github.com/fil-forge/ucantone/ucan/receipt"
	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"
	"go.uber.org/zap"
)

func NewBlobAddHandler(id identity.Identity, provisioningSvc *provisioning.Service, router *routing.Service, nodeProvider piriclient.Provider, agentStore agent.Store, blobRegistry blobregistry.Store, logger *zap.Logger) server.Route {
	log := logger.With(zap.Stringer("handler", blobcmds.Add))
	return blobcmds.Add.Route(
		func(req *binding.Request[*blobcmds.AddArguments], res *binding.Response[*blobcmds.AddOK]) error {
			args := req.Task().Arguments()
			blob := args.Blob
			space := req.Invocation().Subject()
			digest, hashed := blob.Digest()

			log := log.With(zap.Stringer("space", space), blobField(blob))
			log.Debug("adding blob")

			// Nothing is done for a space that is not provisioned.
			providers, err := provisioningSvc.ListServiceProviders(req.Context(), space)
			if err != nil {
				log.Error("failed to list service providers", zap.Error(err))
				return fmt.Errorf("listing service providers: %w", err)
			}
			if len(providers) == 0 {
				return res.SetFailure(errors.New(accesscmds.InsufficientStorageErrorName, "space has no storage provider"))
			}

			// A blob named only by its hash function is keyed by this task: the
			// /http/put key derives from its link, and the allocation hangs off
			// it. Without a nonce, two such adds of the same size in a space
			// would be the same task.
			if !hashed && len(req.Invocation().Nonce()) == 0 {
				return res.SetFailure(errors.New(MissingNonceErrorName,
					"a %s without a digest must carry a nonce", blobcmds.Add.Command))
			}
			cause := req.Invocation().Task().Link()

			// A digest-less add that has already run is answered with its
			// first result: a second allocation for the same task would
			// share its /http/put key and orphan the first. The receipt is
			// written only once the first response has gone out, so this is
			// what tells a replay apart from the add itself.
			if !hashed {
				prior, err := agentStore.GetReceipt(req.Context(), cause)
				if err != nil && !errors.Is(err, agent.ErrReceiptNotFound) {
					log.Error("failed to check for a prior blob add", zap.Error(err))
					return fmt.Errorf("checking for a prior blob add: %w", err)
				}
				if err == nil {
					log.Debug("replaying blob add", zap.Stringer("cause", cause))
					if prior.Out().IsErr() {
						_, x := prior.Out().Unpack()
						var model edm.ErrorModel
						if err := model.UnmarshalCBOR(bytes.NewReader(x)); err != nil {
							return fmt.Errorf("decoding replayed blob add failure: %w", err)
						}
						return res.SetFailure(model)
					}
					return replayAdd(req.Context(), agentStore, prior, res, log)
				}
			}

			// Without a digest there is nothing to look up: the add always
			// uploads.
			err = blobregistry.ErrEntryNotFound
			var reg blobregistry.Record
			if hashed {
				reg, err = blobRegistry.Get(req.Context(), space, digest)
				if err != nil && !errors.Is(err, blobregistry.ErrEntryNotFound) {
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

				// blob registration cause is the CID of the `/blob/add` task
				addRcpt, err := agentStore.GetReceipt(req.Context(), reg.Cause)
				if err != nil {
					log.Error("failed to get receipt for blob registration", zap.Error(err))
					return err
				}

				// should not have been registered on error
				if addRcpt.Out().IsErr() {
					log.Error("blob registration receipt contains failure")
					return fmt.Errorf("blob registration receipt contains failure")
				}
				return replayAdd(req.Context(), agentStore, addRcpt, res, log)
			}

			provider, allocInv, allocRcpt, allocOK, err := doAllocate(req.Context(), router, nodeProvider, agentStore, space, blob, cause, log)
			if err != nil {
				if errors.Is(err, routing.ErrCandidateUnavailable) {
					return res.SetFailure(routing.ErrCandidateUnavailable)
				}
				if errors.Is(err, blobcmds.ErrUnsupportedDigestCode) {
					return res.SetFailure(blobcmds.ErrUnsupportedDigestCode)
				}
				log.Error("allocation failed", zap.Error(err))
				return fmt.Errorf("allocating space: %w", err)
			}
			log = log.With(zap.Stringer("provider", provider.ID))

			putInv, putRcpt, err := genPut(blob, cause, allocInv, allocOK, log)
			if err != nil {
				log.Error("failed to generate put invocation", zap.Error(err))
				return fmt.Errorf("generating put invocation: %w", err)
			}

			accInv, accRcpt, accExtras, err := maybeAccept(req.Context(), agentStore, blobRegistry, nodeProvider, provider, space, blob, cause, putInv, putRcpt, log)
			if err != nil {
				return err
			}

			metaOpts := []container.Option{container.WithInvocations(allocInv, putInv, accInv)}
			for _, rcpt := range []ucan.Receipt{allocRcpt, putRcpt, accRcpt} {
				if rcpt != nil {
					metaOpts = append(metaOpts, container.WithReceipts(rcpt))
				}
			}
			// Forward piri's extra invocations/receipts (location commitment
			// + /pdp/accept) into the response so the client can find them.
			if len(accExtras.invocations) > 0 {
				metaOpts = append(metaOpts, container.WithInvocations(accExtras.invocations...))
			}
			if len(accExtras.receipts) > 0 {
				metaOpts = append(metaOpts, container.WithReceipts(accExtras.receipts...))
			}
			res.SetMetadata(container.New(metaOpts...))

			return res.SetSuccess(&blobcmds.AddOK{
				Site: promise.AwaitOK{
					Task: accInv.Task().Link(),
				},
			})
		},
	)
}

func doAllocate(
	ctx context.Context,
	router *routing.Service,
	nodeProvider piriclient.Provider,
	agentStore agent.Store,
	space did.DID,
	blob blobcmds.BlobSpec,
	cause cid.Cid,
	logger *zap.Logger,
) (routing.StorageProviderInfo, ucan.Invocation, ucan.Receipt, blobcmds.AllocateOK, error) {
	log := logger.With(zap.Stringer("cause", cause))
	log.Debug("doing allocation")

	var exclusions []did.DID
	// unsupported records that a candidate refused the digest code. Once no
	// candidate is left, that refusal is the answer: the client can retry
	// with the digest.
	unsupported := false
	for {
		candidate, err := router.SelectStorageProvider(ctx, space, blob, routing.WithExclusions(exclusions...))
		if err != nil {
			if unsupported && errors.Is(err, routing.ErrCandidateUnavailable) {
				log.Warn("no storage node supports the digest code")
				return routing.StorageProviderInfo{}, nil, nil, blobcmds.AllocateOK{}, blobcmds.ErrUnsupportedDigestCode
			}
			log.Error("failed to select storage node", zap.Error(err))
			return routing.StorageProviderInfo{}, nil, nil, blobcmds.AllocateOK{}, err
		}
		log := logger.With(zap.Stringer("candidate", candidate.ID), zap.String("endpoint", candidate.Endpoint.String()))
		log.Debug("selected storage provider candidate")

		client, err := nodeProvider.Client(candidate.ID, candidate.Endpoint)
		if err != nil {
			log.Error("failed to create piri node", zap.Error(err))
			return routing.StorageProviderInfo{}, nil, nil, blobcmds.AllocateOK{}, err
		}

		// The proof chain for `/blob/allocate` comes from the proofs the selected
		// provider granted the upload service at registration.
		proofStore := ucanlib.NewContainerProofStore(candidate.Proofs)
		res, inv, rcpt, err := client.Allocate(ctx, &piriclient.AllocateRequest{
			Space: space,
			Blob:  blob,
			Cause: cause,
		}, proofStore)
		if err != nil {
			if hasErrorName(err, blobcmds.UnsupportedDigestCodeErrorName) {
				unsupported = true
			}
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
func genPut(blob blobcmds.BlobSpec, cause cid.Cid, allocInv ucan.Invocation, allocOK blobcmds.AllocateOK, logger *zap.Logger) (ucan.Invocation, ucan.Receipt, error) {
	log := logger
	log.Debug("generating put invocation")

	// Derive the principal that will provide the blob from the blob digest.
	// we do this so that any actor with a blob could issue a receipt for the
	// `/http/put` invocation. A blob named without its digest derives it from
	// the `/blob/add` task instead, which only the adding client knows.
	keySource := cause.Hash()
	if digest, ok := blob.Digest(); ok {
		keySource = digest
	}
	blobProvider, err := deriveDID(keySource)
	if err != nil {
		return nil, nil, err
	}

	putInv, err := httpcmds.Put.Invoke(
		blobProvider,
		blobProvider.DID(),
		&httpcmds.PutArguments{
			Body:        blob,
			Destination: promise.AwaitOK{Task: allocInv.Task().Link()},
		},
		invocation.WithAudience(blobProvider.DID()),
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
		return nil, nil, fmt.Errorf("invoking %q: %w", httpcmds.Put, err)
	}

	var putRcpt ucan.Receipt

	// If no address was provided we have a blob in store already and we can issue
	// a receipt for the `/http/put` without requiring blob to be provided.
	if allocOK.Address == nil {
		log.Debug("blob present on provider, issuing receipt for put")
		putRcpt, err = receipt.IssueOK(
			blobProvider,
			putInv.Task().Link(),
			&httpcmds.PutOK{},
		)
		if err != nil {
			return nil, nil, fmt.Errorf("issuing %q receipt: %w", httpcmds.Put, err)
		}
	}

	return putInv, putRcpt, nil
}

// Derives did:key principal from (blob) multihash that can be used to
// sign ucan invocations/receipts for the the subject (blob) multihash.
func deriveDID(digest multihash.Multihash) (multikey.Issuer, error) {
	if len(digest) < ed25519.SeedSize {
		return nil, fmt.Errorf("expected []byte with length %d, got %d", ed25519.SeedSize, len(digest))
	}
	seed := digest[len(digest)-ed25519.SeedSize:]
	key, err := ed25519signer.FromRaw(seed)
	if err != nil {
		return nil, fmt.Errorf("failed to create ed25519 signer: %w", err)
	}
	return multikey.KeyIssuer(key), nil
}

// acceptExtras carries the additional invocations / receipts produced by
// piri's accept handler (notably the /assert/location location commitment
// and the /pdp/accept invocation) so they can be forwarded to the client.
// Guppy's blobadd flow looks for /assert/location in the response metadata
// and fails with "blob accept receipt missing location commitment
// invocation" if it isn't there.
type acceptExtras struct {
	invocations []ucan.Invocation
	receipts    []ucan.Receipt
}

// maybeAccept generates and possibly executes a `/blob/accept` invocation if
// the provided put receipt is non-nil and non-failure.
func maybeAccept(
	ctx context.Context,
	agentStore agent.Store,
	blobRegistry blobregistry.Store,
	nodeProvider piriclient.Provider,
	providerInfo routing.StorageProviderInfo,
	space did.DID,
	blob blobcmds.BlobSpec,
	cause cid.Cid, // original /blob/add task
	putInv ucan.Invocation,
	putRcpt ucan.Receipt,
	logger *zap.Logger,
) (ucan.Invocation, ucan.Receipt, acceptExtras, error) {
	log := logger
	log.Debug("generating accept invocation")

	c, err := nodeProvider.Client(providerInfo.ID, providerInfo.Endpoint)
	if err != nil {
		log.Error("failed to create piri client for accept", zap.Error(err))
		return nil, nil, acceptExtras{}, err
	}

	// The proof chain for `/blob/accept` comes from the proofs the provider
	// granted the upload service at registration.
	proofStore := ucanlib.NewContainerProofStore(providerInfo.Proofs)

	accReq := piriclient.AcceptRequest{
		Space: space,
		Blob:  blob,
		Put:   putInv.Task().Link(),
	}

	accInv, _, err := c.AcceptInvocation(ctx, &accReq, proofStore, invocation.WithNoNonce())
	if err != nil {
		log.Error("failed to create accept invocation", zap.Error(err))
		return nil, nil, acceptExtras{}, err
	}

	var accRcpt ucan.Receipt
	var extras acceptExtras

	// If put has already succeeded, we can execute `/blob/accept` right away.
	// That happens only when the provider already holds the blob, which it can
	// know only from its digest.
	if digest, ok := blob.Digest(); ok && putRcpt != nil && putRcpt.Out().IsOK() {
		res, inv, rcpt, meta, err := c.Accept(ctx, &accReq, proofStore, invocation.WithNoNonce())
		if err != nil {
			log.Error("failed to execute accept on piri", zap.Error(err))
			return nil, nil, acceptExtras{}, err
		}
		log.Debug("blob accepted", zap.Stringer("site", res.Site))

		invs := []ucan.Invocation{inv}
		rcpts := []ucan.Receipt{rcpt}
		if meta != nil {
			invs = append(invs, meta.Invocations()...)
			rcpts = append(rcpts, meta.Receipts()...)
			// Capture the piri-side extras (location commitment +
			// /pdp/accept) so blob_add can include them in the response
			// metadata sent to the caller. Without this, guppy can't
			// find the /assert/location invocation it needs.
			// TODO: pull out the exact things we need
			extras.invocations = append(extras.invocations, meta.Invocations()...)
			extras.receipts = append(extras.receipts, meta.Receipts()...)
		}

		err = writeAgentMessage(ctx, agentStore, invs, rcpts)
		if err != nil {
			log.Error("failed to write agent message for accept", zap.Error(err))
			return nil, nil, acceptExtras{}, err
		}

		err = blobRegistry.Register(ctx, space, blobcmds.Blob{Digest: digest, Size: blob.Size()}, cause)
		if err != nil {
			log.Error("failed to register blob", zap.Error(err))
			return nil, nil, acceptExtras{}, err
		}

		accInv = inv
		accRcpt = rcpt
	}

	return accInv, accRcpt, extras, nil
}

// MissingNonceErrorName is the receipt-failure name for a `/blob/add` that
// names no digest and carries no nonce.
const MissingNonceErrorName = "MissingNonce"

// blobField logs a blob by its digest, or by its hash function when it names
// no digest.
func blobField(blob blobcmds.BlobSpec) zap.Field {
	if digest, ok := blob.Digest(); ok {
		return zap.Dict("blob", zap.String("digest", digestutil.Format(digest)), zap.Uint64("size", blob.Size()))
	}
	return zap.Dict("blob", zap.Uint64("digestCode", blob.DigestCode()), zap.Uint64("size", blob.Size()))
}

// hasErrorName reports whether err is, or wraps, a named failure called name,
// such as one decoded from a receipt.
func hasErrorName(err error, name string) bool {
	var named errors.Named
	return errors.As(err, &named) && named.Name() == name
}

// replayAdd answers a /blob/add with the result of an add that already ran:
// addRcpt's success, and the allocate, put and accept tasks behind it. The put
// and accept receipts are included when they exist; an add whose put has not
// been concluded has neither yet.
func replayAdd(ctx context.Context, agentStore agent.Store, addRcpt ucan.Receipt, res *binding.Response[*blobcmds.AddOK], log *zap.Logger) error {
	o, _ := addRcpt.Out().Unpack()
	var addOK blobcmds.AddOK
	if err := addOK.UnmarshalCBOR(bytes.NewReader(o)); err != nil {
		log.Error("failed to unmarshal add OK result", zap.Error(err))
		return fmt.Errorf("unmarshaling add OK result: %w", err)
	}

	accInv, err := agentStore.GetInvocation(ctx, addOK.Site.Task)
	if err != nil {
		log.Error("failed to get invocation for blob accept", zap.Error(err))
		return fmt.Errorf("getting invocation for blob accept: %w", err)
	}
	var accArgs blobcmds.AcceptArguments
	if err := accArgs.UnmarshalCBOR(bytes.NewReader(accInv.ArgumentsBytes())); err != nil {
		log.Error("failed to rebind accept OK result", zap.Error(err))
		return fmt.Errorf("rebinding accept OK result: %w", err)
	}

	putInv, err := agentStore.GetInvocation(ctx, accArgs.Put.Task)
	if err != nil {
		log.Error("failed to get invocation for HTTP PUT", zap.Error(err))
		return fmt.Errorf("getting invocation for HTTP PUT: %w", err)
	}
	var putArgs httpcmds.PutArguments
	if err := putArgs.UnmarshalCBOR(bytes.NewReader(putInv.ArgumentsBytes())); err != nil {
		log.Error("failed to unmarshal HTTP PUT arguments", zap.Error(err))
		return fmt.Errorf("unmarshaling HTTP PUT arguments: %w", err)
	}

	allocRcpt, err := agentStore.GetReceipt(ctx, putArgs.Destination.Task)
	if err != nil {
		log.Error("failed to get receipt for allocation", zap.Error(err))
		return fmt.Errorf("getting receipt for allocation: %w", err)
	}
	allocInv, err := agentStore.GetInvocation(ctx, putArgs.Destination.Task)
	if err != nil {
		log.Error("failed to get invocation for allocation", zap.Error(err))
		return fmt.Errorf("getting invocation for allocation: %w", err)
	}

	rcpts := []ucan.Receipt{allocRcpt}
	for _, task := range []cid.Cid{accArgs.Put.Task, addOK.Site.Task} {
		rcpt, err := agentStore.GetReceipt(ctx, task)
		if errors.Is(err, agent.ErrReceiptNotFound) {
			continue
		}
		if err != nil {
			log.Error("failed to get receipt", zap.Stringer("task", task), zap.Error(err))
			return fmt.Errorf("getting receipt for %s: %w", task, err)
		}
		rcpts = append(rcpts, rcpt)
	}

	res.SetMetadata(container.New(
		container.WithInvocations(allocInv, putInv, accInv),
		container.WithReceipts(rcpts...),
	))
	return res.SetSuccess(&addOK)
}
