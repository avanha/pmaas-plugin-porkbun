package dnsRecord

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/avanha/pmaas-plugin-porkbun/internal/resolve"
	"github.com/avanha/pmaas-spi"
	"github.com/avanha/pmaas-spi/alert"
)

const (
	// resolveGrace is how long after the record's TTL a resolver that still answers with an old value is allowed to: a resolver is
	// entitled to its cache until the TTL is up, and a little longer is for a forwarder in between that adds its own.
	resolveGrace = 5 * time.Minute

	// recheckEvery is how often the resolver is asked again while it does not give the record's value.
	recheckEvery = 5 * time.Minute

	// lookupTimeout is how long a lookup is waited for.
	lookupTimeout = 10 * time.Second

	// defaultTtl is the TTL of a record that has none: Porkbun's lowest.
	defaultTtl = 600 * time.Second
)

// Deps is what the record needs of its surroundings, which a test can replace. A field left empty has the default.
type Deps struct {
	// Lookup resolves the record the way this machine does. The default asks the system's resolver.
	Lookup resolve.Lookup

	// Now is the time. The default is the clock.
	Now func() time.Time

	// After runs f on the plugin's goroutine once the time has passed, and returns what stops it if it hasn't.
	After func(d time.Duration, f func()) (stop func())

	// Enqueue runs f on the plugin's goroutine, which is where what a lookup found is taken in.
	Enqueue func(f func()) error

	// Go runs f off the plugin's goroutine, which is where a lookup waits.
	Go func(f func())

	// Resolver is the DNS server Lookup asks, for the page and the alert to say, or empty for this machine's own resolver.
	Resolver string

	// Alerts and AlertSource are where the alert for the record is raised from.
	Alerts      alert.Broadcaster
	AlertSource string
}

func (d Deps) withDefaults(container spi.IPMAASContainer) Deps {
	if d.Lookup == nil {
		d.Lookup = resolve.System
	}

	if d.Now == nil {
		d.Now = time.Now
	}

	if d.Enqueue == nil {
		d.Enqueue = func(f func()) error { return container.EnqueueOnPluginGoRoutine(f) }
	}

	if d.After == nil {
		enqueue := d.Enqueue
		d.After = func(delay time.Duration, f func()) func() {
			timer := time.AfterFunc(delay, func() { _ = enqueue(f) })

			return func() { timer.Stop() }
		}
	}

	if d.Go == nil {
		d.Go = func(f func()) { go f() }
	}

	if d.Alerts == nil {
		d.Alerts = container
	}

	if d.AlertSource == "" {
		d.AlertSource = "porkbun"
	}

	return d
}

// checkState is what is known about whether the record is as it should be. Only the plugin's goroutine uses it.
type checkState struct {
	// requested is the value last asked for, and pendingUpdates how many requests for one have not been answered.
	requested      string
	pendingUpdates int

	// apiProblem and resolveProblem are what is wrong, if anything: what Porkbun holds, and what resolves.
	apiProblem     string
	resolveProblem string

	// mismatchSince is when the resolver was first seen to answer with something else than the record's value, or zero if it does.
	mismatchSince time.Time

	// generation says which lookup is the latest, so that the answer to an earlier one is dropped, and stopRecheck stops the one waiting.
	generation  int
	stopRecheck func()

	alertRaised  bool
	alertMessage string
	closed       bool
}

func (r *DnsRecord) fqdn() string {
	return resolve.FQDN(r.currentData.Name, r.domain)
}

func (r *DnsRecord) ttl() time.Duration {
	if r.currentData.Ttl <= 0 {
		return defaultTtl
	}

	return time.Duration(r.currentData.Ttl) * time.Second
}

// afterRead is what is made of the record once its value has been taken from Porkbun: whether it is what was asked for, and what the
// resolver says.
func (r *DnsRecord) afterRead() {
	// While a request is on its way, a read that has the earlier value is not a sign of anything.
	if r.check.pendingUpdates == 0 {
		switch {
		case r.check.requested == "" || resolve.Same(r.currentData.Type, r.currentData.Value, r.check.requested):
			r.check.apiProblem = ""
		default:
			r.check.apiProblem = fmt.Sprintf("Porkbun has %q, not the %q that was asked for", r.currentData.Value, r.check.requested)
		}
	}

	r.syncAlert()
	r.startResolve()
}

// restartResolveGrace is that the record has just been changed: the resolver is given until the TTL and the grace period from now.
func (r *DnsRecord) restartResolveGrace() {
	r.check.mismatchSince = time.Time{}
	r.check.resolveProblem = ""
	r.currentData.ResolveChecked = false
}

// startResolve asks the resolver for the record, off the plugin's goroutine, and takes the answer in on it.
func (r *DnsRecord) startResolve() {
	if r.check.closed || r.currentData.Value == "" {
		return
	}

	r.stopRecheck()

	if !resolve.Supported(r.currentData.Type) {
		r.currentData.ResolveSupported = false
		r.check.resolveProblem = ""
		r.syncAlert()

		return
	}

	r.check.generation++
	generation, recordType, fqdn, expected := r.check.generation, r.currentData.Type, r.fqdn(), r.currentData.Value

	r.deps.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
		defer cancel()

		values, err := r.deps.Lookup(ctx, recordType, fqdn)

		if enqueueErr := r.deps.Enqueue(func() { r.onResolved(generation, expected, values, err) }); enqueueErr != nil {
			r.log.Debug("unable to take in the answer of a lookup", "name", fqdn, "error", enqueueErr)
		}
	})
}

