package agent_test

import (
	"context"
	"io"
	"runtime"
	"testing"

	ucancap "github.com/fil-forge/go-libstoracha/capabilities/ucan"
	"github.com/fil-forge/go-ucanto/core/car"
	"github.com/fil-forge/go-ucanto/core/invocation"
	"github.com/fil-forge/go-ucanto/core/ipld"
	"github.com/fil-forge/go-ucanto/core/message"
	"github.com/fil-forge/go-ucanto/core/receipt"
	"github.com/fil-forge/go-ucanto/core/receipt/ran"
	"github.com/fil-forge/go-ucanto/core/result"
	"github.com/fil-forge/go-ucanto/core/result/ok"
	"github.com/fil-forge/go-ucanto/ucan"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store/agent"
	"github.com/fil-forge/sprue/pkg/store/agent/aws"
	"github.com/fil-forge/sprue/pkg/store/agent/memory"
	agentpostgres "github.com/fil-forge/sprue/pkg/store/agent/postgres"
	"github.com/google/uuid"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/stretchr/testify/require"
)

type StoreKind string

const (
	Memory   StoreKind = "memory"
	AWS      StoreKind = "aws"
	Postgres StoreKind = "postgres"
)

var storeKinds = []StoreKind{Memory, AWS, Postgres}

func makeStore(t *testing.T, k StoreKind) agent.Store {
	switch k {
	case Memory:
		return memory.New()
	case AWS:
		return createAWSStore(t)
	case Postgres:
		return createPostgresStore(t)
	}
	panic("unknown store kind")
}

func createPostgresStore(t *testing.T) agent.Store {
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}
	pool := testutil.CreatePostgres(t)
	s3Endpoint := testutil.CreateS3(t)
	s3Client := testutil.NewS3Client(t, s3Endpoint)

	store := agentpostgres.New(pool, s3Client, "agent-message-"+uuid.NewString())
	require.NoError(t, store.Initialize(t.Context()))
	t.Cleanup(func() {
		require.NoError(t, store.Shutdown(context.Background()))
	})
	return store
}

func createAWSStore(t *testing.T) agent.Store {
	// This test expects docker to be running in linux CI environments and fails if it's not
	if testutil.IsRunningInCI(t) && runtime.GOOS == "linux" {
		if !testutil.IsDockerAvailable(t) {
			t.Fatalf("docker is expected in CI linux testing environments, but wasn't found")
		}
	}
	// otherwise this test is running locally, skip it if docker isn't available
	if !testutil.IsDockerAvailable(t) {
		t.SkipNow()
	}

	s3Endpoint := testutil.CreateS3(t)
	s3 := testutil.NewS3Client(t, s3Endpoint)

	dynamoEndpoint := testutil.CreateDynamo(t)
	dynamo := testutil.NewDynamoClient(t, dynamoEndpoint)

	id := uuid.NewString()
	store := aws.New(dynamo, "agent-index-"+id, s3, "agent-message-"+id)

	err := store.Initialize(t.Context())
	require.NoError(t, err)

	t.Cleanup(func() {
		err := store.Shutdown(context.Background())
		require.NoError(t, err)
	})
	return store
}

func makeInvocation(t *testing.T) invocation.Invocation {
	t.Helper()
	inv, err := invocation.Invoke(
		testutil.Alice,
		testutil.Bob,
		ucan.NewCapability("test/invoke", testutil.Alice.DID().String(), ucan.NoCaveats{}),
	)
	require.NoError(t, err)
	return inv
}

func makeReceipt(t *testing.T, inv invocation.Invocation) receipt.AnyReceipt {
	t.Helper()
	rcpt, err := receipt.Issue(
		testutil.Alice,
		result.Ok[ok.Unit, ipld.Builder](ok.Unit{}),
		ran.FromInvocation(inv),
	)
	require.NoError(t, err)
	return rcpt
}

