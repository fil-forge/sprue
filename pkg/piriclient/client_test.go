package piriclient

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	blobcmds "github.com/fil-forge/libforge/commands/blob"
	"github.com/fil-forge/libforge/identity"
	ucanlib "github.com/fil-forge/libforge/ucan"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/ucantone/binding"
	"github.com/fil-forge/ucantone/client"
	"github.com/fil-forge/ucantone/did/key"
	"github.com/fil-forge/ucantone/did/resolver"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/delegation"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/promise"
	"github.com/fil-forge/ucantone/validator"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// TestAcceptInvocationExpiry pins the two properties a batched accept depends
// on, which pull in opposite directions and are easy to confuse.
//
// The invocation must outlive the whole batch. A node validates each
// invocation immediately before executing it, one at a time, so the last of a
// large batch is checked long after the first was issued — and any clock skew
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
	client, err := New(endpoint, storageProvider.DID(), uploadService, zaptest.NewLogger(t))
	require.NoError(t, err)

	// The provider's registration delegation, as the router hands it over.
	acceptProof := testutil.Must(
		blobcmds.Accept.Delegate(storageProvider, uploadService.DID(), storageProvider.DID(),
			delegation.WithNoExpiration()))(t)
	proofStore := ucanlib.NewContainerProofStore(container.New(container.WithDelegations(acceptProof)))

	req := &AcceptRequest{
		Space:  testutil.RandomDID(t),
		Digest: testutil.RandomMultihash(t),
		Size:   1024,
		Put:    testutil.RandomCID(t),
	}

	inv, _, err := client.AcceptInvocation(ctx, req, proofStore, invocation.WithNoNonce())
	require.NoError(t, err)

	// The invariant is a relationship, not a number: every invocation of a
	// batch is issued before the request goes out, so the last one is
	// validated as the request ends. An expiry that does not outlast a
	// full-length request lets the tail of a batch age out mid-flight.
	require.NotNil(t, inv.Expiration(), "an accept must carry an expiry, not run forever")
	require.Greater(t, int64(*inv.Expiration()), int64(ucan.Now())+int64(piriRequestTimeout.Seconds()),
		"accept expiry must outlast a request that runs to the timeout")

	// Issued again — a different envelope, the same task.
	again, _, err := client.AcceptInvocation(ctx, req, proofStore, invocation.WithNoNonce())
	require.NoError(t, err)
	require.Equal(t, inv.Task().Link(), again.Task().Link(),
		"the accept task link must not depend on when the invocation was issued")
}

// acceptingNode is a storage node that answers /blob/accept, counting the
// requests it receives so a test can tell one batched request from several.
type acceptingNode struct {
	url      *url.URL
	requests atomic.Int64
}

func newAcceptingNode(t *testing.T, node ucan.Issuer, uploadService identity.Identity) *acceptingNode {
	t.Helper()
	n := &acceptingNode{}

	srv := server.NewHTTP(node,
		server.WithValidationOptions(validator.WithDIDResolver(resolver.Tiered{
			resolver.WellKnown{uploadService.DID(): testutil.Must(uploadService.DIDDocument())(t)},
			key.Resolver,
		})),
	)
	srv.Handle(blobcmds.Accept.Command, blobcmds.Accept.Handler(func(
		req *binding.Request[*blobcmds.AcceptArguments],
		res *binding.Response[*blobcmds.AcceptOK],
	) error {
		return res.SetSuccess(&blobcmds.AcceptOK{
			Site: testutil.RandomCID(t),
			PDP:  promise.AwaitOK{Task: testutil.RandomCID(t)},
		})
	}))

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.requests.Add(1)
		srv.ServeHTTP(w, r)
	}))
	t.Cleanup(httpSrv.Close)
	n.url = testutil.Must(url.Parse(httpSrv.URL))(t)
	return n
}

// failAfter fails every request after the first n, so a test can break a
// specific chunk of a batch rather than the whole call.
type failAfter struct {
	inner http.RoundTripper
	n     int64
	calls atomic.Int64
}

func (f *failAfter) RoundTrip(r *http.Request) (*http.Response, error) {
	if f.calls.Add(1) > f.n {
		return nil, fmt.Errorf("node unreachable")
	}
	return f.inner.RoundTrip(r)
}

// acceptFixture is the common setup: a node, the provider proofs it granted
// the upload service, and a client pointed at it.
func acceptFixture(t *testing.T, transport http.RoundTripper) (*Client, ucanlib.ProofStore, *acceptingNode) {
	t.Helper()
	uploadService := testutil.WebService
	node := testutil.RandomIssuer(t)
	accepting := newAcceptingNode(t, node, uploadService)

	opts := []client.HTTPOption{}
	if transport != nil {
		opts = append(opts, client.WithHTTPClient(&http.Client{Transport: transport}))
	}
	c, err := New(accepting.url, node.DID(), uploadService, zaptest.NewLogger(t), opts...)
	require.NoError(t, err)

	acceptProof := testutil.Must(
		blobcmds.Accept.Delegate(node, uploadService.DID(), node.DID(), delegation.WithNoExpiration()))(t)
	store := ucanlib.NewContainerProofStore(container.New(container.WithDelegations(acceptProof)))
	return c, store, accepting
}

