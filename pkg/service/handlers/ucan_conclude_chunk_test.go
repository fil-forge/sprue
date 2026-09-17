package handlers

import (
	"context"
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store"
	"github.com/fil-forge/sprue/pkg/store/agent"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/command"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/require"
)

// recordingAgentStore captures the size of every agent message written.
type recordingAgentStore struct {
	sizes []int
}

func (r *recordingAgentStore) Write(_ context.Context, message ucan.Container, _ []agent.IndexEntry) error {
	r.sizes = append(r.sizes, len(message.Invocations())+len(message.Receipts())+len(message.Delegations()))
	return nil
}

func (r *recordingAgentStore) GetInvocation(context.Context, cid.Cid) (ucan.Invocation, error) {
	return nil, agent.ErrInvocationNotFound
}

func (r *recordingAgentStore) GetReceipt(context.Context, cid.Cid) (ucan.Receipt, error) {
	return nil, agent.ErrReceiptNotFound
}

func (r *recordingAgentStore) List(context.Context, cid.Cid, ...agent.ListOption) (store.Page[ucan.Container], error) {
	return store.Page[ucan.Container]{}, nil
}

// testInvocation issues a distinct invocation, so a container's
// deduplication by link does not silently collapse the fixtures.
func testInvocation(t *testing.T, n int) ucan.Invocation {
	t.Helper()
	iss := testutil.RandomIssuer(t)
	inv, err := invocation.Invoke(iss, iss.DID(), command.MustParse("/test/thing"),
		datamodel.Map{"n": int64(n)})
	require.NoError(t, err)
	return inv
}

// TestWriteAgentMessagesChunks pins that persistence splits into
// container-sized messages. A container cannot encode more tokens than the
// budget allows, so a conclusion large enough to exceed it has to be written
// as several messages — otherwise the record of acceptances the node has
// already performed is lost at the encode step.
func TestWriteAgentMessagesChunks(t *testing.T) {
	setContainerTokenBudget(t, 4)

	// Distinct artifacts, since a container deduplicates by link.
	acceptances := make([]acceptance, 7)
	for i := range acceptances {
		acceptances[i] = acceptance{invs: []ucan.Invocation{testInvocation(t, i)}}
	}

	rec := &recordingAgentStore{}
	require.NoError(t, writeAgentMessages(context.Background(), rec, acceptances))

	require.Greater(t, len(rec.sizes), 1, "artifacts beyond the budget must span several messages")
	var total int
	for _, size := range rec.sizes {
		require.LessOrEqual(t, size, 4, "no single message may exceed the budget")
		total += size
	}
	require.Equal(t, len(acceptances), total, "every artifact must be written exactly once")
}

// An acceptance is what the deliverer polls for, so chunking packs whole
// acceptances and never splits one across messages, even where splitting
// would pack the budget more tightly.
func TestWriteAgentMessagesKeepsAcceptancesWhole(t *testing.T) {
	setContainerTokenBudget(t, 4)

	// Three tokens each: two fit in no single message of four.
	acceptances := make([]acceptance, 3)
	for i := range acceptances {
		acceptances[i] = acceptance{invs: []ucan.Invocation{
			testInvocation(t, i*3), testInvocation(t, i*3+1), testInvocation(t, i*3+2),
		}}
	}

	rec := &recordingAgentStore{}
	require.NoError(t, writeAgentMessages(context.Background(), rec, acceptances))
	require.Equal(t, []int{3, 3, 3}, rec.sizes, "each message holds whole acceptances")
}

// A conclusion within the budget still writes exactly one message.
func TestWriteAgentMessagesSingleMessage(t *testing.T) {
	setContainerTokenBudget(t, 64)

	acceptances := []acceptance{{invs: []ucan.Invocation{testInvocation(t, 0), testInvocation(t, 1)}}}
	rec := &recordingAgentStore{}
	require.NoError(t, writeAgentMessages(context.Background(), rec, acceptances))
	require.Equal(t, []int{2}, rec.sizes)
}

// setContainerTokenBudget lowers the response and agent-message token budget
// for the duration of a test, so the oversized-conclusion paths can be
// exercised with a handful of blobs instead of the couple of thousand the
// real budget needs.
func setContainerTokenBudget(t *testing.T, tokens int) {
	previous := containerTokenBudget
	containerTokenBudget = tokens
	t.Cleanup(func() { containerTokenBudget = previous })
}
