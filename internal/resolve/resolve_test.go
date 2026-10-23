package resolve

import (
	"context"
	"net"
	"testing"
	"time"
)

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

// serveDNS answers every query with one A record for 10.1.2.3, from a socket of its own, and says what it was asked.
func serveDNS(t *testing.T) (address string, asked chan string) {
	t.Helper()

	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no UDP socket: %v", err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	asked = make(chan string, 8)

	go func() {
		buf := make([]byte, 1500)

		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}

			query := buf[:n]

			// The question follows the 12 byte header: labels, a zero, and four bytes of type and class.
			end, name := 12, ""
			for end < n && query[end] != 0 {
				length := int(query[end])
				name += string(query[end+1:end+1+length]) + "."
				end += 1 + length
			}

			end += 5
			asked <- name

			reply := append([]byte{}, query[:2]...)                   // the id
			reply = append(reply, 0x81, 0x80, 0, 1, 0, 1, 0, 0, 0, 0) // a response, one question, one answer
			reply = append(reply, query[12:end]...)
			reply = append(reply, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 10, 1, 2, 3)

			_, _ = conn.WriteTo(reply, from)
		}
	}()

	return conn.LocalAddr().String(), asked
}

func TestServer_AsksTheServerItWasGiven(t *testing.T) {
	address, asked := serveDNS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	values, err := Server(address)(ctx, "A", "mailhost.example.com")
	if err != nil || len(values) != 1 || values[0] != "10.1.2.3" {
		t.Fatalf("%v %v", values, err)
	}

	select {
	case name := <-asked:
		if name != "mailhost.example.com." {
			t.Errorf("asked for %q", name)
		}
	default:
		t.Error("the server was not asked")
	}
}

func TestWithPort_AddsTheDnsPortToWhatHasNone(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"1.1.1.1", "1.1.1.1:53"}, {"8.8.8.8:5353", "8.8.8.8:5353"}, {"2606:4700:4700::1111", "[2606:4700:4700::1111]:53"}, {"[2606:4700:4700::1111]:53", "[2606:4700:4700::1111]:53"},
	} {
		if got := withPort(c.in); got != c.want {
			t.Errorf("%q: %q, want %q", c.in, got, c.want)
		}
	}
}