func acceptRequests(t *testing.T, n int) []*AcceptRequest {
	t.Helper()
	reqs := make([]*AcceptRequest, n)
	for i := range reqs {
		reqs[i] = &AcceptRequest{
			Space:  testutil.RandomDID(t),
			Digest: testutil.RandomMultihash(t),
			Size:   1024,
			Put:    testutil.RandomCID(t),
		}
	}
	return reqs
}

// TestAcceptBatchChunks pins the chunking boundary: more accepts than fit in
// one request are split across several, and every accept still comes back
// with its own receipt, in the order it was asked for.
func TestAcceptBatchChunks(t *testing.T) {
	c, proofs, node := acceptFixture(t, nil)

	// One past the boundary, so the last chunk is a partial one.
	reqs := acceptRequests(t, maxAcceptBatch+1)
	results, metas, err := c.AcceptBatch(t.Context(), reqs, proofs, invocation.WithNoNonce())
	require.NoError(t, err)

	require.EqualValues(t, 2, node.requests.Load(), "one past the cap must split into exactly two requests")
	require.Len(t, metas, 2)
	require.Len(t, results, len(reqs))
	for i, res := range results {
		require.Same(t, reqs[i], res.Request, "results must stay in the order asked for")
		require.NotNil(t, res.Receipt, "accept %d of %d lost its receipt", i, len(reqs))
		require.Equal(t, res.Invocation.Task().Link(), res.Receipt.Ran())
	}
}

// TestAcceptBatchIssuesEachChunkLate pins when invocations are issued. An
// expiry runs from the moment of issuing, so issuing the whole batch up front
// would have a later chunk spend its life queued behind the requests ahead of
// it — each request inside the timeout, yet the tail expired on arrival. The
// node records the expiry it is handed, and a chunk issued after the previous
// request finished carries a later one.
func TestAcceptBatchIssuesEachChunkLate(t *testing.T) {
	uploadService := testutil.WebService
	node := testutil.RandomIssuer(t)
	accepting := newAcceptingNode(t, node, uploadService)

	// Hold the first request open past a clock tick, so a chunk issued after
	// it is distinguishable from one issued before it.
	slow := &delayFirst{inner: http.DefaultTransport, delay: 1100 * time.Millisecond}
	c, err := New(accepting.url, node.DID(), uploadService, zaptest.NewLogger(t),
		client.WithHTTPClient(&http.Client{Transport: slow}))
	require.NoError(t, err)

	acceptProof := testutil.Must(
		blobcmds.Accept.Delegate(node, uploadService.DID(), node.DID(), delegation.WithNoExpiration()))(t)
	proofs := ucanlib.NewContainerProofStore(container.New(container.WithDelegations(acceptProof)))

	reqs := acceptRequests(t, maxAcceptBatch+1)
	results, _, err := c.AcceptBatch(t.Context(), reqs, proofs, invocation.WithNoNonce())
	require.NoError(t, err)

	first := *results[0].Invocation.Expiration()
	last := *results[len(results)-1].Invocation.Expiration()
	require.Greater(t, int64(last), int64(first),
		"the second chunk must be issued after the first request, not alongside it")
}

// delayFirst holds the first request open, leaving later ones untouched.
type delayFirst struct {
	inner http.RoundTripper
	delay time.Duration
	calls atomic.Int64
}

func (d *delayFirst) RoundTrip(r *http.Request) (*http.Response, error) {
	if d.calls.Add(1) == 1 {
		time.Sleep(d.delay)
	}
	return d.inner.RoundTrip(r)
}

// TestAcceptBatchKeepsCompletedChunks pins that a chunk failing does not
// discard the ones before it. Those accepts have already run on the node, so
// losing them here would leave it holding blobs the upload service has no
// record of.
func TestAcceptBatchKeepsCompletedChunks(t *testing.T) {
	c, proofs, _ := acceptFixture(t, &failAfter{inner: http.DefaultTransport, n: 1})

	reqs := acceptRequests(t, maxAcceptBatch+1)
	results, metas, err := c.AcceptBatch(t.Context(), reqs, proofs, invocation.WithNoNonce())

	require.Error(t, err, "the second chunk failed, so the call reports an error")
	require.Len(t, metas, 1, "the completed chunk's response is kept")
	require.Len(t, results, len(reqs), "every request still has a result slot")

	// The first chunk ran and must be preserved; the rest never did.
	for i := 0; i < maxAcceptBatch; i++ {
		require.NotNil(t, results[i].Receipt, "completed accept %d was discarded", i)
	}
	for i := maxAcceptBatch; i < len(reqs); i++ {
		require.Nil(t, results[i].Receipt, "accept %d never ran, so it has no receipt", i)
	}
}
