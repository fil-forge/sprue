package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	"github.com/fil-forge/sprue/pkg/store/agent"
	agentmemory "github.com/fil-forge/sprue/pkg/store/agent/memory"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ipld/codec/dagcbor"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/fil-forge/ucantone/ucan/command"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

var echoCommand = command.MustParse("/test/echo")

// echoServer is the service's UCAN server over the given agent store, with
// one handler that succeeds.
func echoServer(t *testing.T, agentStore agent.Store) *server.HTTPServer {
	t.Helper()
	route := server.NewRoute(echoCommand, func(req execution.Request, res execution.Response) error {
		return res.SetSuccess(datamodel.Map{})
	})
	srv, err := createUCANServer(testutil.Service, agentStore, []server.Route{route}, zaptest.NewLogger(t))
	require.NoError(t, err)
	return srv
}

// roundTrip sends one echo invocation through the server and returns it,
// having checked the response is a success.
func roundTrip(t *testing.T, srv *server.HTTPServer) ucan.Invocation {
	t.Helper()
	inv, err := invocation.Invoke(testutil.Alice, testutil.Alice.DID(), echoCommand, datamodel.Map{}, invocation.WithAudience(testutil.Service.DID()))
	require.NoError(t, err)
	ct := container.New(container.WithInvocations(inv))
	r, w := io.Pipe()
	go func() { w.CloseWithError(ct.MarshalCBOR(w)) }()
	req := (&http.Request{Header: http.Header{}, Body: r}).WithContext(t.Context())
	req.Header.Set("Content-Type", dagcbor.ContentType)

	resp, err := srv.RoundTrip(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return inv
}

// The request and its receipt are both in the agent store by the time the
// response is returned, even though the request is stored while the handler
// runs rather than before it.
func TestUCANServerStoresRequestAndResponse(t *testing.T) {
	agentStore := agentmemory.New()
	inv := roundTrip(t, echoServer(t, agentStore))

	task := inv.Task().Link()
	got, err := agentStore.GetInvocation(t.Context(), task)
	require.NoError(t, err)
	require.Equal(t, inv.Link(), got.Link())
	rcpt, err := agentStore.GetReceipt(t.Context(), task)
	require.NoError(t, err)
	_, x := rcpt.Out().Unpack()
	require.Nil(t, x)
}

// A failed write of the incoming message does not fail the request: by the
// time it is known the handlers have run, and nothing reads the record on a
// request path. The receipt is still stored.
func TestUCANServerToleratesLostRequestRecord(t *testing.T) {
	agentStore := &requestWriteFails{Store: agentmemory.New()}
	inv := roundTrip(t, echoServer(t, agentStore))

	task := inv.Task().Link()
	_, err := agentStore.GetInvocation(t.Context(), task)
	require.ErrorIs(t, err, agent.ErrInvocationNotFound)
	_, err = agentStore.GetReceipt(t.Context(), task)
	require.NoError(t, err)
}

// requestWriteFails is an agent store whose writes of incoming messages,
// the ones carrying no receipts, fail.
type requestWriteFails struct {
	agent.Store
}

func (s *requestWriteFails) Write(ctx context.Context, message ucan.Container, index []agent.IndexEntry) error {
	if len(message.Receipts()) == 0 {
		return errors.New("store unavailable")
	}
	return s.Store.Write(ctx, message, index)
}
