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
	SetContainerTokenBudget(t, 4)

	// Distinct artifacts, since a container deduplicates by link.
	invs := make([]ucan.Invocation, 7)
	for i := range invs {
		invs[i] = testInvocation(t, i)
	}

	rec := &recordingAgentStore{}
	require.NoError(t, writeAgentMessages(context.Background(), rec, invs, nil))

	require.Greater(t, len(rec.sizes), 1, "artifacts beyond the budget must span several messages")
	var total int
	for _, size := range rec.sizes {
		require.LessOrEqual(t, size, 4, "no single message may exceed the budget")
		total += size
	}
	require.Equal(t, len(invs), total, "every artifact must be written exactly once")
}

// A conclusion within the budget still writes exactly one message.
func TestWriteAgentMessagesSingleMessage(t *testing.T) {
	SetContainerTokenBudget(t, 64)

	invs := []ucan.Invocation{testInvocation(t, 0), testInvocation(t, 1)}
	rec := &recordingAgentStore{}
	require.NoError(t, writeAgentMessages(context.Background(), rec, invs, nil))
	require.Equal(t, []int{2}, rec.sizes)
}
