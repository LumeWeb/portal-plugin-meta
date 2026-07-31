package meta

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ipfs/go-cid"
	pluginCore "go.lumeweb.com/portal-plugin-meta/core"
	quotaCore "go.lumeweb.com/portal-plugin-quota/core"
	"go.lumeweb.com/portal/core"
	"go.lumeweb.com/portal/db/models"
	"go.opentelemetry.io/otel/attribute"
)

var (
	ErrCIDNotFound     = errors.New("cid not found")
	ErrObjectNotReady  = errors.New("object is staged/packing; try again later")
	ErrDAGNotSupported = errors.New("protocol does not support DAG traversal")
)

type MetaServiceDefault struct {
	*core.BaseComponent
	pinSvc       core.PinService
	uploadSvc    core.UploadService
	renterSvc    core.RenterService
	pinHealthSvc pluginCore.PinHealthProvider // optional — nil when quota plugin absent
}

var _ pluginCore.MetaService = (*MetaServiceDefault)(nil)

func NewMetaService() (core.Service, []core.ContextBuilderOption, error) {
	service := &MetaServiceDefault{}

	return service, core.ContextOptions(
		core.ContextWithStartupFunc(func(ctx core.Context) error {
			service.pinSvc = core.GetService[core.PinService](ctx, core.PIN_SERVICE)
			if service.pinSvc == nil {
				return fmt.Errorf("%s service required", core.PIN_SERVICE)
			}
			service.uploadSvc = core.GetService[core.UploadService](ctx, core.UPLOAD_SERVICE)
			if service.uploadSvc == nil {
				return fmt.Errorf("%s service required", core.UPLOAD_SERVICE)
			}
			service.renterSvc = core.GetService[core.RenterService](ctx, core.RENTER_SERVICE)
			if service.renterSvc == nil {
				return fmt.Errorf("%s service required", core.RENTER_SERVICE)
			}

			// Optionally resolve the quota plugin's QuotaService.
			if qs := core.GetServiceOptional[quotaCore.QuotaService](ctx, quotaCore.QUOTA_SERVICE); qs != nil {
				service.pinHealthSvc = qs
			}

			return nil
		}),
	), nil
}

func (s *MetaServiceDefault) ID() string { return pluginCore.META_SERVICE }

func (s *MetaServiceDefault) CIDStats(ctx context.Context, cidStr string) (*pluginCore.CIDStatsResponse, error) {
	ctx, span := core.TraceMethod(ctx, "MetaService.CIDStats")
	defer span.End()
	span.SetAttributes(attribute.String("meta.cid", cidStr))

	hash, err := core.ParseStorageHash(cidStr)
	if err != nil {
		return nil, err
	}

	upload, err := s.uploadSvc.GetUpload(ctx, hash)
	if err != nil {
		if errors.Is(err, core.ErrUploadNotFound) {
			return nil, ErrCIDNotFound
		}
		return nil, err
	}

	// Per-CID stats reveal info about a specific upload — gate behind
	// the export ACL just like ExportSiaObject and ExportDAG.
	if err := s.checkExportAllowed(ctx, upload, cidStr); err != nil {
		return nil, err
	}

	resp := &pluginCore.CIDStatsResponse{
		CID:       cidStr,
		SizeBytes: upload.Size,
	}

	pinned, err := s.pinSvc.UploadPinnedGlobal(ctx, hash)
	if err != nil {
		return nil, err
	}
	resp.Pinned = pinned

	pins, err := s.pinSvc.GetAllPinsByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	resp.PinCount = uint64(len(pins))

	if len(pins) == 0 {
		return resp, nil
	}

	var totalDays float64
	var first, last time.Time
	for i, p := range pins {
		age := time.Since(p.CreatedAt).Hours() / 24
		totalDays += age
		if i == 0 || p.CreatedAt.Before(first) {
			first = p.CreatedAt
		}
		if i == 0 || p.CreatedAt.After(last) {
			last = p.CreatedAt
		}
	}
	resp.StorageDays = float64(int(totalDays*10)) / 10

	firstStr := first.Format(time.RFC3339)
	lastStr := last.Format(time.RFC3339)
	resp.FirstPinnedAt = &firstStr
	resp.LastPinnedAt = &lastStr

	// If the quota plugin is available, enrich with pin health data.
	// requesterID = 0 means system call — always authorized.
	if s.pinHealthSvc != nil {
		if health, err := s.pinHealthSvc.GetCIDPinHealth(ctx, hash, 0); err == nil && health != nil {
			tqb := health.TotalQuotaBytes
			trb := health.TotalRemainingBytes
			tub := health.TotalUsedBytes
			unlimited := health.IsUnlimited
			resp.TotalQuotaBytes = &tqb
			resp.TotalRemainingBytes = &trb
			resp.TotalUsedBytes = &tub
			resp.IsUnlimited = &unlimited
			if health.EstimatedQuotaExhaustionDate != nil {
				exhaustStr := health.EstimatedQuotaExhaustionDate.Format(time.RFC3339)
				resp.QuotaRunsOutAt = &exhaustStr
			}
		}
		// On error, omit quota fields rather than failing the whole request.
	}

	return resp, nil
}

