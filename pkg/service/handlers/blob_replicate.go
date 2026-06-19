package handlers

// import (
// 	"bytes"
// 	"context"
// 	"fmt"
// 	"slices"

// 	"go.uber.org/zap"

// 	"github.com/fil-forge/ucantone/did"
// 	"github.com/fil-forge/ucantone/errors"
// 	"github.com/multiformats/go-multihash"
// 	"github.com/storacha/go-libstoracha/capabilities/assert"
// 	blobreplicacap "github.com/storacha/go-libstoracha/capabilities/blob/replica"
// 	spaceblobcap "github.com/storacha/go-libstoracha/capabilities/space/blob"
// 	"github.com/storacha/go-libstoracha/capabilities/types"
// 	"github.com/storacha/go-libstoracha/digestutil"
// 	"github.com/storacha/go-ucanto/core/dag/blockstore"
// 	"github.com/storacha/go-ucanto/core/delegation"
// 	"github.com/storacha/go-ucanto/core/invocation"
// 	"github.com/storacha/go-ucanto/core/ipld"
// 	"github.com/storacha/go-ucanto/core/receipt"
// 	"github.com/storacha/go-ucanto/core/receipt/fx"
// 	"github.com/storacha/go-ucanto/core/result"
// 	"github.com/storacha/go-ucanto/core/result/failure"
// 	fdm "github.com/storacha/go-ucanto/core/result/failure/datamodel"
// 	"github.com/storacha/go-ucanto/server"
// 	"github.com/storacha/go-ucanto/ucan"
// 	"github.com/storacha/go-ucanto/validator"
// 	"github.com/fil-forge/sprue/internal/config"
//
// 	"github.com/fil-forge/sprue/pkg/internal/ipldutil"
// 	"github.com/fil-forge/sprue/pkg/piriclient"
// 	"github.com/fil-forge/sprue/pkg/routing"
// 	"github.com/fil-forge/sprue/pkg/store/agent"
// 	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
// 	"github.com/fil-forge/sprue/pkg/store/replica"
// )

// const (
// 	// Too many or too few replicas were instructed.
// 	ReplicationCountRangeErrorName = "ReplicationCountRangeError"
// 	// There are not enough replication nodes available to replicate the data.
// 	ReplicationCandidateUnavailableErrorName = "ReplicationCandidateUnavailable"
// 	// Blob to replicate was not found in the space.
// 	ReplicationSourceNotFoundErrorName = "ReplicationSourceNotFound"
// 	// The location commitment was invalid in some way. For example, it has
// 	// expired, is revoked, had a signature that did not verify or referenced a
// 	// blob that was not requested to be replicated.
// 	InvalidReplicationSiteErrorName = "InvalidReplicationSite"
// )

// var (
// 	ErrReplicationSourceNotFound       = errors.New(ReplicationSourceNotFoundErrorName, "blob to replicate was not found in the space")
// 	ErrReplicationCandidateUnavailable = errors.New(ReplicationCandidateUnavailableErrorName, "no replication candidates available")
// )

// // WithSpaceBlobReplicateMethod registers the space/blob/replicate handler.
// func WithSpaceBlobReplicateMethod(
// 	cfg config.DeploymentConfig,
// 	id identity.Identity,
// 	router *routing.Service,
// 	blobRegistry blobregistry.Store,
// 	replicaStore replica.Store,
// 	agentStore agent.Store,
// 	storageNode piriclient.Provider,
// 	logger *zap.Logger,
// ) server.Option {
// 	return server.WithServiceMethod(
// 		spaceblobcap.ReplicateAbility,
// 		server.Provide(
// 			spaceblobcap.Replicate,
// 			SpaceBlobReplicateHandler(cfg, id, router, blobRegistry, replicaStore, agentStore, storageNode, logger),
// 		),
// 	)
// }

