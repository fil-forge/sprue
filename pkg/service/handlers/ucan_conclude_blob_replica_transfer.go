package handlers

import (
	"bytes"
	"context"
	"fmt"

	replicacaps "github.com/fil-forge/go-libstoracha/capabilities/blob/replica"
	ucancaps "github.com/fil-forge/go-libstoracha/capabilities/ucan"
	"github.com/fil-forge/go-libstoracha/digestutil"
	"github.com/fil-forge/go-ucanto/core/delegation"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/receipt"
	"github.com/fil-forge/go-ucanto/core/result"
	fdm "github.com/fil-forge/go-ucanto/core/result/failure/datamodel"
	"github.com/fil-forge/go-ucanto/principal"
	"github.com/fil-forge/go-ucanto/principal/ed25519/verifier"
	"github.com/fil-forge/go-ucanto/server"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/go-ucanto/validator"
	"github.com/fil-forge/sprue/pkg/identity"
	"github.com/fil-forge/sprue/pkg/internal/ipldutil"
	"github.com/fil-forge/sprue/pkg/lib/errors"
	"github.com/fil-forge/sprue/pkg/store/agent"
	"github.com/fil-forge/sprue/pkg/store/replica"
	"go.uber.org/zap"
)

const (
	InvalidReplicaTransferArgsErrorName      = "InvalidReplicaTransferArgs"
	InvalidReplicaTransferCauseErrorName     = "InvalidReplicaTransferCause"
	UnknownReplicaAllocationErrorName        = "UnknownReplicaAllocation"
	ReplicaTransferArgMismatchErrorName      = "ReplicaTransferArgMismatch"
	ReplicaAllocationFailedErrorName         = "ReplicaAllocationFailed"
	InvalidTransferReceiptSignatureErrorName = "InvalidTransferReceiptSignature"
)

var ErrInvalidTransferReceiptSignature = errors.New(InvalidTransferReceiptSignatureErrorName, "invalid transfer receipt signature")

