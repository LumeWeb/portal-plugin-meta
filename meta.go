package meta

import (
	pluginplugin "go.lumeweb.com/portal-plugin-meta/internal/plugin"
	"go.lumeweb.com/portal/core"
)

func init() {
	core.RegisterPlugin(pluginplugin.GetPluginInfo())
}