// func BlobReplicateHandler(
// 	cfg config.DeploymentConfig,
// 	id identity.Identity,
// 	router *routing.Service,
// 	blobRegistry blobregistry.Store,
// 	replicaStore replica.Store,
// 	agentStore agent.Store,
// 	storageNode piriclient.Provider,
// 	logger *zap.Logger,
// ) server.HandlerFunc[spaceblobcap.ReplicateCaveats, spaceblobcap.ReplicateOk, failure.IPLDBuilderFailure] {
// 	log := logger.With(zap.String("handler", spaceblobcap.ReplicateAbility))
// 	return func(ctx context.Context,
// 		cap ucan.Capability[spaceblobcap.ReplicateCaveats],
// 		inv invocation.Invocation,
// 		iCtx server.InvocationContext,
// 	) (result.Result[spaceblobcap.ReplicateOk, failure.IPLDBuilderFailure], fx.Effects, error) {
// 		space, err := did.Parse(cap.With())
// 		if err != nil {
// 			return result.Error[spaceblobcap.ReplicateOk, failure.IPLDBuilderFailure](
// 				errors.New(InvalidSpaceErrorName, "invalid space DID: %v", err),
// 			), nil, nil
// 		}
// 		blob := cap.Nb().Blob
// 		replicas := cap.Nb().Replicas

// 		log := log.With(
// 			zap.Stringer("space", space),
// 			zap.Dict(
// 				"blob",
// 				zap.String("digest", digestutil.Format(blob.Digest)),
// 				zap.Uint64("size", blob.Size),
// 			),
// 			zap.Uint("replicas", replicas),
// 		)
// 		log.Debug("replicating blob")

// 		if replicas > cfg.MaxReplicas {
// 			log.Warn("replication count out of range")
// 			return result.Error[spaceblobcap.ReplicateOk, failure.IPLDBuilderFailure](
// 				errors.New(ReplicationCountRangeErrorName, "requested number of replicas is greater than maximum: %d", cfg.MaxReplicas),
// 			), nil, nil
// 		}

// 		_, err = blobRegistry.Get(ctx, space, blob.Digest)
// 		if err != nil {
// 			if errors.Is(err, blobregistry.ErrEntryNotFound) {
// 				log.Warn("replication source not found in space")
// 				return result.Error[spaceblobcap.ReplicateOk, failure.IPLDBuilderFailure](
// 					ErrReplicationSourceNotFound,
// 				), nil, nil
// 			}
// 			log.Error("failed to get blob registration")
// 			return nil, nil, fmt.Errorf("getting blob registration: %w", err)
// 		}

// 		// check if we have any active replications
// 		records, err := replicaStore.List(ctx, space, blob.Digest)
// 		if err != nil {
// 			log.Error("failed to list replicas", zap.Error(err))
// 			return nil, nil, fmt.Errorf("listing replicas: %w", err)
// 		}

// 		// TODO: handle the case where a receipt was not received and the replica
// 		// still exists in "allocated", but has actually timed out/failed.

// 		var activeReplicas []replica.Record
// 		var failedReplicas []replica.Record

// 		var allocTasks []invocation.Invocation
// 		var allocReceipts []receipt.AnyReceipt
// 		var transferTasks []invocation.Invocation
// 		var transferReceipts []receipt.AnyReceipt

// 		for _, r := range records {
// 			if r.Status == replica.Failed {
// 				failedReplicas = append(failedReplicas, r)
// 			} else {
// 				detail, err := replicaFxDetail(ctx, agentStore, r, logger)
// 				if err != nil {
// 					log.Error("failed to get replica details", zap.Error(err))
// 					return nil, nil, fmt.Errorf("getting replica details: %w", err)
// 				}
// 				activeReplicas = append(activeReplicas, r)
// 				allocTasks = append(allocTasks, detail.allocate.invocation)
// 				allocReceipts = append(allocReceipts, detail.allocate.receipt)
// 				if detail.transfer != nil {
// 					transferTasks = append(transferTasks, detail.transfer.invocation)
// 					if detail.transfer.receipt != nil {
// 						transferReceipts = append(transferReceipts, detail.transfer.receipt)
// 					}
// 				}
// 			}
// 		}

// 		// Note: We +1 below to include the source blob, which is not recorded in
// 		// the replicas table.
// 		newReplicasCount := int(replicas) - (len(activeReplicas) + 1)

// 		// TODO: support reducing the number of replicas
// 		if newReplicasCount < 0 {
// 			return result.Error[spaceblobcap.ReplicateOk, failure.IPLDBuilderFailure](
// 				errors.New(ReplicationCountRangeErrorName, "reducing replica count not implemented"),
// 			), nil, nil
// 		}

