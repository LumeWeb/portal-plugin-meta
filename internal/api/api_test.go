package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	pluginCore "go.lumeweb.com/portal-plugin-meta/core"
	metaService "go.lumeweb.com/portal-plugin-meta/internal/service/meta"
	"go.lumeweb.com/portal/core"
	"go.lumeweb.com/portal/db/models"
	coreTesting "go.lumeweb.com/portal/core/testing"
	"go.lumeweb.com/portal/core/testing/mocks"
)

const testCID = "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"

// allowExportProtocol wraps a testing MockProtocol and embeds a
// MockProtocolExportAccessController so tests can set CanExportCID
// expectations. By default it allows all exports.
type allowExportProtocol struct {
	*coreTesting.MockProtocol
	acl *mocks.MockProtocolExportAccessController
}

func (a *allowExportProtocol) CanExportCID(ctx context.Context, cidStr string) (bool, error) {
	return a.acl.CanExportCID(ctx, cidStr)
}

func withAllowExportProtocol(name string) coreTesting.TestContextBuilderOption {
	return coreTesting.WithCustomMockProtocol(name, func(ctx coreTesting.TestContext) core.Protocol {
		mockProto := coreTesting.NewMockProtocol(ctx.T(), name)
		acl := mocks.NewMockProtocolExportAccessController(ctx.T())
		acl.EXPECT().CanExportCID(mock.Anything, mock.Anything).Return(true, nil).Maybe()
		return &allowExportProtocol{MockProtocol: mockProto, acl: acl}
	})
}

var baseTestOptions = coreTesting.CombineOptions(
	coreTesting.NewMockPluginBuilder("meta").
		WithService("meta", metaService.NewMetaService).
		BuilderOption(),
	coreTesting.WithMockUploadService(),
	coreTesting.WithMockPinService(),
	coreTesting.WithMockRenterService(),
	withAllowExportProtocol("sia"),
)

// TestAPI_RegistersAllRoutes verifies that all expected routes are registered.
func TestAPI_RegistersAllRoutes(t *testing.T) {
	opts := coreTesting.CombineOptions(
		baseTestOptions,
		coreTesting.WithAPI("meta", NewAPI),
		coreTesting.WithAPIID("meta"),
		coreTesting.WithConfig("plugin.meta.api.subdomain", "meta"),
	)

	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)
		pinSvc := coreTesting.GetMockPinService(ctx)
		renterSvc := coreTesting.GetMockRenterService(ctx)
		uploadSvc.EXPECT().GetUpload(mock.Anything, mock.Anything).Return(&models.Upload{Protocol: "sia", Size: 1024}, nil).Maybe()
		uploadSvc.EXPECT().GetUploadStats(mock.Anything).Return([]core.ProtocolUploadStat{}, nil).Maybe()
		pinSvc.EXPECT().UploadPinnedGlobal(mock.Anything, mock.Anything).Return(false, nil).Maybe()
		pinSvc.EXPECT().GetAllPinsByHash(mock.Anything, mock.Anything).Return([]*models.Pin{}, nil).Maybe()
		pinSvc.EXPECT().GetPinStats(mock.Anything).Return([]core.ProtocolPinStat{}, nil).Maybe()
		renterSvc.EXPECT().UploadExists(mock.Anything, mock.Anything, mock.Anything).Return(true, &models.RenterObject{Status: models.RenterObjectStatusUploaded, SealedData: []byte("{}")}, nil).Maybe()
		renterSvc.EXPECT().SharedObject(mock.Anything, mock.Anything, mock.Anything).Return(&core.SharedObject{}, &models.RenterObject{Status: models.RenterObjectStatusUploaded}, nil).Maybe()

		expectedRoutes := []struct {
			method string
			path   string
		}{
			{http.MethodGet, "/api/stats/cid/" + testCID},
			{http.MethodGet, "/api/export/cid/" + testCID + "/sia-object"},
			{http.MethodGet, "/api/export/cid/" + testCID + "/dag"},
			{http.MethodGet, "/api/stats/aggregate"},
			{http.MethodGet, "/api/stats/protocols"},
		}

		for _, r := range expectedRoutes {
			req := ctx.NewAPIRequest(r.method, r.path, nil)
			rec := httptest.NewRecorder()
			ctx.Router().ServeHTTP(rec, req)
			// A 404 from the handler (e.g. CID not found) is fine — we're
			// only checking that the route is registered, not that it
			// succeeds. Echo returns 404 for unmatched routes too, so
			// we verify the response is not the Echo default 404 page.
			assert.NotEqual(tb, http.StatusNotFound, rec.Code,
				"route %s %s should be registered (got 404)", r.method, r.path)
		}
	}, opts)
}

