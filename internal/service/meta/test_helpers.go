package meta

import (
	"context"

	"github.com/stretchr/testify/mock"
	"go.lumeweb.com/portal/core"
	coreTesting "go.lumeweb.com/portal/core/testing"
	"go.lumeweb.com/portal/core/testing/mocks"
)

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

var _ core.ProtocolExportAccessController = (*allowExportProtocol)(nil)

// withAllowExportProtocol registers a mock protocol that implements
// ProtocolExportAccessController. By default CanExportCID returns (true, nil).
// Tests can override via the returned acl mock's EXPECT().
func withAllowExportProtocol(protocolName string) coreTesting.TestContextBuilderOption {
	return coreTesting.WithCustomMockProtocol(protocolName, func(ctx coreTesting.TestContext) core.Protocol {
		mockProto := coreTesting.NewMockProtocol(ctx.T(), protocolName)
		acl := mocks.NewMockProtocolExportAccessController(ctx.T())
		acl.EXPECT().CanExportCID(mock.Anything, mock.Anything).Return(true, nil).Maybe()
		return &allowExportProtocol{MockProtocol: mockProto, acl: acl}
	})
}

// statsProtocol wraps allowExportProtocol and implements
// ProtocolStorageStatsProvider so tests can exercise the stats path.
type statsProtocol struct {
	*allowExportProtocol
	stats *core.ProtocolStorageStats
}

func (s *statsProtocol) StorageStats(ctx context.Context) (*core.ProtocolStorageStats, error) {
	if s.stats == nil {
		return nil, nil
	}
	return s.stats, nil
}

var _ core.ProtocolStorageStatsProvider = (*statsProtocol)(nil)

// withStatsProtocol registers a mock protocol that implements both
// ProtocolExportAccessController and ProtocolStorageStatsProvider.
// The provided stats will be returned from StorageStats().
func withStatsProtocol(protocolName string, stats *core.ProtocolStorageStats) coreTesting.TestContextBuilderOption {
	return coreTesting.WithCustomMockProtocol(protocolName, func(ctx coreTesting.TestContext) core.Protocol {
		mockProto := coreTesting.NewMockProtocol(ctx.T(), protocolName)
		acl := mocks.NewMockProtocolExportAccessController(ctx.T())
		acl.EXPECT().CanExportCID(mock.Anything, mock.Anything).Return(true, nil).Maybe()
		return &statsProtocol{
			allowExportProtocol: &allowExportProtocol{MockProtocol: mockProto, acl: acl},
			stats:               stats,
		}
	})
}
