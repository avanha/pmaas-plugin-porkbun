package resolve

import "testing"

func TestFQDN(t *testing.T) {
	for _, c := range []struct{ name, domain, want string }{
		{"www", "example.com", "www.example.com"},
		{"", "example.com", "example.com"},
		{"@", "example.com", "example.com"},
		{"www.example.com", "example.com", "www.example.com"},
	} {
		if got := FQDN(c.name, c.domain); got != c.want {
			t.Errorf("%q in %q: %q, want %q", c.name, c.domain, got, c.want)
		}
	}
}

func TestSame_ComparesValuesTheWayTheirTypeIsWritten(t *testing.T) {
	for _, c := range []struct {
		recordType, a, b string
		want             bool
	}{
		{"A", "10.0.0.1", "10.0.0.1", true},
		{"A", "10.0.0.1", "10.0.0.2", false},
		{"AAAA", "2001:DB8::1", "2001:db8:0:0:0:0:0:1", true},
		{"AAAA", "::ffff:10.0.0.1", "10.0.0.1", true},
		{"CNAME", "Host.Example.com.", "host.example.com", true},
		{"TXT", `"v=spf1 -all"`, "v=spf1 -all", true},
		{"TXT", "a", "b", false},
	} {
		if got := Same(c.recordType, c.a, c.b); got != c.want {
			t.Errorf("%s %q %q: %v", c.recordType, c.a, c.b, got)
		}
	}
}

func TestMatches_IsOneOfTheValuesResolvedTo(t *testing.T) {
	if !Matches("A", "10.0.0.2", []string{"10.0.0.1", "10.0.0.2"}) || Matches("A", "10.0.0.3", []string{"10.0.0.1"}) || Matches("A", "10.0.0.1", nil) {
		t.Error("wrong")
	}
}

func TestSupported(t *testing.T) {
	if !Supported("a") || !Supported("TXT") || Supported("MX") || Supported("SRV") {
		t.Error("wrong")
	}
}
