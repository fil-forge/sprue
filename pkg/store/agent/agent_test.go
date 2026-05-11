package agent_test

import (
	"context"
	"runtime"
	"testing"

	ucancap "github.com/fil-forge/libforge/capabilities/ucan"
	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store/agent"
	agentaws "github.com/fil-forge/sprue/pkg/store/agent/aws"
	agentmemory "github.com/fil-forge/sprue/pkg/store/agent/memory"
	agentpostgres "github.com/fil-forge/sprue/pkg/store/agent/postgres"
	"github.com/fil-forge/ucantone/ipld"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/result"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/fil-forge/ucantone/ucan/receipt"
	"github.com/google/uuid"
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
		return agentmemory.New()
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
	store := agentaws.New(dynamo, "agent-index-"+id, s3, "agent-message-"+id)

	err := store.Initialize(t.Context())
	require.NoError(t, err)

	t.Cleanup(func() {
		err := store.Shutdown(context.Background())
		require.NoError(t, err)
	})
	return store
}

func makeInvocation(t *testing.T) ucan.Invocation {
	t.Helper()
	inv, err := invocation.Invoke(
		testutil.Alice,
		testutil.Alice,
		"/test/invoke",
		datamodel.Map{},
		invocation.WithAudience(testutil.Bob),
	)
	require.NoError(t, err)
	return inv
}

func makeReceipt(t *testing.T, inv ucan.Invocation) ucan.Receipt {
	t.Helper()
	rcpt, err := receipt.Issue(
		testutil.Alice,
		inv.Task().Link(),
		result.OK[ipld.Any, ipld.Any](datamodel.Map{}),
	)
	require.NoError(t, err)
	return rcpt
}

func buildAndWrite(t *testing.T, store agent.Store, invocations []ucan.Invocation, receipts []ucan.Receipt) {
	t.Helper()
	msg := container.New(
		container.WithInvocations(invocations...),
		container.WithReceipts(receipts...),
	)
	index := agent.Index(msg)
	err := store.Write(t.Context(), msg, index)
	require.NoError(t, err)
}

func TestAgentStore(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(string(k), func(t *testing.T) {
			store := makeStore(t, k)

			t.Run("gets an invocation", func(t *testing.T) {
				inv := makeInvocation(t)
				buildAndWrite(t, store, []ucan.Invocation{inv}, nil)

				got, err := store.GetInvocation(t.Context(), inv.Task().Link())
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
				buildAndWrite(t, store, nil, []ucan.Receipt{rcpt})

				got, err := store.GetReceipt(t.Context(), inv.Task().Link())
				require.NoError(t, err)
				require.Equal(t, rcpt.Link().String(), got.Link().String())
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

				concludeInv, err := ucancap.Conclude.Invoke(
					testutil.Alice,
					testutil.Alice,
					&ucancap.ConcludeArguments{
						Receipt: rcpt.Link(),
					},
					invocation.WithAudience(testutil.Bob),
				)
				require.NoError(t, err)

				// The receipt is now retrievable by the original task invocation CID.
				buildAndWrite(t, store, []ucan.Invocation{concludeInv}, []ucan.Receipt{rcpt})
				got, err := store.GetReceipt(t.Context(), taskInv.Task().Link())
				require.NoError(t, err)
				require.Equal(t, rcpt.Link().String(), got.Link().String())
			})

			t.Run("writes invocation and receipt in the same message", func(t *testing.T) {
				inv := makeInvocation(t)
				rcpt := makeReceipt(t, inv)
				buildAndWrite(t, store, []ucan.Invocation{inv}, []ucan.Receipt{rcpt})

				task := inv.Task().Link()

				gotInv, err := store.GetInvocation(t.Context(), task)
				require.NoError(t, err)
				require.Equal(t, inv.Link().String(), gotInv.Link().String())

				gotRcpt, err := store.GetReceipt(t.Context(), task)
				require.NoError(t, err)
				require.Equal(t, rcpt.Link().String(), gotRcpt.Link().String())
			})
		})
	}
}
