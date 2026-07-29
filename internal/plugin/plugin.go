package plugin

import (
	"go.lumeweb.com/portal-plugin-meta/build"
	"go.lumeweb.com/portal-plugin-meta/internal"
	"go.lumeweb.com/portal-plugin-meta/internal/api"
	metaService "go.lumeweb.com/portal-plugin-meta/internal/service/meta"
	"go.lumeweb.com/portal/core"
)

func GetPluginInfo() core.PluginInfo {
	return core.PluginInfo{
		ID:      internal.PluginName,
		Version: build.GetInfo(),
		Services: func() ([]core.ServiceInfo, error) {
			return []core.ServiceInfo{
				{ID: internal.PluginName, Factory: metaService.NewMetaService},
			}, nil
		},
		API: func() (core.API, []core.ContextBuilderOption, error) {
			return api.NewAPI()
		},
		APIExtensions: func(core.Context) ([]core.APIExtensionFactory, error) {
			return nil, nil
		},
		Meta: func(ctx core.Context, builder core.PortalMetaBuilder) error {
			return nil
		},
	}
}
