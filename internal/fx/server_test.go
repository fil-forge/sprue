package fx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/pkg/build"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestServerInfoHandler(t *testing.T) {
	id, err := identity.New("", "")
	require.NoError(t, err)
	handler := serverInfoHandler(id)

	t.Run("returns JSON when requested via Accept, case-insensitively", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept", "Application/JSON")
		rec := httptest.NewRecorder()
		require.NoError(t, handler(echo.New().NewContext(req, rec)))

		require.Equal(t, http.StatusOK, rec.Code)
		var info struct {
			ID    string `json:"id"`
			Build struct {
				Version string `json:"version"`
				Repo    string `json:"repo"`
			} `json:"build"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
		require.Equal(t, id.DID().String(), info.ID)
		require.Equal(t, build.Version, info.Build.Version)
		require.Equal(t, "https://github.com/fil-forge/sprue", info.Build.Repo)
	})

	t.Run("returns a plain-text banner by default", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		require.NoError(t, handler(echo.New().NewContext(req, rec)))

		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Header().Get(echo.HeaderContentType), "text/plain")
		body := rec.Body.String()
		require.Contains(t, body, "sprue "+build.Version)
		require.Contains(t, body, "https://github.com/fil-forge/sprue")
		require.Contains(t, body, id.DID().String())
	})
}

func TestDIDDocumentHandler(t *testing.T) {
	id, err := identity.New("", "did:web:sprue.example")
	require.NoError(t, err)
	handler := didDocumentHandler(id)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/did.json", nil)
	rec := httptest.NewRecorder()
	require.NoError(t, handler(echo.New().NewContext(req, rec)))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get(echo.HeaderContentType), "application/json")
	require.Contains(t, rec.Body.String(), "did:web:sprue.example")
	require.Equal(t, "max-age=300", rec.Header().Get("Cache-Control"))
	require.NotEmpty(t, rec.Header().Get("Last-Modified"))
	etag := rec.Header().Get("ETag")
	require.NotEmpty(t, etag)

	// A client that already holds the document gets a 304.
	req = httptest.NewRequest(http.MethodGet, "/.well-known/did.json", nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	require.NoError(t, handler(echo.New().NewContext(req, rec)))
	require.Equal(t, http.StatusNotModified, rec.Code)
	require.Empty(t, rec.Body.Bytes())
}