// 		// lets allocate some replicas!
// 		if newReplicasCount > 0 {
// 			blocks, err := blockstore.NewBlockReader(blockstore.WithBlocksIterator(inv.Blocks()))
// 			if err != nil {
// 				return nil, nil, fmt.Errorf("creating block reader: %w", err)
// 			}
// 			site := cap.Nb().Site
// 			lComm, location, err := extractLocationCommitment(space, blob.Digest, site, blocks)
// 			if err != nil {
// 				log.Warn("failed to extract location commitment", zap.Stringer("site", site), zap.Error(err))
// 				return result.Error[spaceblobcap.ReplicateOk, failure.IPLDBuilderFailure](
// 					errors.New(InvalidReplicationSiteErrorName, "invalid location commitment: %s", err.Error()),
// 				), nil, nil
// 			}
// 			_, err = validator.Claim(
// 				ctx,
// 				assert.Location,
// 				[]delegation.Proof{delegation.FromDelegation(lComm)},
// 				validator.NewClaimContext(
// 					id.Signer.Verifier(),
// 					iCtx.CanIssue,
// 					iCtx.ValidateAuthorization,
// 					iCtx.ResolveProof,
// 					iCtx.ParsePrincipal,
// 					iCtx.ResolveDIDKey,
// 					iCtx.ValidateTimeBounds,
// 					iCtx.AuthorityProofs()...,
// 				),
// 			)
// 			if err != nil {
// 				log.Warn("failed to authorize location commitment", zap.Stringer("site", site), zap.Error(err))
// 				return result.Error[spaceblobcap.ReplicateOk, failure.IPLDBuilderFailure](
// 					errors.New(InvalidReplicationSiteErrorName, "unauthorized location commitment: %s", err.Error()),
// 				), nil, nil
// 			}

// 			urls := make([]string, 0, len(location.Location))
// 			for _, url := range location.Location {
// 				urls = append(urls, url.String())
// 			}
// 			siteLogFields := []zap.Field{zap.Stringer("root", site), zap.Strings("locations", urls)}
// 			if location.Range != nil {
// 				siteLogFields = append(siteLogFields, zap.Uint64("offset", location.Range.Offset))
// 				if location.Range.Length != nil {
// 					siteLogFields = append(siteLogFields, zap.Uint64("length", *location.Range.Length))
// 				}
// 			}
// 			log = log.With(zap.Dict("site", siteLogFields...))
// 			log.Debug("allocating space to replicate blob")

// 			// do not include any nodes where we already have replications
// 			var exclude []ucan.Principal
// 			for _, r := range activeReplicas {
// 				exclude = append(exclude, r.Provider)
// 			}

// 			for range newReplicasCount {
// 				for {
// 					candidate, err := router.SelectReplicationProvider(ctx, lComm.Issuer(), blob, routing.WithExclusions(exclude...))
// 					if err != nil {
// 						if errors.Is(err, routing.ErrCandidateUnavailable) {
// 							log.Warn("no replication candidates available")
// 							return result.Error[spaceblobcap.ReplicateOk, failure.IPLDBuilderFailure](
// 								ErrReplicationCandidateUnavailable,
// 							), nil, nil
// 						}
// 						log.Error("failed to select replication provider", zap.Error(err))
// 						return nil, nil, fmt.Errorf("selecting replication provider: %w", err)
// 					}

// 					log.Debug("selected replication provider", zap.Stringer("provider", candidate.ID.DID()))
// 					client, err := storageNode.Client(candidate.ID, candidate.Endpoint)
// 					if err != nil {
// 						log.Error("failed to create storage node client", zap.Error(err))
// 						return nil, nil, fmt.Errorf("creating storage node client: %w", err)
// 					}

// 					allocRes, allocInv, allocRcpt, err := client.ReplicaAllocate(ctx, &piriclient.ReplicaAllocateRequest{
// 						Space:  space,
// 						Digest: blob.Digest,
// 						Size:   blob.Size,
// 						Site:   lComm,
// 						Cause:  inv.Link(),
// 					}, delegationFetcher{proof: candidate.Proof})
// 					if err != nil {
// 						log.Warn("failed to allocate replica", zap.Error(err))
// 						exclude = append(exclude, candidate.ID)
// 						continue
// 					}

// 					// record the invocation and the receipt, so we can retrieve it later
// 					// when we get a blob/replica/transfer receipt in ucan/conclude
// 					err = writeAgentMessage(ctx, agentStore, []invocation.Invocation{allocInv}, []receipt.AnyReceipt{allocRcpt})
// 					if err != nil {
// 						log.Error("failed to write agent message", zap.Error(err))
// 						return nil, nil, fmt.Errorf("writing agent message: %w", err)
// 					}

