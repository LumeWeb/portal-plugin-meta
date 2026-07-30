package meta

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	pluginCore "go.lumeweb.com/portal-plugin-meta/core"
	"go.lumeweb.com/portal/core"
	"go.lumeweb.com/portal/db/models"
	coreTesting "go.lumeweb.com/portal/core/testing"
	"gorm.io/gorm"
)

// testCID is a valid CIDv1 string that the core CIDStorageHashParser can parse.
const testCID = "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"

var baseTestOptions = coreTesting.CombineOptions(
	coreTesting.NewMockPluginBuilder("meta").
		WithService("meta", NewMetaService).
		BuilderOption(),
	coreTesting.WithMockUploadService(),
	coreTesting.WithMockPinService(),
	coreTesting.WithMockRenterService(),
	withAllowExportProtocol("sia"),
)

// --- CIDStats ---

func TestMetaService_CIDStats_NotPinned(t *testing.T) {
	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)
		pinSvc := coreTesting.GetMockPinService(ctx)

		uploadSvc.EXPECT().GetUpload(mock.Anything, mock.Anything).Return(&models.Upload{Protocol: "sia", Size: 1024}, nil).Once()
		pinSvc.EXPECT().UploadPinnedGlobal(mock.Anything, mock.Anything).Return(false, nil).Once()
		pinSvc.EXPECT().GetAllPinsByHash(mock.Anything, mock.Anything).Return([]*models.Pin{}, nil).Once()

		svc := core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
		require.NotNil(tb, svc)

		resp, err := svc.CIDStats(context.Background(), testCID)
		require.NoError(tb, err)
		assert.Equal(tb, testCID, resp.CID)
		assert.False(tb, resp.Pinned)
		assert.Equal(tb, uint64(0), resp.PinnerCount)
		assert.Equal(tb, uint64(1024), resp.SizeBytes)
		assert.Nil(tb, resp.FirstPinnedAt)
		assert.Nil(tb, resp.LastPinnedAt)
		assert.Nil(tb, resp.TotalQuotaBytes) // quota plugin not registered
	}, baseTestOptions)
}

func TestMetaService_CIDStats_PinnedWithPins(t *testing.T) {
	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)
		pinSvc := coreTesting.GetMockPinService(ctx)

		now := time.Now().Add(-48 * time.Hour)
		pins := []*models.Pin{
			{Model: gorm.Model{CreatedAt: now}},
			{Model: gorm.Model{CreatedAt: time.Now().Add(-24 * time.Hour)}},
		}

		uploadSvc.EXPECT().GetUpload(mock.Anything, mock.Anything).Return(&models.Upload{Protocol: "sia", Size: 1024}, nil).Once()
		pinSvc.EXPECT().UploadPinnedGlobal(mock.Anything, mock.Anything).Return(true, nil).Once()
		pinSvc.EXPECT().GetAllPinsByHash(mock.Anything, mock.Anything).Return(pins, nil).Once()

		svc := core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
		require.NotNil(tb, svc)

		resp, err := svc.CIDStats(context.Background(), testCID)
		require.NoError(tb, err)
		assert.True(tb, resp.Pinned)
		assert.Equal(tb, uint64(2), resp.PinnerCount)
		assert.Equal(tb, uint64(1024), resp.SizeBytes)
		assert.Greater(tb, resp.StorageDays, 0.0)
		assert.NotNil(tb, resp.FirstPinnedAt)
		assert.NotNil(tb, resp.LastPinnedAt)
	}, baseTestOptions)
}

func TestMetaService_CIDStats_UploadNotFound(t *testing.T) {
	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)

		uploadSvc.EXPECT().GetUpload(mock.Anything, mock.Anything).Return(nil, core.ErrUploadNotFound).Once()

		svc := core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
		require.NotNil(tb, svc)

		_, err := svc.CIDStats(context.Background(), testCID)
		assert.ErrorIs(tb, err, ErrCIDNotFound)
	}, baseTestOptions)
}

func TestMetaService_CIDStats_InvalidCID(t *testing.T) {
	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		svc := core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
		require.NotNil(tb, svc)

		// "not-a-cid" should fail parsing but the core base64 parser may accept it.
		// Use a clearly invalid string.
		_, err := svc.CIDStats(context.Background(), "")
		assert.Error(tb, err)
	}, baseTestOptions)
}

// --- AggregateStats ---

func TestMetaService_AggregateStats_Success(t *testing.T) {
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

		svc := core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
		require.NotNil(tb, svc)

		resp, err := svc.AggregateStats(context.Background())
		require.NoError(tb, err)
		assert.Equal(tb, uint64(15), resp.TotalCIDs)
		assert.Equal(tb, uint64(30), resp.TotalPinners)
		assert.Equal(tb, uint64(1536), resp.TotalStorageBytes)
	}, baseTestOptions)
}

