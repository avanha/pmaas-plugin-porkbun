package config

type PluginConfig struct {
	ApiKey    string
	ApiSecret string
	Domains   map[string]*Domain
}

func (c *PluginConfig) AddDomain(name string) *Domain {
	domain := NewDomain(name)
	c.Domains[name] = domain

	return domain
}