func (s *MetaServiceDefault) AggregateStats(ctx context.Context) (*pluginCore.AggregateStatsResponse, error) {
	ctx, span := core.TraceMethod(ctx, "MetaService.AggregateStats")
	defer span.End()

	// Derive from ProtocolStats to avoid duplicated logic.
	protoResp, err := s.ProtocolStats(ctx)
	if err != nil {
		return nil, err
	}

	var totalUploads, totalPins, totalStorageBytes uint64
	for _, p := range protoResp.Protocols {
		totalUploads += p.TotalUploads
		totalPins += p.TotalPins
		totalStorageBytes += p.TotalStorageBytes
	}

	return &pluginCore.AggregateStatsResponse{
		TotalUploads:      totalUploads,
		TotalPins:         totalPins,
		TotalStorageBytes: totalStorageBytes,
	}, nil
}

// getProtocolStorageStats returns (storageBytes, objectCount, true) if the
// protocol implements ProtocolStorageStatsProvider, otherwise (0, 0, false).
func (s *MetaServiceDefault) getProtocolStorageStats(ctx context.Context, name string) (storageBytes, objectCount uint64, ok bool) {
	_, span := core.TraceMethod(ctx, "MetaService.getProtocolStorageStats")
	defer span.End()
	span.SetAttributes(attribute.String("meta.protocol", name))

	proto := core.GetProtocol(name)
	if proto == nil {
		return 0, 0, false
	}
	statsProvider, isStats := proto.(core.ProtocolStorageStatsProvider)
	if !isStats {
		return 0, 0, false
	}
	stats, err := statsProvider.StorageStats(ctx)
	if err != nil || stats == nil {
		return 0, 0, false
	}
	return stats.StorageBytes, stats.ObjectCount, true
}

func (s *MetaServiceDefault) ProtocolStats(ctx context.Context) (*pluginCore.ProtocolStatsResponse, error) {
	ctx, span := core.TraceMethod(ctx, "MetaService.ProtocolStats")
	defer span.End()

	uploadStats, err := s.uploadSvc.GetUploadStats(ctx)
	if err != nil {
		return nil, err
	}

	pinStats, err := s.pinSvc.GetPinStats(ctx)
	if err != nil {
		return nil, err
	}

	pinMap := make(map[string]uint64)
	for _, ps := range pinStats {
		pinMap[ps.Protocol] = ps.TotalPins
	}

	// Build upload stats map for fallback.
	uploadMap := make(map[string]core.ProtocolUploadStat)
	for _, us := range uploadStats {
		uploadMap[us.Protocol] = us
	}

	// Build unified sorted protocol set from upload stats, pin stats,
	// and registered protocols so nothing is dropped and order is deterministic.
	protocolSet := make(map[string]struct{})
	for _, us := range uploadStats {
		protocolSet[us.Protocol] = struct{}{}
	}
	for _, ps := range pinStats {
		protocolSet[ps.Protocol] = struct{}{}
	}
	for name := range core.GetProtocols() {
		protocolSet[name] = struct{}{}
	}

	protocols := make([]string, 0, len(protocolSet))
	for name := range protocolSet {
		protocols = append(protocols, name)
	}
	slices.Sort(protocols)

	resp := &pluginCore.ProtocolStatsResponse{Protocols: make([]pluginCore.ProtocolStat, 0, len(protocols))}
	for _, name := range protocols {
		// Try ProtocolStorageStatsProvider first for accurate storage bytes.
		storageBytes, objectCount, ok := s.getProtocolStorageStats(ctx, name)
		if ok {
			resp.Protocols = append(resp.Protocols, pluginCore.ProtocolStat{
				Protocol:          name,
				TotalUploads:      objectCount,
				TotalStorageBytes: storageBytes,
				TotalPins:         pinMap[name],
			})
		} else {
			us := uploadMap[name]
			resp.Protocols = append(resp.Protocols, pluginCore.ProtocolStat{
				Protocol:          name,
				TotalUploads:      us.TotalUploads,
				TotalStorageBytes: us.TotalStorageBytes,
				TotalPins:         pinMap[name],
			})
		}
	}
	return resp, nil
}

