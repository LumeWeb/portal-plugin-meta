package core

import (
	portalCore "go.lumeweb.com/portal/core"
)

// CIDStatsResponse is returned by CIDStats.
type CIDStatsResponse struct {
	CID           string  `json:"cid"`
	Pinned        bool    `json:"pinned"`
	PinnerCount   uint64  `json:"pinner_count"`
	SizeBytes     uint64  `json:"size_bytes"`
	StorageDays   float64 `json:"storage_days"`
	FirstPinnedAt *string `json:"first_pinned_at,omitempty"`
	LastPinnedAt  *string `json:"last_pinned_at,omitempty"`

	// Quota health fields — populated when portal-plugin-quota is available.
	// All omitted when quota plugin is absent.
	TotalQuotaBytes              *uint64 `json:"total_quota_bytes,omitempty"`
	TotalRemainingBytes          *uint64 `json:"total_remaining_bytes,omitempty"`
	TotalUsedBytes               *uint64 `json:"total_used_bytes,omitempty"`
	IsUnlimited                  *bool   `json:"is_unlimited,omitempty"`
	QuotaRunsOutAt *string `json:"quota_runs_out_at,omitempty"`
}

// AggregateStatsResponse is returned by AggregateStats.
type AggregateStatsResponse struct {
	TotalCIDs         uint64 `json:"total_cids"`
	TotalPinners      uint64 `json:"total_pinners"`
	TotalStorageBytes uint64 `json:"total_storage_bytes"`
}

// ProtocolStatsResponse is returned by ProtocolStats.
type ProtocolStatsResponse struct {
	Protocols []ProtocolStat `json:"protocols"`
}

// ProtocolStat is a per-protocol breakdown.
type ProtocolStat struct {
	Protocol          string `json:"protocol"`
	TotalUploads      uint64 `json:"total_uploads"`
	TotalStorageBytes uint64 `json:"total_storage_bytes"`
	TotalPins         uint64 `json:"total_pins"`
}

// CIDExportResponse is returned by ExportSiaObject.
type CIDExportResponse struct {
	CID          string             `json:"cid"`
	SizeBytes    uint64             `json:"size_bytes"`
	SharedObject *portalCore.SharedObject `json:"shared_object"`
	CreatedAt    string             `json:"created_at"`
	UpdatedAt    string             `json:"updated_at"`
}

// DAGExportResponse is returned by ExportDAG.
type DAGExportResponse struct {
	RootCID        string     `json:"root_cid"`
	TotalBlocks    uint64     `json:"total_blocks"`
	TotalSizeBytes uint64     `json:"total_size_bytes"`
	Blocks         []DAGBlock `json:"blocks"`
}

// DAGBlock is a single block in a DAG export.
type DAGBlock struct {
	CID       string             `json:"cid"`
	Size      uint64             `json:"size"`
	Links     []DAGLink          `json:"links"`
	SiaObject *CIDExportResponse `json:"sia_object,omitempty"`
}

// DAGLink is a parent→child link in a DAG block.
type DAGLink struct {
	CID   string `json:"cid"`
	Index int    `json:"index"`
}