func NewBlobReplicaTransferConcludeHandler(
	id *identity.Identity,
	agentStore agent.Store,
	replicaStore replica.Store,
	logger *zap.Logger,
) ConclusionHandler {
	log := logger.With(
		zap.String("handler", ucancaps.ConcludeAbility),
		zap.String("conclude", replicacaps.TransferAbility),
	)
	return ConclusionHandler{
		Ability: replicacaps.TransferAbility,
		Handler: func(ctx context.Context, transferTask invocation.Invocation, transferRcpt receipt.AnyReceipt, iCtx server.InvocationContext) error {
			log := log.With(zap.Stringer("ran", transferRcpt.Ran().Link()))
			log.Debug("handling conclude")

			var err error
			transferCap := transferTask.Capabilities()[0]
			transferMatch, err := replicacaps.Transfer.Match(validator.NewSource(transferCap, transferTask))
			if err != nil {
				log.Warn("failed to match replica transfer parameters", zap.Error(err))
				return errors.New(InvalidReplicaTransferArgsErrorName, "invalid replica transfer parameters: %v", err)
			}
			transferArgs := transferMatch.Value().Nb()
			log = log.With(zap.Stringer("allocation", transferArgs.Cause))

			allocTaskLink, err := ipldutil.ToCID(transferArgs.Cause)
			if err != nil {
				return err
			}

			allocTask, err := agentStore.GetInvocation(ctx, allocTaskLink)
			if err != nil {
				log.Error("failed to get replica allocation invocation", zap.Error(err))
				return fmt.Errorf("getting replica allocation invocation: %w", err)
			}

			ok, err := ucan.VerifySignature(allocTask.Data(), id.Signer.Verifier())
			if err != nil {
				log.Error("failed to verify replica allocation invocation signature", zap.Error(err))
				return fmt.Errorf("verifying replica allocation invocation signature: %w", err)
			}
			// shouldn't happen - we should only store invocations made by our service...
			if !ok {
				log.Warn("replica allocation invocation issued by unknown DID", zap.Stringer("issuer", allocTask.Issuer().DID()))
				return errors.New(UnknownReplicaAllocationErrorName, "allocation was not issued by this service")
			}

			if len(allocTask.Capabilities()) != 1 {
				log.Warn("invalid replica allocation invocation: expected exactly 1 capability", zap.Int("capabilities", len(allocTask.Capabilities())))
				return errors.New(InvalidReplicaTransferCauseErrorName, "invalid replica allocation invocation: expected exactly 1 capability, got %d", len(allocTask.Capabilities()))
			}

			allocCap := allocTask.Capabilities()[0]
			allocMatch, err := replicacaps.Allocate.Match(validator.NewSource(allocCap, allocTask))
			if err != nil {
				log.Warn("failed to match replica allocation parameters", zap.Error(err))
				return errors.New(InvalidReplicaTransferCauseErrorName, "invalid replica allocation parameters: %v", err)
			}
			allocArgs := allocMatch.Value().Nb()
			log = log.With(
				zap.Stringer("space", allocArgs.Space),
				zap.Dict(
					"blob",
					zap.String("digest", digestutil.Format(allocArgs.Blob.Digest)),
					zap.Uint64("size", allocArgs.Blob.Size),
				),
			)

			var executor principal.Verifier
			if transferRcpt.Issuer() != nil {
				executor, err = verifier.Parse(transferRcpt.Issuer().DID().String())
			} else {
				executor, err = verifier.Parse(transferTask.Audience().DID().String())
			}
			if err != nil {
				log.Warn("failed to parse executor DID", zap.Error(err))
				return fmt.Errorf("parsing executor DID: %w", err)
			}
			log = log.With(zap.Stringer("executor", executor.DID()))

			if executor.DID() != allocTask.Audience().DID() {
				log.Warn("transfer executor does not match replica allocation audience", zap.Stringer("expected", allocTask.Audience().DID()))
				return errors.New(ReplicaTransferArgMismatchErrorName, "transfer executor does not match replica allocation audience")
			}

			updateReplicaStatus := func(status replica.ReplicationStatus) error {
				err = replicaStore.SetStatus(ctx, allocArgs.Space, allocArgs.Blob.Digest, executor.DID(), status)
				if err != nil {
					log.Error("failed to update replica status", zap.Error(err))
					return err
				}
				return nil
			}

			// verify the receipt was signed by the executor
			ok, err = transferRcpt.VerifySignature(executor)
			if err != nil {
				log.Error("failed to verify receipt signature", zap.Error(err))
				return fmt.Errorf("verifying receipt signature: %w", err)
			}
			if !ok {
				log.Warn("invalid receipt signature", zap.Error(err))
				_ = updateReplicaStatus(replica.Failed)
				return ErrInvalidTransferReceiptSignature
			}

			// verify the executor has delegated capability
			proofs := []delegation.Proof{delegation.FromDelegation(transferTask)}
			proofs = append(proofs, transferRcpt.Proofs()...)
			_, err = validator.Claim(
				ctx,
				replicacaps.Transfer,
				proofs,
				validator.NewClaimContext(
					executor,
					iCtx.CanIssue,
					iCtx.ValidateAuthorization,
					iCtx.ResolveProof,
					iCtx.ParsePrincipal,
					iCtx.ResolveDIDKey,
					iCtx.ValidateTimeBounds,
					iCtx.AuthorityProofs()...,
				),
			)
			if err != nil {
				log.Warn("failed to validate executor capability", zap.Error(err))
				_ = updateReplicaStatus(replica.Failed)
				return fmt.Errorf("validating executor capability: %w", err)
			}

			allocRcpt, err := agentStore.GetReceipt(ctx, allocTaskLink)
			if err != nil {
				log.Error("failed to get replica allocation receipt", zap.Error(err))
				return fmt.Errorf("getting replica allocation receipt: %w", err)
			}

			err = result.MatchResultR1(
				allocRcpt.Out(),
				func(o ipld.Node) error { return nil },
				func(x ipld.Node) error { return fdm.Bind(x) },
			)
			// if the receipt for the allocation was in error we should not be
			// receiving a conclude for the transfer
			if err != nil {
				log.Warn("replica allocation failed", zap.Error(err))
				_ = updateReplicaStatus(replica.Failed)
				return errors.New(ReplicaAllocationFailedErrorName, "Allocation associated with this transfer has failed: %s", err.Error())
			}

			if !bytes.Equal(transferArgs.Blob.Digest, allocArgs.Blob.Digest) ||
				transferArgs.Blob.Size != allocArgs.Blob.Size ||
				transferArgs.Space != allocArgs.Space {
				log.Warn("transfer parameters do not match allocation parameters")
				_ = updateReplicaStatus(replica.Failed)
				return errors.New(ReplicaTransferArgMismatchErrorName, "transfer parameters do not match allocation parameters")
			}

			status := result.MatchResultR1(
				transferRcpt.Out(),
				func(o ipld.Node) replica.ReplicationStatus {
					return replica.Transferred
				},
				func(x ipld.Node) replica.ReplicationStatus {
					log.Warn("replica transfer failed", zap.Error(fdm.Bind(x)))
					return replica.Failed
				},
			)

			err = updateReplicaStatus(status)
			if err != nil {
				return fmt.Errorf("updating replica status: %w", err)
			}

			return nil
		},
	}
}
