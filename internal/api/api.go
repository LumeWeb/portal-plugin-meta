package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"go.lumeweb.com/httputil"
	"go.lumeweb.com/portal-plugin-meta/internal"
	pluginCore "go.lumeweb.com/portal-plugin-meta/core"
	pluginConfig "go.lumeweb.com/portal-plugin-meta/internal/config"
	metaService "go.lumeweb.com/portal-plugin-meta/internal/service/meta"
	router "go.lumeweb.com/portal-router"
	"go.lumeweb.com/portal/config"
	"go.lumeweb.com/portal/core"
)

var _ core.API = (*API)(nil)

type API struct {
	*core.BaseComponent
	metaSvc pluginCore.MetaService
}

func (a *API) ID() string        { return a.Name() }
func (a *API) Name() string      { return internal.PluginName }
func (a *API) Subdomain() string { return "meta" }
func (a *API) AuthTokenName() string { return "meta" }

func (a *API) OpenAPIInfo() router.APIInfoDefinition {
	return router.APIInfo().
		Title("Meta API").
		Description("Public, anonymized statistics about pinned content.")
}

func (a *API) GetConfig() config.APIConfig {
	return &pluginConfig.APIConfig{Subdomain: "meta"}
}

func NewAPI() (core.API, []core.ContextBuilderOption, error) {
	api := &API{}
	opts := core.ContextOptions(
		core.ContextWithStartupFunc(func(ctx core.Context) error {
			api.metaSvc = core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
			if api.metaSvc == nil {
				return fmt.Errorf("meta service not available")
			}
			return nil
		}),
	)
	return api, opts, nil
}

func (a *API) Configure(gRouter router.Router, accessSvc core.AccessService) error {
	statsRouter, err := gRouter.Group("/api/stats")
	if err != nil {
		return fmt.Errorf("failed to create stats router group: %w", err)
	}
	exportRouter, err := gRouter.Group("/api/export")
	if err != nil {
		return fmt.Errorf("failed to create export router group: %w", err)
	}

	statsRoutes := []router.Route{
		router.NewRoute(http.MethodGet, "/cid/:cid", a.handleCIDStats,
			router.WithCors(),
			router.WithSwaggerOptions(
				router.WithSummary("CID stats"),
				router.WithDescription("Returns anonymized stats for a CID: pinned status, pinner count, size, storage-days, and quota health (when quota plugin is available)."),
				router.WithTags("stats"),
				router.WithPathParam("cid", "Content identifier (CID)", ""),
				router.WithSuccessResponse(http.StatusOK, "CID stats", router.WithJSONContent(pluginCore.CIDStatsResponse{})),
				router.WithErrorResponses(router.DefaultPublicErrorResponses()),
			),
		),
		router.NewRoute(http.MethodGet, "/aggregate", a.handleAggregateStats,
			router.WithCors(),
			router.WithSwaggerOptions(
				router.WithSummary("Aggregate stats"),
				router.WithDescription("Total CIDs, pinners, storage bytes across all protocols."),
				router.WithTags("stats"),
				router.WithSuccessResponse(http.StatusOK, "Aggregate stats", router.WithJSONContent(pluginCore.AggregateStatsResponse{})),
				router.WithErrorResponses(router.DefaultPublicErrorResponses()),
			),
		),
		router.NewRoute(http.MethodGet, "/protocols", a.handleProtocolStats,
			router.WithCors(),
			router.WithSwaggerOptions(
				router.WithSummary("Per-protocol stats"),
				router.WithDescription("Total uploads, storage bytes, and pin counts broken down by protocol."),
				router.WithTags("stats"),
				router.WithSuccessResponse(http.StatusOK, "Per-protocol stats", router.WithJSONContent(pluginCore.ProtocolStatsResponse{})),
				router.WithErrorResponses(router.DefaultPublicErrorResponses()),
			),
		),
	}

	exportRoutes := []router.Route{
		router.NewRoute(http.MethodGet, "/cid/:cid/sia-object", a.handleExport,
			router.WithCors(),
			router.WithSwaggerOptions(
				router.WithSummary("Export Sia object for a CID"),
				router.WithDescription("Returns the indexed SharedObject — slab layout, encryption metadata, sector refs — so any Sia account holder can retrieve and decrypt the block directly from the Sia network."),
				router.WithTags("export"),
				router.WithPathParam("cid", "Content identifier (CID)", ""),
				router.WithSuccessResponse(http.StatusOK, "Sia object export", router.WithJSONContent(pluginCore.CIDExportResponse{})),
				router.WithErrorResponses(router.DefaultPublicErrorResponses()),
			),
		),
		router.NewRoute(http.MethodGet, "/cid/:cid/dag", a.handleDAGExport,
			router.WithCors(),
			router.WithSwaggerOptions(
				router.WithSummary("Export full DAG structure for a CID"),
				router.WithDescription("Returns the full block DAG — all blocks, their parent→child relationships, sizes, and per-block Sia object references."),
				router.WithTags("export"),
				router.WithPathParam("cid", "Root content identifier (CID)", ""),
				router.WithSuccessResponse(http.StatusOK, "DAG export", router.WithJSONContent(pluginCore.DAGExportResponse{})),
				router.WithErrorResponses(router.DefaultPublicErrorResponses()),
			),
		),
	}

	subdomain := core.GetAPI(a.Name()).Subdomain()
	if err := router.RegisterRoutes(statsRouter, accessSvc, subdomain, statsRoutes); err != nil {
		return err
	}
	return router.RegisterRoutes(exportRouter, accessSvc, subdomain, exportRoutes)
}