// 					// write a replication record to the store
// 					firstTimeReplica := !slices.ContainsFunc(failedReplicas, func(r replica.Record) bool {
// 						return r.Provider.DID() == candidate.ID.DID()
// 					})
// 					status := result.MatchResultR1(
// 						allocRcpt.Out(),
// 						func(o ipld.Node) replica.ReplicationStatus {
// 							return replica.Allocated
// 						},
// 						func(x ipld.Node) replica.ReplicationStatus {
// 							return replica.Failed
// 						},
// 					)
// 					cause, err := ipldutil.ToCID(allocInv.Link())
// 					if err != nil {
// 						return nil, nil, err
// 					}
// 					if firstTimeReplica {
// 						err = replicaStore.Add(ctx, space, blob.Digest, candidate.ID.DID(), status, cause)
// 					} else {
// 						err = replicaStore.Retry(ctx, space, blob.Digest, candidate.ID.DID(), status, cause)
// 					}
// 					if err != nil {
// 						log.Error("failed to store replica record", zap.Error(err))
// 						return nil, nil, fmt.Errorf("storing replica record: %w", err)
// 					}

// 					allocTasks = append(allocTasks, allocInv)
// 					allocReceipts = append(allocReceipts, allocRcpt)
// 					transferTasks = append(transferTasks, allocRes.Transfer)
// 					// exclude this provider from next candidate selection (in case there
// 					// are more replicas to be allocated).
// 					exclude = append(exclude, candidate.ID)
// 					break
// 				}
// 			}
// 		}

// 		var res spaceblobcap.ReplicateOk
// 		for _, t := range transferTasks {
// 			res.Site = append(res.Site, types.Promise{
// 				UcanAwait: types.Await{
// 					Selector: blobreplicacap.AllocateSiteSelector,
// 					Link:     t.Link(),
// 				},
// 			})
// 		}

// 		forks := []fx.Effect{}
// 		for _, t := range allocTasks {
// 			forks = append(forks, fx.FromInvocation(t))
// 		}
// 		for _, t := range transferTasks {
// 			forks = append(forks, fx.FromInvocation(t))
// 		}
// 		for _, r := range allocReceipts {
// 			// as a temporary solution we fork all allocate effects that add inline
// 			// receipts so they can be delivered to the client.
// 			conclude, err := issueConclude(id.Signer, r)
// 			if err != nil {
// 				log.Error("failed to create conclude invocation for replica allocate receipt", zap.Error(err))
// 				return nil, nil, fmt.Errorf("creating conclude invocation: %w", err)
// 			}
// 			forks = append(forks, fx.FromInvocation(conclude))
// 		}
// 		for _, r := range transferReceipts {
// 			// as a temporary solution we fork all transfer effects that add inline
// 			// receipts so they can be delivered to the client.
// 			conclude, err := issueConclude(id.Signer, r)
// 			if err != nil {
// 				log.Error("failed to create conclude invocation for replica transfer receipt", zap.Error(err))
// 				return nil, nil, fmt.Errorf("creating conclude invocation: %w", err)
// 			}
// 			forks = append(forks, fx.FromInvocation(conclude))
// 		}

// 		fx := fx.NewEffects(fx.WithFork(forks...))

// 		return result.Ok[spaceblobcap.ReplicateOk, failure.IPLDBuilderFailure](res), fx, nil
// 	}
// }

// type transaction struct {
// 	invocation invocation.Invocation
// 	receipt    receipt.AnyReceipt
// }

// type replicaDetail struct {
// 	allocate transaction
// 	transfer *transaction
// }

// // Retrieves details of effect chain for replica allocations.
// //
// // If the allocation failed (receipt in error) then the return value will not
// // include any details about the transfer. i.e. `transfer` will be `nil`.
// //
// // If the receipt for `blob/replica/transfer` was not yet received, it will not
// // be included in the return value. i.e. `transfer.receipt` will be `nil`.
// func replicaFxDetail(ctx context.Context, agentStore agent.Store, rec replica.Record, logger *zap.Logger) (replicaDetail, error) {
// 	log := logger.With(zap.Stringer("allocation", rec.Cause))