func TestMetaService_AggregateStats_UploadError(t *testing.T) {
	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)

		uploadSvc.EXPECT().GetUploadStats(mock.Anything).Return(nil, errors.New("db error")).Once()

		svc := core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
		require.NotNil(tb, svc)

		_, err := svc.AggregateStats(context.Background())
		assert.Error(tb, err)
	}, baseTestOptions)
}

func TestMetaService_AggregateStats_Empty(t *testing.T) {
	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)
		pinSvc := coreTesting.GetMockPinService(ctx)

		uploadSvc.EXPECT().GetUploadStats(mock.Anything).Return([]core.ProtocolUploadStat{}, nil).Once()
		pinSvc.EXPECT().GetPinStats(mock.Anything).Return([]core.ProtocolPinStat{}, nil).Once()

		svc := core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
		require.NotNil(tb, svc)

		resp, err := svc.AggregateStats(context.Background())
		require.NoError(tb, err)
		assert.Equal(tb, uint64(0), resp.TotalCIDs)
		assert.Equal(tb, uint64(0), resp.TotalPinners)
		assert.Equal(tb, uint64(0), resp.TotalStorageBytes)
	}, baseTestOptions)
}

// --- ProtocolStats ---

func TestMetaService_ProtocolStats_Success(t *testing.T) {
	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)
		pinSvc := coreTesting.GetMockPinService(ctx)

		uploadSvc.EXPECT().GetUploadStats(mock.Anything).Return([]core.ProtocolUploadStat{
			{Protocol: "sia", TotalUploads: 10, TotalStorageBytes: 1024},
		}, nil).Once()
		pinSvc.EXPECT().GetPinStats(mock.Anything).Return([]core.ProtocolPinStat{
			{Protocol: "sia", TotalPins: 20},
		}, nil).Once()

		svc := core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
		require.NotNil(tb, svc)

		resp, err := svc.ProtocolStats(context.Background())
		require.NoError(tb, err)
		require.Len(tb, resp.Protocols, 1)
		assert.Equal(tb, "sia", resp.Protocols[0].Protocol)
		assert.Equal(tb, uint64(10), resp.Protocols[0].TotalUploads)
		assert.Equal(tb, uint64(1024), resp.Protocols[0].TotalStorageBytes)
		assert.Equal(tb, uint64(20), resp.Protocols[0].TotalPins)
	}, baseTestOptions)
}

// --- ExportSiaObject ---

func TestMetaService_ExportSiaObject_NotFound(t *testing.T) {
	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)

		uploadSvc.EXPECT().GetUpload(mock.Anything, mock.Anything).Return(nil, core.ErrUploadNotFound).Once()

		svc := core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
		require.NotNil(tb, svc)

		_, err := svc.ExportSiaObject(context.Background(), testCID)
		assert.ErrorIs(tb, err, ErrCIDNotFound)
	}, baseTestOptions)
}

func TestMetaService_ExportSiaObject_ObjectStaged(t *testing.T) {
	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)
		renterSvc := coreTesting.GetMockRenterService(ctx)

		uploadSvc.EXPECT().GetUpload(mock.Anything, mock.Anything).Return(&models.Upload{
			Protocol: "sia",
			Size:     1024,
		}, nil).Once()

		renterSvc.EXPECT().SharedObject(mock.Anything, mock.Anything, mock.Anything).Return(nil, &models.RenterObject{Status: models.RenterObjectStatusStaged, Size: 1024}, nil).Once()

		svc := core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
		require.NotNil(tb, svc)

		_, err := svc.ExportSiaObject(context.Background(), testCID)
		assert.ErrorIs(tb, err, ErrObjectNotReady)
	}, baseTestOptions)
}

func TestMetaService_ExportSiaObject_Success(t *testing.T) {
	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)
		renterSvc := coreTesting.GetMockRenterService(ctx)

		uploadSvc.EXPECT().GetUpload(mock.Anything, mock.Anything).Return(&models.Upload{
			Protocol: "sia",
			Size:     2048,
		}, nil).Once()

		renterSvc.EXPECT().SharedObject(mock.Anything, mock.Anything, mock.Anything).Return(&core.SharedObject{}, &models.RenterObject{Status: models.RenterObjectStatusUploaded, Size: 2048}, nil).Once()

		svc := core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
		require.NotNil(tb, svc)

		resp, err := svc.ExportSiaObject(context.Background(), testCID)
		require.NoError(tb, err)
		assert.Equal(tb, testCID, resp.CID)
		assert.Equal(tb, uint64(2048), resp.SizeBytes)
		assert.NotNil(tb, resp.SharedObject)
	}, baseTestOptions)
}