func (a *API) handleCIDStats(c echo.Context) error {
	ctx := httputil.Context(c)
	cidStr := c.Param("cid")

	resp, err := a.metaSvc.CIDStats(c.Request().Context(), cidStr)
	if err != nil {
		if errors.Is(err, metaService.ErrCIDNotFound) {
			apiErr := core.NewError(Namespace, ErrKeyCIDNotFound, err)
			return ctx.Error(apiErr, http.StatusNotFound)
		}
		apiErr := core.NewError(Namespace, ErrKeyFetchFailed, err)
		return ctx.Error(apiErr, http.StatusInternalServerError)
	}

	return ctx.Encode(resp)
}

func (a *API) handleExport(c echo.Context) error {
	ctx := httputil.Context(c)
	cidStr := c.Param("cid")

	resp, err := a.metaSvc.ExportSiaObject(c.Request().Context(), cidStr)
	if err != nil {
		if errors.Is(err, metaService.ErrCIDNotFound) {
			apiErr := core.NewError(Namespace, ErrKeyCIDNotFound, err)
			return ctx.Error(apiErr, http.StatusNotFound)
		}
		if errors.Is(err, metaService.ErrObjectNotReady) {
			apiErr := core.NewError(Namespace, ErrKeyObjectNotReady, err)
			return ctx.Error(apiErr, http.StatusConflict)
		}
		if errors.Is(err, core.ErrExportDenied) {
			apiErr := core.NewError(Namespace, ErrKeyExportDenied, err)
			return ctx.Error(apiErr, http.StatusForbidden)
		}
		apiErr := core.NewError(Namespace, ErrKeyExportFailed, err)
		return ctx.Error(apiErr, http.StatusInternalServerError)
	}

	return ctx.Encode(resp)
}

func (a *API) handleDAGExport(c echo.Context) error {
	ctx := httputil.Context(c)
	cidStr := c.Param("cid")

	resp, err := a.metaSvc.ExportDAG(c.Request().Context(), cidStr)
	if err != nil {
		if errors.Is(err, metaService.ErrCIDNotFound) {
			apiErr := core.NewError(Namespace, ErrKeyCIDNotFound, err)
			return ctx.Error(apiErr, http.StatusNotFound)
		}
		if errors.Is(err, metaService.ErrDAGNotSupported) {
			apiErr := core.NewError(Namespace, ErrKeyDAGNotSupported, err)
			return ctx.Error(apiErr, http.StatusNotImplemented)
		}
		if errors.Is(err, metaService.ErrObjectNotReady) {
			apiErr := core.NewError(Namespace, ErrKeyObjectNotReady, err)
			return ctx.Error(apiErr, http.StatusConflict)
		}
		if errors.Is(err, core.ErrExportDenied) {
			apiErr := core.NewError(Namespace, ErrKeyExportDenied, err)
			return ctx.Error(apiErr, http.StatusForbidden)
		}
		apiErr := core.NewError(Namespace, ErrKeyExportFailed, err)
		return ctx.Error(apiErr, http.StatusInternalServerError)
	}

	return ctx.Encode(resp)
}

func (a *API) handleAggregateStats(c echo.Context) error {
	ctx := httputil.Context(c)

	resp, err := a.metaSvc.AggregateStats(c.Request().Context())
	if err != nil {
		apiErr := core.NewError(Namespace, ErrKeyFetchFailed, err)
		return ctx.Error(apiErr, http.StatusInternalServerError)
	}

	return ctx.Encode(resp)
}

func (a *API) handleProtocolStats(c echo.Context) error {
	ctx := httputil.Context(c)

	resp, err := a.metaSvc.ProtocolStats(c.Request().Context())
	if err != nil {
		apiErr := core.NewError(Namespace, ErrKeyFetchFailed, err)
		return ctx.Error(apiErr, http.StatusInternalServerError)
	}

	return ctx.Encode(resp)
}
