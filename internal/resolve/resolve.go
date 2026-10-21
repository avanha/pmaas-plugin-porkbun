// Package resolve looks a DNS record up the way this machine does, to see whether it answers with what the record is meant to say.
package resolve

import (
	"context"
	"net"
	"net/netip"
	"strings"
)

// Lookup answers a record's type and fully qualified name with the values it resolves to.
type Lookup func(ctx context.Context, recordType string, fqdn string) ([]string, error)

// Supported is whether a record of the type can be checked: the types a machine's resolver answers simply.
func Supported(recordType string) bool {
	switch strings.ToUpper(recordType) {
	case "A", "AAAA", "CNAME", "TXT":
		return true
	default:
		return false
	}
}

// FQDN is the name of a record in a domain: the name is the part before the domain, and empty, or "@", for the domain itself.
func FQDN(name string, domain string) string {
	if name == "" || name == "@" {
		return domain
	}

	if strings.HasSuffix(name, "."+domain) {
		return name
	}

	return name + "." + domain
}

// System is the Lookup that asks the resolver of this machine (the Go resolver, which uses what the system is set up to).
func System(ctx context.Context, recordType string, fqdn string) ([]string, error) {
	resolver := net.DefaultResolver

	switch strings.ToUpper(recordType) {
	case "A", "AAAA":
		network := "ip4"
		if strings.EqualFold(recordType, "AAAA") {
			network = "ip6"
		}

		addresses, err := resolver.LookupNetIP(ctx, network, fqdn)
		if err != nil {
			return nil, err
		}

		values := make([]string, len(addresses))
		for i, address := range addresses {
			values[i] = address.String()
		}

		return values, nil
	case "CNAME":
		target, err := resolver.LookupCNAME(ctx, fqdn)
		if err != nil {
			return nil, err
		}

		return []string{target}, nil
	case "TXT":
		return resolver.LookupTXT(ctx, fqdn)
	default:
		return nil, &net.DNSError{Err: "record type not supported", Name: fqdn}
	}
}

// Same is whether two values of a record of the type are the same value: addresses are compared as addresses, names without regard to
// case or a trailing dot, and text without the quotes a DNS host may put round it.
func Same(recordType string, a string, b string) bool {
	return normalize(recordType, a) == normalize(recordType, b)
}

// Matches is whether the record's value is among those it resolved to. A record that has more than one value (several addresses) has
// only this one of them to be found.
func Matches(recordType string, expected string, resolved []string) bool {
	for _, value := range resolved {
		if Same(recordType, expected, value) {
			return true
		}
	}

	return false
}

func normalize(recordType string, value string) string {
	value = strings.TrimSpace(value)

	switch strings.ToUpper(recordType) {
	case "A", "AAAA":
		if address, err := netip.ParseAddr(value); err == nil {
			return address.Unmap().String()
		}

		return value
	case "CNAME":
		return strings.ToLower(strings.TrimSuffix(value, "."))
	case "TXT":
		return strings.Trim(value, `"`)
	default:
		return value
	}
}
