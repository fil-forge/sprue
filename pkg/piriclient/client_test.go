package piriclient_test

import (
	"net/url"
	"testing"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	ucanlib "github.com/fil-forge/libforge/ucan"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/piriclient"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/delegation"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// TestAcceptInvocationExpiry pins the two properties a batched accept depends
// on, which pull in opposite directions and are easy to confuse.
//
// The invocation must outlive the whole batch. A node validates each
// invocation immediately before executing it, one at a time, so the last of a
// large batch is checked long after the first was minted — and any clock skew
// between the two hosts comes out of the same budget. The 30-second default
// does not survive that.
//
// The task link must not move, because it is what the client was handed as
// AddOK.Site.Task and is what it polls the receipts endpoint for. Expiry sits
// on the invocation envelope, not in the task, so a longer expiry leaves the
// link alone — this test is what keeps the two facts from drifting apart.
func TestAcceptInvocationExpiry(t *testing.T) {
	ctx := t.Context()
	uploadService := testutil.WebService
	storageProvider := testutil.RandomIssuer(t)

	endpoint := testutil.Must(url.Parse("https://piri.example"))(t)
	client, err := piriclient.New(endpoint, storageProvider.DID(), uploadService, zaptest.NewLogger(t))
	require.NoError(t, err)

	// The provider's registration delegation, as the router hands it over.
	acceptProof := testutil.Must(
		blobcmds.Accept.Delegate(storageProvider, uploadService.DID(), storageProvider.DID(),
			delegation.WithNoExpiration()))(t)
	proofStore := ucanlib.NewContainerProofStore(container.New(container.WithDelegations(acceptProof)))

	req := &piriclient.AcceptRequest{
		Space:  testutil.RandomDID(t),
		Digest: testutil.RandomMultihash(t),
		Size:   1024,
		Put:    testutil.RandomCID(t),
	}

	inv, _, err := client.AcceptInvocation(ctx, req, proofStore, invocation.WithNoNonce())
	require.NoError(t, err)

	// Comfortably beyond the 30s default, so a long batch cannot age out
	// mid-flight.
	require.NotNil(t, inv.Expiration(), "an accept must carry an expiry, not run forever")
	require.Greater(t, int64(*inv.Expiration()), int64(ucan.Now())+20*60,
		"accept expiry must outlast a whole batch, not a single call")

	// Minted again — a different envelope, the same task.
	again, _, err := client.AcceptInvocation(ctx, req, proofStore, invocation.WithNoNonce())
	require.NoError(t, err)
	require.Equal(t, inv.Task().Link(), again.Task().Link(),
		"the accept task link must not depend on when the invocation was minted")
}
