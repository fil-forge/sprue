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
