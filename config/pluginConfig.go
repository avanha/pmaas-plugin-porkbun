package config

// The DNS servers a record can be checked against (see PluginConfig.Resolver). The default, an empty address, is this machine's own resolver.
const (
	ResolverSystem     = ""
	ResolverCloudflare = "1.1.1.1"
	ResolverGoogle     = "8.8.8.8"
)

type PluginConfig struct {
	ApiKey    string
	ApiSecret string
	Domains   map[string]*Domain

	// Resolver is the DNS server the records are looked up through to see whether they give their value: an address, "host" or "host:port",
	// or one of the constants above. The default is this machine's own resolver. A record can have its own (DnsRecord.Resolver).
	Resolver string
}

func (c *PluginConfig) AddDomain(name string) *Domain {
	domain := NewDomain(name)
	c.Domains[name] = domain

	return domain
}
