package fx

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/fil-forge/libforge/identity"
	"github.com/fil-forge/sprue/internal/config"
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
	e.GET("/", infoHandler(id))
	e.GET("/health", healthHandler)
	e.GET("/.well-known/did.json", didDocumentHandler(id))
	e.POST("/", svc.HandleUCANRequest)
	e.GET("/validate-email", svc.HandleValidateEmailRequest)
	e.POST("/validate-email", svc.HandleValidateEmailRequest)
	e.GET("/receipt/:cid", svc.HandleReceiptRequest)

	return e
}

// requestLogger returns Echo's request logger middleware configured to emit
// each request through the zap logger, keeping a single uniform JSON log stream.
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
		HandleError:      true, // forwards error to the global error handler, so it can decide appropriate status code
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			fields := []zap.Field{
				zap.String("method", v.Method),
				zap.String("uri", v.URI),
				zap.Int("status", v.Status),
				zap.Duration("latency", v.Latency),
				zap.String("host", v.Host),
				zap.String("bytes_in", v.ContentLength),
				zap.Int64("bytes_out", v.ResponseSize),
				zap.String("user_agent", v.UserAgent),
				zap.String("remote_ip", v.RemoteIP),
				zap.String("request_id", v.RequestID),
			}
			if v.Error == nil {
				logger.Info("REQUEST", fields...)
			} else {
				logger.Error("REQUEST_ERROR", append(fields, zap.Error(v.Error))...)
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

// infoHandler returns service information.
func infoHandler(id identity.Identity) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"service": "sprue",
			"did":     id.DID(),
			"version": "0.1.0",
		})
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
