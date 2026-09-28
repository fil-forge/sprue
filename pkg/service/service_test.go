package service

import (
	"io"
	"net/http"
	"testing"

	"github.com/fil-forge/sprue/internal/testutil"
	agentmemory "github.com/fil-forge/sprue/pkg/store/agent/memory"
	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/ipld/codec/dagcbor"
	"github.com/fil-forge/ucantone/ipld/datamodel"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan/command"
	"github.com/fil-forge/ucantone/ucan/container"
	"github.com/fil-forge/ucantone/ucan/invocation"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// The request and its receipt are both in the agent store by the time the
// response is returned, even though the request is stored while the handler
// runs rather than before it.
func TestUCANServerStoresRequestAndResponse(t *testing.T) {
	echo := command.MustParse("/test/echo")
	agentStore := agentmemory.New()
	route := server.NewRoute(echo, func(req execution.Request, res execution.Response) error {
		return res.SetSuccess(datamodel.Map{})
	})
	srv, err := createUCANServer(testutil.Service, agentStore, []server.Route{route}, zaptest.NewLogger(t))
	require.NoError(t, err)

	inv, err := invocation.Invoke(testutil.Alice, testutil.Alice.DID(), echo, datamodel.Map{}, invocation.WithAudience(testutil.Service.DID()))
	require.NoError(t, err)
	ct := container.New(container.WithInvocations(inv))
	r, w := io.Pipe()
	go func() { w.CloseWithError(ct.MarshalCBOR(w)) }()
	req := (&http.Request{Header: http.Header{}, Body: r}).WithContext(t.Context())
	req.Header.Set("Content-Type", dagcbor.ContentType)

	resp, err := srv.RoundTrip(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	task := inv.Task().Link()
	got, err := agentStore.GetInvocation(t.Context(), task)
	require.NoError(t, err)
	require.Equal(t, inv.Link(), got.Link())
	rcpt, err := agentStore.GetReceipt(t.Context(), task)
	require.NoError(t, err)
	_, x := rcpt.Out().Unpack()
	require.Nil(t, x)
}