func buildAndWrite(t *testing.T, store agent.Store, invocations []invocation.Invocation, receipts []receipt.AnyReceipt) {
	t.Helper()
	msg, err := message.Build(invocations, receipts)
	require.NoError(t, err)

	carReader := car.Encode([]ipld.Link{msg.Root().Link()}, msg.Blocks())

	source, err := io.ReadAll(carReader)
	require.NoError(t, err)

	var index []agent.IndexEntry
	for entry, err := range agent.Index(msg) {
		require.NoError(t, err)
		index = append(index, entry)
	}

	err = store.Write(t.Context(), msg, index, source)
	require.NoError(t, err)
}

func TestAgentStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			store := makeStore(t, k)

			t.Run("gets an invocation", func(t *testing.T) {
				inv := makeInvocation(t)
				buildAndWrite(t, store, []invocation.Invocation{inv}, nil)

				got, err := store.GetInvocation(t.Context(), inv.Link().(cidlink.Link).Cid)
				require.NoError(t, err)
				require.Equal(t, inv.Link().String(), got.Link().String())
			})

			t.Run("returns not found for missing invocation", func(t *testing.T) {
				_, err := store.GetInvocation(t.Context(), testutil.RandomCID(t))
				require.ErrorIs(t, err, agent.ErrInvocationNotFound)
			})

			t.Run("gets a receipt", func(t *testing.T) {
				inv := makeInvocation(t)
				rcpt := makeReceipt(t, inv)
				buildAndWrite(t, store, nil, []receipt.AnyReceipt{rcpt})

				got, err := store.GetReceipt(t.Context(), inv.Link().(cidlink.Link).Cid)
				require.NoError(t, err)
				require.Equal(t, rcpt.Root().Link().String(), got.Root().Link().String())
			})

			t.Run("returns not found for missing receipt", func(t *testing.T) {
				_, err := store.GetReceipt(t.Context(), testutil.RandomCID(t))
				require.ErrorIs(t, err, agent.ErrReceiptNotFound)
			})

			t.Run("gets receipt from ucan/conclude invocation", func(t *testing.T) {
				// Create the task invocation and a receipt for it.
				taskInv := makeInvocation(t)
				rcpt := makeReceipt(t, taskInv)

				// Create a ucan/conclude invocation that carries the receipt as its
				// nb.receipt caveat. This is how agents communicate receipts in-band.
				concludeInv, err := invocation.Invoke(
					testutil.Alice,
					testutil.Bob,
					ucan.NewCapability(
						ucancap.ConcludeAbility,
						testutil.Alice.DID().String(),
						ucancap.ConcludeCaveats{Receipt: rcpt.Root().Link()},
					),
				)
				require.NoError(t, err)

				// The indexer resolves the receipt link against the message blockstore,
				// so the receipt blocks must travel with the conclude invocation. Attach
				// them directly so they are included when the message is built.
				for blk, err := range rcpt.Blocks() {
					require.NoError(t, err)
					require.NoError(t, concludeInv.Attach(blk))
				}

				// The receipt is now retrievable by the original task invocation CID.
				buildAndWrite(t, store, []invocation.Invocation{concludeInv}, nil)
				got, err := store.GetReceipt(t.Context(), taskInv.Link().(cidlink.Link).Cid)
				require.NoError(t, err)
				require.Equal(t, rcpt.Root().Link().String(), got.Root().Link().String())
			})

			t.Run("writes invocation and receipt in the same message", func(t *testing.T) {
				inv := makeInvocation(t)
				rcpt := makeReceipt(t, inv)
				buildAndWrite(t, store, []invocation.Invocation{inv}, []receipt.AnyReceipt{rcpt})

				task := inv.Link().(cidlink.Link).Cid

				gotInv, err := store.GetInvocation(t.Context(), task)
				require.NoError(t, err)
				require.Equal(t, inv.Link().String(), gotInv.Link().String())

				gotRcpt, err := store.GetReceipt(t.Context(), task)
				require.NoError(t, err)
				require.Equal(t, rcpt.Root().Link().String(), gotRcpt.Root().Link().String())
			})
		})
	}
}
