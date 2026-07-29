package config

import (
	z "github.com/Oudwins/zog"

	"go.lumeweb.com/portal/config"
)

var _ config.APIConfig = (*APIConfig)(nil)

type APIConfig struct {
	Subdomain string `config:"subdomain"`
}

func (a APIConfig) Schema() z.ZogSchema {
	return z.Struct(z.Shape{
		"Subdomain": z.String().Required(),
	})
}

func (a APIConfig) Defaults() map[string]any {
	return map[string]any{
		"Subdomain": "meta",
	}
}
