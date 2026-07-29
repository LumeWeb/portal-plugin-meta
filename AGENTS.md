# AGENTS.md

## Project Overview

`portal-plugin-meta` is a Go plugin for the portal framework that exposes anonymized, CID-level statistics via a public API. It provides stats on pinned content (pin status, pinner count, storage days), aggregate protocol-level statistics, Sia object/DAG exports, and quota health integration.

## Common Commands

### Building
```bash
go build ./...
```

### Testing
```bash
go test ./...
go test -v ./internal/api/
```

### Code Generation
```bash
go install github.com/vektra/mockery/v3@v3.7.0
mockery
```

### Linting
```bash
go vet ./...
```

## Architecture

The plugin registers a `core.Service` (MetaService) and a `core.API` on the `meta` subdomain. All routes are under the `/api` group with `/meta/` prefix. All read endpoints are public and unauthenticated — they return only aggregate counts and timestamps, never user IDs or wallet addresses.

### Plugin Registration
- `meta.go` — `init()` calls `core.RegisterPlugin(plugin.GetPluginInfo())`
- `internal/plugin/plugin.go` — `GetPluginInfo()` registers the service and API via `core.ServiceInfo` and `core.APIFactory`
- Service factory: `internal/service/meta.NewMetaService`
- API factory: `api.NewAPI`

### Service Layer
- **MetaService** (`core/types.go` interface, `internal/service/meta/meta.go` implementation)
  - Embeds `*core.BaseComponent`
  - Implements all stats, export, and DAG logic in one unified service
  - Resolves dependencies via `GetServiceOptional` in startup func:
    - `PinService`, `UploadService`, `RenterService` (portal core)
    - `QuotaService` (portal-plugin-quota, optional)

### API Layer
- `internal/api/api.go` — `API` struct embeds `*core.BaseComponent`, resolves `MetaService` via `core.GetService`
- `internal/api/errors.go` — error namespace and definitions
- `internal/api/api_test.go` — integration tests

### Core Package
- `core/types.go` — `MetaService` interface + `META_SERVICE` constant
- `core/dto.go` — response DTOs (CIDStatsResponse, AggregateStatsResponse, etc.)
- `core/mock_MetaService.go` — mockery-generated mock

### Endpoints
- `GET /api/meta/cid/:cid` — pinned status, pinner count, size, storage-days, quota health (when quota plugin available)
- `GET /api/meta/cid/:cid/sia-object` — Sia SharedObject export (slabs, encryption keys, sector refs)
- `GET /api/meta/cid/:cid/dag` — full DAG export (tree of SharedObjects via ProtocolDAGProvider)
- `GET /api/meta/stats` — aggregate totals across all protocols
- `GET /api/meta/stats/protocols` — per-protocol upload/storage/pin counts

### Quota Integration
The `CIDStats` endpoint enriches its response with quota health data when `portal-plugin-quota` is installed:
- `total_quota_bytes` — sum of all pinners' storage limits
- `total_remaining_bytes` — sum of all pinners' remaining storage quota
- `total_used_bytes` — sum of all pinners' current window usage
- `is_unlimited` — true if any pinner has unlimited storage
- `estimated_quota_exhaustion_date` — projected date when quota runs out (nil = indefinite)

When the quota plugin is absent, all quota fields are omitted from the response (graceful degradation via `PinHealthProvider` interface + nil check).

### Key Dependencies
- `go.lumeweb.com/portal/core` — `PinService`, `UploadService`, `RenterService`, `ProtocolDAGProvider`, `ProtocolExportAccessController`
- `go.lumeweb.com/portal-plugin-quota/core` — `QuotaService` (optional, via `PinHealthProvider` interface)
- `go.lumeweb.com/portal/db/models` — `RenterObject`, `Pin`, `Upload`

### Testing Notes
- Test uses `coreTesting.NewMockPluginBuilder("meta").WithService("meta", metaService.NewMetaService)` to register the service
- Mock services (`UploadService`, `PinService`, `RenterService`) must be registered BEFORE the plugin builder so the service startup func can resolve them
- Mocks are generated via `.mockery.yaml` into `core/mock_MetaService.go`