// TestAPI_CIDStats_ReturnsStats verifies the CID stats endpoint returns
// correct JSON response with expected fields.
func TestAPI_CIDStats_ReturnsStats(t *testing.T) {
	opts := coreTesting.CombineOptions(
		baseTestOptions,
		coreTesting.WithAPI("meta", NewAPI),
		coreTesting.WithAPIID("meta"),
		coreTesting.WithConfig("plugin.meta.api.subdomain", "meta"),
	)

	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)
		pinSvc := coreTesting.GetMockPinService(ctx)

		uploadSvc.EXPECT().GetUpload(mock.Anything, mock.Anything).Return(&models.Upload{Protocol: "sia", Size: 2048}, nil).Once()
		pinSvc.EXPECT().UploadPinnedGlobal(mock.Anything, mock.Anything).Return(true, nil).Once()
		pinSvc.EXPECT().GetAllPinsByHash(mock.Anything, mock.Anything).Return([]*models.Pin{}, nil).Once()

		req := ctx.NewAPIRequest(http.MethodGet, "/api/stats/cid/"+testCID, nil)
		rec := httptest.NewRecorder()
		ctx.Router().ServeHTTP(rec, req)

		assert.Equal(tb, http.StatusOK, rec.Code)

		var resp pluginCore.CIDStatsResponse
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(tb, err)
		assert.Equal(tb, testCID, resp.CID)
		assert.True(tb, resp.Pinned)
		assert.Equal(tb, uint64(2048), resp.SizeBytes)
	}, opts)
}

// TestAPI_CIDStats_NotFound verifies CID not found returns 404.
func TestAPI_CIDStats_NotFound(t *testing.T) {
	opts := coreTesting.CombineOptions(
		baseTestOptions,
		coreTesting.WithAPI("meta", NewAPI),
		coreTesting.WithAPIID("meta"),
		coreTesting.WithConfig("plugin.meta.api.subdomain", "meta"),
	)

	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)

		uploadSvc.EXPECT().GetUpload(mock.Anything, mock.Anything).Return(nil, core.ErrUploadNotFound).Once()

		req := ctx.NewAPIRequest(http.MethodGet, "/api/stats/cid/"+testCID, nil)
		rec := httptest.NewRecorder()
		ctx.Router().ServeHTTP(rec, req)

		assert.Equal(tb, http.StatusNotFound, rec.Code)
	}, opts)
}

// TestAPI_AggregateStats_ReturnsStats verifies the aggregate stats endpoint.
func TestAPI_AggregateStats_ReturnsStats(t *testing.T) {
	opts := coreTesting.CombineOptions(
		baseTestOptions,
		coreTesting.WithAPI("meta", NewAPI),
		coreTesting.WithAPIID("meta"),
		coreTesting.WithConfig("plugin.meta.api.subdomain", "meta"),
	)

	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)
		pinSvc := coreTesting.GetMockPinService(ctx)

		uploadSvc.EXPECT().GetUploadStats(mock.Anything).Return([]core.ProtocolUploadStat{
			{Protocol: "sia", TotalUploads: 10, TotalStorageBytes: 1024},
		}, nil).Once()
		pinSvc.EXPECT().GetPinStats(mock.Anything).Return([]core.ProtocolPinStat{
			{Protocol: "sia", TotalPins: 5},
		}, nil).Once()

		req := ctx.NewAPIRequest(http.MethodGet, "/api/stats/aggregate", nil)
		rec := httptest.NewRecorder()
		ctx.Router().ServeHTTP(rec, req)

		assert.Equal(tb, http.StatusOK, rec.Code)

		var resp pluginCore.AggregateStatsResponse
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(tb, err)
		assert.Equal(tb, uint64(10), resp.TotalCIDs)
		assert.Equal(tb, uint64(5), resp.TotalPinners)
		assert.Equal(tb, uint64(1024), resp.TotalStorageBytes)
	}, opts)
}

// TestAPI_ProtocolStats_ReturnsStats verifies the protocol stats endpoint.
func TestAPI_ProtocolStats_ReturnsStats(t *testing.T) {
	opts := coreTesting.CombineOptions(
		baseTestOptions,
		coreTesting.WithAPI("meta", NewAPI),
		coreTesting.WithAPIID("meta"),
		coreTesting.WithConfig("plugin.meta.api.subdomain", "meta"),
	)

	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)
		pinSvc := coreTesting.GetMockPinService(ctx)

		uploadSvc.EXPECT().GetUploadStats(mock.Anything).Return([]core.ProtocolUploadStat{
			{Protocol: "sia", TotalUploads: 10, TotalStorageBytes: 1024},
			{Protocol: "s3", TotalUploads: 5, TotalStorageBytes: 512},
		}, nil).Once()
		pinSvc.EXPECT().GetPinStats(mock.Anything).Return([]core.ProtocolPinStat{
			{Protocol: "sia", TotalPins: 20},
			{Protocol: "s3", TotalPins: 10},
		}, nil).Once()

		req := ctx.NewAPIRequest(http.MethodGet, "/api/stats/protocols", nil)
		rec := httptest.NewRecorder()
		ctx.Router().ServeHTTP(rec, req)

		assert.Equal(tb, http.StatusOK, rec.Code)

		var resp pluginCore.ProtocolStatsResponse
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(tb, err)
		require.Len(tb, resp.Protocols, 2)
		assert.Equal(tb, "s3", resp.Protocols[0].Protocol)
		assert.Equal(tb, uint64(5), resp.Protocols[0].TotalUploads)
		assert.Equal(tb, uint64(10), resp.Protocols[0].TotalPins)
		assert.Equal(tb, "sia", resp.Protocols[1].Protocol)
		assert.Equal(tb, uint64(10), resp.Protocols[1].TotalUploads)
		assert.Equal(tb, uint64(20), resp.Protocols[1].TotalPins)
	}, opts)
}