// --- ExportDAG ---

func TestMetaService_ExportDAG_CIDNotFound(t *testing.T) {
	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)

		uploadSvc.EXPECT().GetUpload(mock.Anything, mock.Anything).Return(nil, core.ErrUploadNotFound).Once()

		svc := core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
		require.NotNil(tb, svc)

		_, err := svc.ExportDAG(context.Background(), testCID)
		assert.ErrorIs(tb, err, ErrCIDNotFound)
	}, baseTestOptions)
}

// --- ID ---

func TestMetaService_ID(t *testing.T) {
	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		svc := core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
		require.NotNil(tb, svc)
		assert.Equal(tb, "meta", svc.ID())
	}, baseTestOptions)
}

// gormModel returns a gorm.Model with fixed timestamps for testing.
func gormModel() gorm.Model {
	return gorm.Model{
		CreatedAt: time.Now().Add(-48 * time.Hour),
		UpdatedAt: time.Now(),
	}
}

// --- ProtocolStorageStatsProvider integration ---

func TestMetaService_ProtocolStats_UsesStorageStatsProvider(t *testing.T) {
	opts := coreTesting.CombineOptions(
		coreTesting.NewMockPluginBuilder("meta").
			WithService("meta", NewMetaService).
			BuilderOption(),
		coreTesting.WithMockUploadService(),
		coreTesting.WithMockPinService(),
		coreTesting.WithMockRenterService(),
		withStatsProtocol("sia", &core.ProtocolStorageStats{
			ObjectCount:          42,
			StorageBytes:         2048,
			PhysicalStorageBytes: 8192,
			PhysicalUnitCount:    10,
		}),
	)

	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)
		pinSvc := coreTesting.GetMockPinService(ctx)

		// Upload stats return different numbers; StorageStats should take priority.
		uploadSvc.EXPECT().GetUploadStats(mock.Anything).Return([]core.ProtocolUploadStat{
			{Protocol: "sia", TotalUploads: 99, TotalStorageBytes: 9999},
		}, nil).Once()
		pinSvc.EXPECT().GetPinStats(mock.Anything).Return([]core.ProtocolPinStat{
			{Protocol: "sia", TotalPins: 15},
		}, nil).Once()

		svc := core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
		require.NotNil(tb, svc)

		resp, err := svc.ProtocolStats(context.Background())
		require.NoError(tb, err)
		require.Len(tb, resp.Protocols, 1)

		assert.Equal(tb, "sia", resp.Protocols[0].Protocol)
		assert.Equal(tb, uint64(42), resp.Protocols[0].TotalUploads)
		assert.Equal(tb, uint64(2048), resp.Protocols[0].TotalStorageBytes)
		assert.Equal(tb, uint64(15), resp.Protocols[0].TotalPins)
	}, opts)
}

func TestMetaService_AggregateStats_UsesStorageStatsProvider(t *testing.T) {
	opts := coreTesting.CombineOptions(
		coreTesting.NewMockPluginBuilder("meta").
			WithService("meta", NewMetaService).
			BuilderOption(),
		coreTesting.WithMockUploadService(),
		coreTesting.WithMockPinService(),
		coreTesting.WithMockRenterService(),
		withStatsProtocol("sia", &core.ProtocolStorageStats{
			ObjectCount:          42,
			StorageBytes:         2048,
			PhysicalStorageBytes: 8192,
			PhysicalUnitCount:    10,
		}),
	)

	coreTesting.RunTestCase(t, func(tb coreTesting.TB, ctx coreTesting.TestContext) {
		uploadSvc := coreTesting.GetMockUploadService(ctx)
		pinSvc := coreTesting.GetMockPinService(ctx)

		uploadSvc.EXPECT().GetUploadStats(mock.Anything).Return([]core.ProtocolUploadStat{
			{Protocol: "sia", TotalUploads: 99, TotalStorageBytes: 9999},
		}, nil).Once()
		pinSvc.EXPECT().GetPinStats(mock.Anything).Return([]core.ProtocolPinStat{
			{Protocol: "sia", TotalPins: 15},
		}, nil).Once()

		svc := core.GetService[pluginCore.MetaService](ctx, pluginCore.META_SERVICE)
		require.NotNil(tb, svc)

		resp, err := svc.AggregateStats(context.Background())
		require.NoError(tb, err)

		// Should use StorageStatsProvider values, not GetUploadStats.
		assert.Equal(tb, uint64(42), resp.TotalCIDs)
		assert.Equal(tb, uint64(15), resp.TotalPinners)
		assert.Equal(tb, uint64(2048), resp.TotalStorageBytes)
	}, opts)
}