// 	allocRcpt, err := agentStore.GetReceipt(ctx, rec.Cause)
// 	if err != nil {
// 		log.Error("failed to get replica allocation receipt", zap.Error(err))
// 		return replicaDetail{}, fmt.Errorf("getting allocation receipt: %w", err)
// 	}

// 	// receipt typically contains invocation
// 	allocInv, ok := allocRcpt.Ran().Invocation()
// 	if !ok {
// 		allocInv, err = agentStore.GetInvocation(ctx, rec.Cause)
// 		if err != nil {
// 			log.Error("failed to get replica allocation invocation", zap.Error(err))
// 			return replicaDetail{}, fmt.Errorf("getting allocation invocation: %w", err)
// 		}
// 	}

// 	o, x := result.Unwrap(allocRcpt.Out())
// 	// if allocation failed, we cannot provide details for transfer
// 	if x != nil {
// 		log.Error("cannot get transfer details because allocation failed", zap.Error(fdm.Bind(x)))
// 		return replicaDetail{
// 			allocate: transaction{
// 				invocation: allocInv,
// 				receipt:    allocRcpt,
// 			},
// 		}, nil
// 	}

// 	allocOk, err := ipld.Rebind[blobreplicacap.AllocateOk](o, blobreplicacap.AllocateOkType(), types.Converters...)
// 	if err != nil {
// 		log.Error("failed to rebind allocation result", zap.Error(err))
// 		return replicaDetail{}, fmt.Errorf("rebinding allocation result: %w", err)
// 	}

// 	transferTask, err := ipldutil.ToCID(allocOk.Site.UcanAwait.Link)
// 	if err != nil {
// 		return replicaDetail{}, err
// 	}
// 	log = log.With(zap.Stringer("transfer", transferTask))

// 	var transferInv invocation.Invocation
// 	transferRcpt, err := agentStore.GetReceipt(ctx, transferTask)
// 	if err != nil {
// 		if !errors.Is(err, agent.ErrReceiptNotFound) {
// 			log.Error("failed to get replica transfer receipt", zap.Error(err))
// 			return replicaDetail{}, fmt.Errorf("getting transfer receipt: %w", err)
// 		}
// 		log.Debug("transfer receipt not found, may still be in progress")
// 	}

// 	if transferRcpt != nil {
// 		transferInv, _ = transferRcpt.Ran().Invocation()
// 	}
// 	if transferInv == nil {
// 		transferInv, err = agentStore.GetInvocation(ctx, transferTask)
// 		if err != nil {
// 			log.Error("failed to get replica transfer invocation", zap.Error(err))
// 			return replicaDetail{}, fmt.Errorf("getting transfer invocation: %w", err)
// 		}
// 	}

// 	return replicaDetail{
// 		allocate: transaction{
// 			invocation: allocInv,
// 			receipt:    allocRcpt,
// 		},
// 		transfer: &transaction{
// 			invocation: transferInv,
// 			receipt:    transferRcpt,
// 		},
// 	}, nil
// }

// func extractLocationCommitment(space did.DID, digest multihash.Multihash, root ipld.Link, blocks blockstore.BlockReader) (delegation.Delegation, assert.LocationCaveats, error) {
// 	lComm, err := delegation.NewDelegationView(root, blocks)
// 	if err != nil {
// 		return nil, assert.LocationCaveats{}, fmt.Errorf("creating location commitment: %w", err)
// 	}
// 	if len(lComm.Capabilities()) == 0 {
// 		return nil, assert.LocationCaveats{}, fmt.Errorf("missing capabilities")
// 	}
// 	match, err := assert.Location.Match(validator.NewSource(lComm.Capabilities()[0], lComm))
// 	if err != nil {
// 		return nil, assert.LocationCaveats{}, fmt.Errorf("matching caveats: %w", err)
// 	}
// 	nb := match.Value().Nb()
// 	if nb.Space != space {
// 		return nil, assert.LocationCaveats{}, fmt.Errorf("space mismatch: expected %s, got %s", space, nb.Space)
// 	}
// 	if !bytes.Equal(nb.Content.Hash(), digest) {
// 		return nil, assert.LocationCaveats{}, fmt.Errorf("digest mismatch: expected %s, got %s", digestutil.Format(digest), digestutil.Format(nb.Content.Hash()))
// 	}
// 	return lComm, nb, nil
// }
