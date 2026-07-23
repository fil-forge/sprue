package handlers

import (
	"github.com/fil-forge/sprue/pkg/service/handlers"
	"github.com/fil-forge/ucantone/ucan"
	"go.uber.org/fx"
)

var Module = fx.Module("service-handlers",
	fx.Provide(
		fx.Annotate(
			handlers.NewAccessClaimHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewAccessConfirmHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewAccessDelegateHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewAccessRequestHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewCustomerAddHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewAdminProviderDeregisterHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewAdminProviderListHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewAdminProviderRegisterHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewAdminProviderWeightSetHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewBlobAddHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewBlobListHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewBlobRemoveHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewBlobAbortHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		// fx.Annotate(
		// 	handlers.NewBlobReplicateHandler,
		// 	fx.ResultTags(`group:"ucan_handlers"`),
		// ),
		fx.Annotate(
			handlers.NewIndexAddHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewProviderAddHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewSpaceInfoHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewUCANConcludeHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewUploadAddHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewUploadRemoveHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewUploadListHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewUploadShardListHandler,
			fx.ResultTags(`group:"ucan_handlers"`),
		),
		fx.Annotate(
			handlers.NewHTTPPutConcludeHandler,
			fx.ResultTags(`group:"ucan_conclude_handlers"`),
		),
		// fx.Annotate(
		// 	handlers.NewBlobReplicaTransferConcludeHandler,
		// 	fx.ResultTags(`group:"ucan_conclude_handlers"`),
		// ),
		NewConcludeHandlers,
	),
)

type ConcludeHandlersParams struct {
	fx.In
	Handlers []handlers.ConclusionHandler `group:"ucan_conclude_handlers"`
}

func NewConcludeHandlers(params ConcludeHandlersParams) map[ucan.Command]handlers.ConclusionHandlerFunc {
	handlers := make(map[ucan.Command]handlers.ConclusionHandlerFunc, len(params.Handlers))
	for _, h := range params.Handlers {
		handlers[h.Command] = h.Handler
	}
	return handlers
}
