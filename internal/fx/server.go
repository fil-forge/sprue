package fx

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/internal/config"
	"github.com/fil-forge/sprue/pkg/build"
	"github.com/fil-forge/sprue/pkg/service"
)

// ServerModule provides the HTTP server with lifecycle management.
var ServerModule = fx.Module("server",
	fx.Provide(NewEchoServer),
	fx.Invoke(RegisterServerLifecycle),
)

// NewEchoServer creates and configures the Echo HTTP server.
func NewEchoServer(
	id identity.Identity,
	svc *service.Service,
	logger *zap.Logger,
) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	// Middleware
	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	e.Use(requestLogger(logger))

	// Routes
	e.GET("/", serverInfoHandler(id))
	e.GET("/health", healthHandler)
	e.GET("/.well-known/did.json", didDocumentHandler(id))
	e.POST("/", svc.HandleUCANRequest)
	e.GET("/validate-email", svc.HandleValidateEmailRequest)
	e.POST("/validate-email", svc.HandleValidateEmailRequest)
	e.GET("/receipt/:cid", svc.HandleReceiptRequest)

	return e
}

// requestLogger returns Echo's request logger middleware configured to route
// each request through the shared zap logger, so request and application logs
// share the same output. Mirrors Piri's request logger setup.
func requestLogger(logger *zap.Logger) echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogLatency:       true,
		LogRemoteIP:      true,
		LogHost:          true,
		LogMethod:        true,
		LogURI:           true,
		LogRequestID:     true,
		LogUserAgent:     true,
		LogStatus:        true,
		LogError:         true,
		LogContentLength: true,
		LogResponseSize:  true,
		LogHeaders:       []string{"X-Agent-Message"},
		HandleError:      true, // forwards error to the global error handler, so it can decide appropriate status code
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			fields := []zap.Field{
				zap.Int("status", v.Status),
				zap.String("method", v.Method),
				zap.String("uri", v.URI),
				zap.String("host", v.Host),
				zap.String("remote_ip", v.RemoteIP),
				zap.Duration("latency", v.Latency),
				zap.String("user_agent", v.UserAgent),
				zap.String("content_length", v.ContentLength),
				zap.Int64("response_size", v.ResponseSize),
				zap.String("request_id", v.RequestID),
				zap.Reflect("headers", v.Headers),
			}
			if v.Error != nil {
				fields = append(fields, zap.Error(v.Error))
			}
			switch {
			case v.Status >= http.StatusInternalServerError:
				logger.Error("server error", fields...)
			case v.Status >= http.StatusBadRequest:
				logger.Warn("client error", fields...)
			default:
				logger.Info("request completed", fields...)
			}
			return nil
		},
	})
}

// RegisterServerLifecycle hooks server start/stop to fx lifecycle.
func RegisterServerLifecycle(
	lc fx.Lifecycle,
	e *echo.Echo,
	cfg *config.Config,
	logger *zap.Logger,
	id identity.Identity,
) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
			logger.Info("starting sprue service",
				zap.String("address", addr),
				zap.String("did", id.DID().String()),
			)

			go func() {
				if err := e.Start(addr); err != nil && err != http.ErrServerClosed {
					logger.Fatal("server error", zap.Error(err))
				}
			}()

			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info("shutting down server")
			shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			return e.Shutdown(shutdownCtx)
		},
	})
}

// serverInfo identifies the service and its build, served on GET /.
type serverInfo struct {
	ID    string    `json:"id"`
	Build buildInfo `json:"build"`
}

// buildInfo is the version and source repository of the running build.
type buildInfo struct {
	Version string `json:"version"`
	Repo    string `json:"repo"`
}

// serverInfoHandler serves the service DID and build info: JSON when requested
// via the Accept header, otherwise a plain-text banner. Mirrors Hilt's handler.
func serverInfoHandler(id identity.Identity) echo.HandlerFunc {
	info := serverInfo{
		ID: id.DID().String(),
		Build: buildInfo{
			Version: build.Version,
			Repo:    "https://github.com/fil-forge/sprue",
		},
	}
	return func(c echo.Context) error {
		// Media type tokens are case-insensitive.
		if strings.Contains(strings.ToLower(c.Request().Header.Get("Accept")), "application/json") {
			return c.JSON(http.StatusOK, info)
		}
		banner := fmt.Sprintf("⚒️ sprue %s\n- %s\n- %s", info.Build.Version, info.Build.Repo, info.ID)
		return c.String(http.StatusOK, banner)
	}
}

// healthHandler returns health status.
func healthHandler(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{
		"status": "healthy",
	})
}

// didDocumentHandler returns the DID document for did:web resolution.
func didDocumentHandler(id identity.Identity) echo.HandlerFunc {
	return func(c echo.Context) error {
		doc, err := id.DIDDocument()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to get DID document",
			})
		}
		return c.JSON(http.StatusOK, doc)
	}
}
