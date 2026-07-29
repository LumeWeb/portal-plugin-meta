package core

import (
	"context"

	quotaCore "go.lumeweb.com/portal-plugin-quota/core"
	"go.lumeweb.com/portal/core"
)

const META_SERVICE = "meta"

// PinHealthProvider is satisfied by quotaCore.QuotaService.
// Exposed publicly so consumers can provide alternative implementations.
type PinHealthProvider interface {
	GetCIDPinHealth(ctx context.Context, cid core.StorageHash, requesterID uint) (*quotaCore.CIDPinHealth, error)
}

// MetaService is the interface for meta statistics and export functionality.
type MetaService interface {
	core.Service

	// Stats
	CIDStats(ctx context.Context, cidStr string) (*CIDStatsResponse, error)
	AggregateStats(ctx context.Context) (*AggregateStatsResponse, error)
	ProtocolStats(ctx context.Context) (*ProtocolStatsResponse, error)

	// Export
	ExportSiaObject(ctx context.Context, cidStr string) (*CIDExportResponse, error)
	ExportDAG(ctx context.Context, rootCIDStr string) (*DAGExportResponse, error)
}