func (s *MetaServiceDefault) ExportSiaObject(ctx context.Context, cidStr string) (*pluginCore.CIDExportResponse, error) {
	ctx, span := core.TraceMethod(ctx, "MetaService.ExportSiaObject")
	defer span.End()
	span.SetAttributes(attribute.String("meta.cid", cidStr))

	hash, err := core.ParseStorageHash(cidStr)
	if err != nil {
		return nil, err
	}

	upload, err := s.uploadSvc.GetUpload(ctx, hash)
	if err != nil {
		if errors.Is(err, core.ErrUploadNotFound) {
			return nil, ErrCIDNotFound
		}
		return nil, err
	}

	if err := s.checkExportAllowed(ctx, upload, cidStr); err != nil {
		return nil, err
	}

	proto := core.GetProtocol(upload.Protocol)
	if proto == nil {
		return nil, fmt.Errorf("protocol %q not registered", upload.Protocol)
	}

	storageProto, ok := proto.(core.StorageProtocol)
	if !ok {
		return nil, fmt.Errorf("protocol %q does not support storage operations", upload.Protocol)
	}

	bucket := upload.Protocol
	objectKey := storageProto.EncodeFileName(hash)

	sharedObj, renterObj, err := s.renterSvc.SharedObject(ctx, bucket, objectKey)
	if err != nil {
		if errors.Is(err, core.ErrUploadNotFound) {
			return nil, ErrCIDNotFound
		}
		return nil, err
	}
	if renterObj == nil {
		return nil, ErrCIDNotFound
	}

	if renterObj.Status != models.RenterObjectStatusUploaded {
		return nil, ErrObjectNotReady
	}

	if sharedObj == nil {
		return nil, ErrObjectNotReady
	}

	return &pluginCore.CIDExportResponse{
		CID:          cidStr,
		SizeBytes:    uint64(renterObj.Size),
		SharedObject: sharedObj,
		CreatedAt:    renterObj.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:    renterObj.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

func (s *MetaServiceDefault) ExportDAG(ctx context.Context, rootCIDStr string) (*pluginCore.DAGExportResponse, error) {
	ctx, span := core.TraceMethod(ctx, "MetaService.ExportDAG")
	defer span.End()
	span.SetAttributes(attribute.String("meta.rootCID", rootCIDStr))

	hash, err := core.ParseStorageHash(rootCIDStr)
	if err != nil {
		return nil, err
	}

	upload, err := s.uploadSvc.GetUpload(ctx, hash)
	if err != nil {
		if errors.Is(err, core.ErrUploadNotFound) {
			return nil, ErrCIDNotFound
		}
		return nil, err
	}

	if err := s.checkExportAllowed(ctx, upload, rootCIDStr); err != nil {
		return nil, err
	}

	// Resolve protocol and storage protocol once for the root upload.
	proto := core.GetProtocol(upload.Protocol)
	if proto == nil {
		return nil, ErrDAGNotSupported
	}
	dagProvider, ok := proto.(core.ProtocolDAGProvider)
	if !ok {
		return nil, ErrDAGNotSupported
	}
	storageProto, ok := proto.(core.StorageProtocol)
	if !ok {
		return nil, ErrDAGNotSupported
	}
	bucket := upload.Protocol

	rootCID, err := cid.Decode(rootCIDStr)
	if err != nil {
		return nil, err
	}

	nodes, err := dagProvider.ResolveDAG(ctx, rootCID)
	if err != nil {
		if errors.Is(err, core.ErrDAGNotSupported) {
			return nil, ErrDAGNotSupported
		}
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, ErrCIDNotFound
	}
	span.SetAttributes(attribute.Int("meta.dag.blockCount", len(nodes)))

	var blocks []pluginCore.DAGBlock
	var totalSize uint64

	for _, node := range nodes {
		cidStr := node.CID.String()
		totalSize += node.Size

		var links []pluginCore.DAGLink
		for i, child := range node.Children {
			links = append(links, pluginCore.DAGLink{
				CID:   child.String(),
				Index: i,
			})
		}

		siaObj, err := s.exportBlockSiaObject(ctx, storageProto, bucket, node.CID.String())
		if err != nil {
			if errors.Is(err, ErrCIDNotFound) || errors.Is(err, ErrObjectNotReady) {
				// No Sia object for this block — omit sia_object (nil)
			} else {
				return nil, err
			}
		}

		blocks = append(blocks, pluginCore.DAGBlock{
			CID:       cidStr,
			Size:      node.Size,
			Links:     links,
			SiaObject: siaObj,
		})
	}

	return &pluginCore.DAGExportResponse{
		RootCID:        rootCIDStr,
		TotalBlocks:    uint64(len(blocks)),
		TotalSizeBytes: totalSize,
		Blocks:         blocks,
	}, nil
}

// exportBlockSiaObject resolves the Sia renter object for a single DAG block
// using the already-resolved protocol and bucket, skipping the redundant CID
// parsing, upload lookup, protocol lookup, and ACL check that ExportSiaObject
// would repeat per block.
func (s *MetaServiceDefault) exportBlockSiaObject(ctx context.Context, storageProto core.StorageProtocol, bucket, cidStr string) (*pluginCore.CIDExportResponse, error) {
	ctx, span := core.TraceMethod(ctx, "MetaService.exportBlockSiaObject")
	defer span.End()
	span.SetAttributes(
		attribute.String("meta.cid", cidStr),
		attribute.String("meta.bucket", bucket),
	)

	hash, err := core.ParseStorageHash(cidStr)
	if err != nil {
		return nil, err
	}

	objectKey := storageProto.EncodeFileName(hash)

	sharedObj, renterObj, err := s.renterSvc.SharedObject(ctx, bucket, objectKey)
	if err != nil {
		if errors.Is(err, core.ErrUploadNotFound) {
			return nil, ErrCIDNotFound
		}
		return nil, err
	}
	if renterObj == nil {
		return nil, ErrCIDNotFound
	}

	if renterObj.Status != models.RenterObjectStatusUploaded {
		return nil, ErrObjectNotReady
	}

	if sharedObj == nil {
		return nil, ErrObjectNotReady
	}

	return &pluginCore.CIDExportResponse{
		CID:          cidStr,
		SizeBytes:    uint64(renterObj.Size),
		SharedObject: sharedObj,
		CreatedAt:    renterObj.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:    renterObj.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

func (s *MetaServiceDefault) checkExportAllowed(ctx context.Context, upload *models.Upload, cidStr string) error {
	_, span := core.TraceMethod(ctx, "MetaService.checkExportAllowed")
	defer span.End()
	span.SetAttributes(
		attribute.String("meta.cid", cidStr),
		attribute.String("meta.protocol", upload.Protocol),
	)

	proto := core.GetProtocol(upload.Protocol)
	if proto == nil {
		return core.ErrExportDenied
	}

	acl, ok := proto.(core.ProtocolExportAccessController)
	if !ok {
		return core.ErrExportDenied
	}

	allowed, err := acl.CanExportCID(ctx, cidStr)
	if err != nil {
		return err
	}
	if !allowed {
		return core.ErrExportDenied
	}
	return nil
}

