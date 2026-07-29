package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ipfs/go-cid"
	pluginCore "go.lumeweb.com/portal-plugin-meta/core"
	quotaCore "go.lumeweb.com/portal-plugin-quota/core"
	"go.lumeweb.com/portal/core"
	"go.lumeweb.com/portal/db/models"
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
	resp.PinnerCount = uint64(len(pins))

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
	uploadStats, err := s.uploadSvc.GetUploadStats(ctx)
	if err != nil {
		return nil, err
	}

	pinStats, err := s.pinSvc.GetPinStats(ctx)
	if err != nil {
		return nil, err
	}

	var totalCIDs, totalStorageBytes, totalPins uint64
	for _, us := range uploadStats {
		totalCIDs += us.TotalUploads
		totalStorageBytes += us.TotalStorageBytes
	}
	for _, ps := range pinStats {
		totalPins += ps.TotalPins
	}

	// TODO: compute real TotalStorageDays once core PinService exposes
	// a global pin iterator or aggregate storage-days metric. The per-hash
	// GetAllPinsByHash API is not suitable for a global aggregation.

	return &pluginCore.AggregateStatsResponse{
		TotalCIDs:         totalCIDs,
		TotalPinners:      totalPins,
		TotalStorageBytes: totalStorageBytes,
		TotalStorageDays:  0,
	}, nil
}

func (s *MetaServiceDefault) ProtocolStats(ctx context.Context) (*pluginCore.ProtocolStatsResponse, error) {
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

	// Build unified sorted protocol set from both upload and pin stats so
	// protocols with pins but no uploads are not dropped, and output
	// order is deterministic.
	uploadMap := make(map[string]core.ProtocolUploadStat)
	for _, us := range uploadStats {
		uploadMap[us.Protocol] = us
	}

	protocolSet := make(map[string]struct{})
	for _, us := range uploadStats {
		protocolSet[us.Protocol] = struct{}{}
	}
	for _, ps := range pinStats {
		protocolSet[ps.Protocol] = struct{}{}
	}

	protocols := make([]string, 0, len(protocolSet))
	for name := range protocolSet {
		protocols = append(protocols, name)
	}
	slices.Sort(protocols)

	resp := &pluginCore.ProtocolStatsResponse{Protocols: make([]pluginCore.ProtocolStat, 0, len(protocols))}
	for _, name := range protocols {
		us := uploadMap[name]
		resp.Protocols = append(resp.Protocols, pluginCore.ProtocolStat{
			Protocol:          name,
			TotalUploads:      us.TotalUploads,
			TotalStorageBytes: us.TotalStorageBytes,
			TotalPins:         pinMap[name],
		})
	}
	return resp, nil
}

func (s *MetaServiceDefault) ExportSiaObject(ctx context.Context, cidStr string) (*pluginCore.CIDExportResponse, error) {
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

	exists, renterObj, err := s.renterSvc.UploadExists(ctx, bucket, objectKey)
	if err != nil {
		return nil, err
	}
	if !exists || renterObj == nil {
		return nil, ErrCIDNotFound
	}

	if renterObj.Status != models.RenterObjectStatusUploaded {
		return nil, ErrObjectNotReady
	}

	sharedObj, err := buildSharedObjectJSON(renterObj)
	if err != nil {
		return nil, err
	}

	return &pluginCore.CIDExportResponse{
		CID:          cidStr,
		SiaObjectID:  renterObj.SiaObjectID,
		SizeBytes:    uint64(renterObj.Size),
		Bucket:       renterObj.Bucket,
		ObjectKey:    renterObj.ObjectKey,
		SharedObject: sharedObj,
		CreatedAt:    renterObj.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:    renterObj.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

func (s *MetaServiceDefault) ExportDAG(ctx context.Context, rootCIDStr string) (*pluginCore.DAGExportResponse, error) {
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
	hash, err := core.ParseStorageHash(cidStr)
	if err != nil {
		return nil, err
	}

	objectKey := storageProto.EncodeFileName(hash)

	exists, renterObj, err := s.renterSvc.UploadExists(ctx, bucket, objectKey)
	if err != nil {
		return nil, err
	}
	if !exists || renterObj == nil {
		return nil, ErrCIDNotFound
	}

	if renterObj.Status != models.RenterObjectStatusUploaded {
		return nil, ErrObjectNotReady
	}

	sharedObj, err := buildSharedObjectJSON(renterObj)
	if err != nil {
		return nil, err
	}

	return &pluginCore.CIDExportResponse{
		CID:          cidStr,
		SiaObjectID:  renterObj.SiaObjectID,
		SizeBytes:    uint64(renterObj.Size),
		Bucket:       renterObj.Bucket,
		ObjectKey:    renterObj.ObjectKey,
		SharedObject: sharedObj,
		CreatedAt:    renterObj.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:    renterObj.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

func (s *MetaServiceDefault) checkExportAllowed(ctx context.Context, upload *models.Upload, cidStr string) error {
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

// buildSharedObjectJSON extracts the SharedObject from a RenterObject's SealedData.
func buildSharedObjectJSON(renterObj *models.RenterObject) (map[string]any, error) {
	var shared map[string]any
	if err := json.Unmarshal(renterObj.SealedData, &shared); err != nil {
		return nil, fmt.Errorf("failed to parse SealedData: %w", err)
	}
	return shared, nil
}