// onResolved takes in the answer of a lookup. Only a record that still gives the resolver something else after its TTL and a grace
// period is a problem: until then an old value is what a cache is for.
func (r *DnsRecord) onResolved(generation int, expected string, values []string, err error) {
	if r.check.closed || generation != r.check.generation {
		return
	}

	now := r.deps.Now()
	r.currentData.ResolveSupported = true
	r.currentData.ResolveError = ""

	if err != nil {
		var dnsErr *net.DNSError

		if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
			// The resolver could not say, which says nothing about the record.
			r.currentData.ResolveError = err.Error()
			r.log.Warn("unable to resolve DNS record", "name", r.fqdn(), "error", err)
			r.recheckLater()

			return
		}

		values = nil
	}

	r.currentData.ResolveTime = now
	r.currentData.ResolvedValues = values

	matches := resolve.Matches(r.currentData.Type, expected, values)
	r.currentData.ResolveChecked, r.currentData.ResolveMatches = true, matches

	if matches {
		r.check.mismatchSince, r.check.resolveProblem = time.Time{}, ""
		r.syncAlert()

		return
	}

	if r.check.mismatchSince.IsZero() {
		r.check.mismatchSince = now
	}

	start := r.check.mismatchSince
	if r.currentData.LastModifiedTime.After(start) {
		start = r.currentData.LastModifiedTime
	}

	if due := start.Add(r.ttl() + resolveGrace); now.Before(due) {
		r.log.Debug("DNS record resolves to something else, within its TTL", "name", r.fqdn(), "expected", expected, "resolved", values, "until", due)
	} else {
		r.check.resolveProblem = fmt.Sprintf("%s resolves %s to %s, not %q, more than %s after it was changed or first seen so",
			r.resolverName(), r.fqdn(), describe(values), expected, (r.ttl() + resolveGrace).Round(time.Second))
	}

	r.syncAlert()
	r.recheckLater()
}

// resolverName is who does the resolving, for what is said about it.
func (r *DnsRecord) resolverName() string {
	if r.deps.Resolver == "" {
		return "this machine"
	}

	return "the DNS server " + r.deps.Resolver
}

func describe(values []string) string {
	if len(values) == 0 {
		return "nothing"
	}

	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = fmt.Sprintf("%q", value)
	}

	return strings.Join(quoted, ", ")
}

// recheckLater asks the resolver again after a while.
func (r *DnsRecord) recheckLater() {
	r.stopRecheck()
	r.check.stopRecheck = r.deps.After(recheckEvery, func() {
		r.check.stopRecheck = nil
		r.startResolve()
	})
}

func (r *DnsRecord) stopRecheck() {
	if r.check.stopRecheck != nil {
		r.check.stopRecheck()
		r.check.stopRecheck = nil
	}
}

// syncAlert makes the record's one alert say what is wrong with it, or clears it if nothing is. It is only sent when it has changed.
func (r *DnsRecord) syncAlert() {
	var problems []string

	for _, problem := range []string{r.check.apiProblem, r.check.resolveProblem} {
		if problem != "" {
			problems = append(problems, problem)
		}
	}

	message := strings.Join(problems, "; ")
	r.currentData.Problem = message

	if message == "" {
		if r.check.alertRaised {
			if err := alert.Clear(r.deps.Alerts, r.deps.AlertSource, r.alertKey()); err != nil {
				r.log.Error("unable to clear the alert", "name", r.fqdn(), "error", err)
			}

			r.check.alertRaised, r.check.alertMessage = false, ""
		}

		return
	}

	if r.check.alertRaised && r.check.alertMessage == message {
		return
	}

	title := fmt.Sprintf("DNS record %s (%s) has an unexpected value", r.fqdn(), r.currentData.Type)

	if err := alert.Raise(r.deps.Alerts, r.deps.AlertSource, r.alertKey(), alert.SeverityWarning, title, message); err != nil {
		r.log.Error("unable to raise the alert", "name", r.fqdn(), "error", err)

		return
	}

	r.check.alertRaised, r.check.alertMessage = true, message
}

// alertKey is the key of the record's alert: there is one for each record.
func (r *DnsRecord) alertKey() string {
	return fmt.Sprintf("%s/%s/%s", r.domain, r.currentData.Type, r.currentData.Name)
}

// Force sends the value last asked for once more if Porkbun, or this machine's resolver, has anything else, to have it saved again. A
// record that has not been asked for a value, or has the one asked for everywhere, is only checked again. It says which it did.
func (r *DnsRecord) Force() (updated bool, err error) {
	wanted := r.check.requested

	differs := wanted != "" && (r.currentData.Value == "" || !resolve.Same(r.currentData.Type, r.currentData.Value, wanted) ||
		r.check.apiProblem != "" || r.currentData.ResolveChecked && !r.currentData.ResolveMatches || r.check.resolveProblem != "")

	if differs {
		return true, r.sendUpdate(wanted, true)
	}

	return false, r.Refresh()
}

// Close stops what the record is waiting for. Nothing more is done for it.
func (r *DnsRecord) Close() {
	r.check.closed = true
	r.stopRecheck()
}
