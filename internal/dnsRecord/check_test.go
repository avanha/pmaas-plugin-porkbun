package dnsRecord

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/avanha/pmaas-plugin-porkbun/data"
	"github.com/avanha/pmaas-plugin-porkbun/entities"
	"github.com/avanha/pmaas-plugin-porkbun/internal/common"
	"github.com/avanha/pmaas-spi"
	"github.com/avanha/pmaas-spi/alert"
	spicommon "github.com/avanha/pmaas-spi/common"
)

type timer struct {
	at      time.Time
	f       func()
	stopped bool
}

// harness is a record with a clock, a resolver, a queue for the plugin's goroutine, timers and an alert console that the test runs.
type harness struct {
	t        *testing.T
	record   *DnsRecord
	now      time.Time
	queue    []func()
	timers   []*timer
	requests []common.Request
	events   []any
	lookups  int
	resolve  func() ([]string, error)
}

type harnessContainer struct {
	spi.IPMAASContainer
	h *harness
}

func (c *harnessContainer) EnqueueOnPluginGoRoutine(f func()) error {
	c.h.queue = append(c.h.queue, f)

	return nil
}

func (h *harness) BroadcastEvent(_ string, event any) error {
	h.events = append(h.events, event)

	return nil
}

func newHarness(t *testing.T, recordType string) *harness {
	t.Helper()

	h := &harness{t: t, now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	h.resolve = func() ([]string, error) { return nil, nil }
	container := &harnessContainer{h: h}

	var holder spicommon.StubHolder[entities.DnsRecord]

	h.record = NewDnsRecord(container, "DnsRecord_1", "example.com", recordType, "www",
		func(request common.Request) error { h.requests = append(h.requests, request); return nil }, &holder,
		Deps{
			Lookup:  func(context.Context, string, string) ([]string, error) { h.lookups++; return h.resolve() },
			Now:     func() time.Time { return h.now },
			Go:      func(f func()) { f() },
			Enqueue: container.EnqueueOnPluginGoRoutine,
			After: func(d time.Duration, f func()) func() {
				timer := &timer{at: h.now.Add(d), f: f}
				h.timers = append(h.timers, timer)

				return func() { timer.stopped = true }
			},
			Alerts:      h,
			AlertSource: "porkbun",
		})
	h.record.currentData.Ttl = 600

	return h
}

// run does what has been queued for the plugin's goroutine.
func (h *harness) run() {
	for len(h.queue) > 0 {
		f := h.queue[0]
		h.queue = h.queue[1:]
		f()
	}
}

// advance moves the clock a minute at a time, firing the timers that are due, as they would be.
func (h *harness) advance(d time.Duration) {
	for remaining := d; remaining > 0; remaining -= time.Minute {
		h.now = h.now.Add(min(remaining, time.Minute))

		for _, timer := range h.timers {
			if !timer.stopped && !timer.at.After(h.now) {
				timer.stopped = true
				timer.f()
			}
		}

		h.run()
	}
}

// read is the answer to a read of the record from Porkbun.
func (h *harness) read(value string) {
	h.record.processGetDnsRecordResult(common.DnsRecordResult{CurrentData: data.DnsRecordData{Name: "www", Type: h.record.currentData.Type, Value: value, Ttl: 600, LastUpdateTime: h.now}})
	h.run()
}

// updated is the answer to a request to update the record, as Porkbun saved it.
func (h *harness) updated(value string, err error) {
	result := common.DnsRecordResult{Error: err}

	if err == nil {
		result.CurrentData = data.DnsRecordData{Name: "www", Type: h.record.currentData.Type, Value: value, Ttl: 600, LastUpdateTime: h.now, LastModifiedTime: h.now}
	}

	h.record.processUpdateValueResult(result)
	h.run()
}

func (h *harness) raised() []alert.RaisedEvent {
	var raised []alert.RaisedEvent

	for _, event := range h.events {
		if r, ok := event.(alert.RaisedEvent); ok {
			raised = append(raised, r)
		}
	}

	return raised
}

func (h *harness) cleared() int {
	count := 0

	for _, event := range h.events {
		if _, ok := event.(alert.ClearedEvent); ok {
			count++
		}
	}

	return count
}

func TestCheck_ARecordThatResolvesToItsValueHasNoProblem(t *testing.T) {
	h := newHarness(t, "A")
	h.resolve = func() ([]string, error) { return []string{"10.0.0.1"}, nil }

	h.read("10.0.0.1")

	d := h.record.Data()
	if !d.ResolveChecked || !d.ResolveMatches || d.Problem != "" || len(h.events) != 0 || d.ResolvedValues[0] != "10.0.0.1" {
		t.Errorf("%+v %v", d, h.events)
	}
}

func TestCheck_ARecordThatPorkbunDoesNotHoldWhatWasAskedForIsAProblemAtOnce(t *testing.T) {
	h := newHarness(t, "A")
	h.resolve = func() ([]string, error) { return []string{"10.0.0.1"}, nil }

	if err := h.record.UpdateValue("10.0.0.2"); err != nil {
		t.Fatal(err)
	}

	// A read that comes while the update is on its way, with the earlier value, says nothing.
	h.read("10.0.0.1")

	if len(h.raised()) != 0 {
		t.Fatalf("an alert while an update was on its way: %v", h.events)
	}

	// The update fails for good: the alert says so.
	h.updated("", errors.New("porkbun: the update was not saved"))

	raised := h.raised()
	if len(raised) != 1 || raised[0].Severity != alert.SeverityWarning || raised[0].Key != "example.com/A/www" || raised[0].Source != "porkbun" {
		t.Fatalf("%+v", h.events)
	}

	// A later read that still has the earlier value is the same problem, and the alert is not sent again.
	h.read("10.0.0.1")

	if len(h.raised()) != 2 {
		// The message says what Porkbun holds now, and so differs from the one for the failed update.
		t.Fatalf("%+v", h.events)
	}

	// Put right, by an update that took.
	h.updated("10.0.0.2", nil)
	h.resolve = func() ([]string, error) { return []string{"10.0.0.2"}, nil }
	h.read("10.0.0.2")

	if h.cleared() != 1 || h.record.Data().Problem != "" {
		t.Errorf("%+v", h.events)
	}
}

func TestCheck_WhatResolvesToSomethingElseIsOnlyAProblemOnceTheTtlAndFiveMinutesHavePassed(t *testing.T) {
	h := newHarness(t, "A")
	h.resolve = func() ([]string, error) { return []string{"10.0.0.1"}, nil }

	_ = h.record.UpdateValue("10.0.0.2")
	h.updated("10.0.0.2", nil)

	d := h.record.Data()
	if d.ResolveMatches || !d.ResolveChecked || d.Problem != "" || len(h.events) != 0 {
		t.Fatalf("a problem within the TTL: %+v %v", d, h.events)
	}

	// Ten minutes (the TTL) and then some: still within the five minutes.
	h.advance(10*time.Minute + 4*time.Minute)

	if len(h.events) != 0 {
		t.Fatalf("an alert within the grace: %v", h.events)
	}

	h.advance(2 * time.Minute)

	raised := h.raised()
	if len(raised) != 1 || h.record.Data().Problem == "" {
		t.Fatalf("no alert once the TTL and grace were up: %+v", h.events)
	}

	// And the resolver catches up: it is checked again while it differs, and the alert clears.
	h.resolve = func() ([]string, error) { return []string{"10.0.0.2"}, nil }
	h.advance(recheckEvery)

	if h.cleared() != 1 || h.record.Data().Problem != "" || !h.record.Data().ResolveMatches {
		t.Errorf("%+v", h.events)
	}

	before := h.lookups
	h.advance(time.Hour)

	if h.lookups != before {
		t.Error("the resolver was asked again though it matched")
	}
}

func TestCheck_ARecordNobodySetIsGivenItsTtlAndGraceFromWhenItFirstResolvedWrong(t *testing.T) {
	h := newHarness(t, "A")
	h.resolve = func() ([]string, error) { return []string{"10.0.0.1"}, nil }

	h.read("10.0.0.9")
	h.advance(14 * time.Minute)

	if len(h.events) != 0 {
		t.Fatalf("%v", h.events)
	}

	h.advance(time.Minute)

	if len(h.raised()) != 1 {
		t.Fatalf("%v", h.events)
	}
}

func TestCheck_ForcingAnUpdateStartsTheGraceAgainAndClearsTheAlert(t *testing.T) {
	h := newHarness(t, "A")
	h.resolve = func() ([]string, error) { return []string{"10.0.0.1"}, nil }

	_ = h.record.UpdateValue("10.0.0.2")
	h.updated("10.0.0.2", nil)
	h.advance(16 * time.Minute)

	if len(h.raised()) != 1 {
		t.Fatalf("%v", h.events)
	}

	// The resolver still has the old value after a forced update, but is given the TTL and grace from now.
	h.updated("10.0.0.2", nil)

	if h.cleared() != 1 || h.record.Data().Problem != "" {
		t.Fatalf("%+v", h.events)
	}

	h.advance(14 * time.Minute)

	if len(h.raised()) != 1 {
		t.Fatalf("raised again within the new grace: %v", h.events)
	}
}

func TestCheck_NoAnswerIsAMismatchButAResolverThatCouldNotSayIsNot(t *testing.T) {
	h := newHarness(t, "A")
	h.resolve = func() ([]string, error) { return nil, &net.DNSError{Err: "no such host", IsNotFound: true} }
	h.read("10.0.0.1")

	d := h.record.Data()
	if !d.ResolveChecked || d.ResolveMatches || d.ResolveError != "" {
		t.Fatalf("%+v", d)
	}

	h = newHarness(t, "A")
	h.resolve = func() ([]string, error) { return nil, errors.New("i/o timeout") }
	h.read("10.0.0.1")
	h.advance(time.Hour)

	d = h.record.Data()
	if d.ResolveChecked || d.ResolveError == "" || d.Problem != "" || len(h.events) != 0 {
		t.Errorf("%+v %v", d, h.events)
	}
}

func TestCheck_ATypeTheResolverDoesNotAnswerSimplyIsNotLookedUp(t *testing.T) {
	h := newHarness(t, "MX")
	h.read("mail.example.com")

	if h.lookups != 0 || h.record.Data().ResolveSupported {
		t.Errorf("%d lookups, %+v", h.lookups, h.record.Data())
	}
}

func TestCheck_AnAnswerToAnEarlierLookupIsDropped(t *testing.T) {
	h := newHarness(t, "A")
	h.record.currentData.Value = "10.0.0.1"
	h.record.check.generation = 5

	h.record.onResolved(4, "10.0.0.1", []string{"10.9.9.9"}, nil)

	if h.record.Data().ResolveChecked {
		t.Error("an old answer was taken in")
	}
}

func TestForce_SendsTheRequestedValueAgainWhereTheresAProblemAndOtherwiseOnlyChecks(t *testing.T) {
	h := newHarness(t, "A")
	h.resolve = func() ([]string, error) { return []string{"10.0.0.1"}, nil }

	// Nothing was ever asked for: a read.
	if updated, err := h.record.Force(); err != nil || updated || len(h.requests) != 1 || h.requests[0].RequestType != common.RequestTypeGetDnsRecord {
		t.Fatalf("%v %v %+v", updated, err, h.requests)
	}

	// Asked for a value Porkbun holds and the resolver gives: a read.
	_ = h.record.UpdateValue("10.0.0.1")
	h.updated("10.0.0.1", nil)
	h.requests = nil

	if updated, _ := h.record.Force(); updated || h.requests[0].RequestType != common.RequestTypeGetDnsRecord {
		t.Fatalf("%+v", h.requests)
	}

	// The resolver differs, the problem only after the grace: the value is sent again, forced although Porkbun says it has it.
	h.record.check.requested = "10.0.0.2"
	h.record.currentData.Value = "10.0.0.2"
	h.record.currentData.ResolveChecked, h.record.currentData.ResolveMatches = true, false
	h.requests = nil

	updated, err := h.record.Force()
	if err != nil || !updated || len(h.requests) != 1 || h.requests[0].RequestType != common.RequestTypeUpdateDnsRecord ||
		!h.requests[0].UpdateDnsRecordRequest.Force || h.requests[0].UpdateDnsRecordRequest.NewValue != "10.0.0.2" {
		t.Fatalf("%v %v %+v", updated, err, h.requests)
	}
}

func TestClose_StopsTheRecheckAndDropsLateAnswers(t *testing.T) {
	h := newHarness(t, "A")
	h.resolve = func() ([]string, error) { return []string{"10.0.0.1"}, nil }
	h.read("10.0.0.9")

	if len(h.timers) == 0 {
		t.Fatal("no recheck waiting")
	}

	h.record.Close()
	before := h.lookups
	h.advance(time.Hour)

	if h.lookups != before {
		t.Error("looked up after it was closed")
	}

	h.record.onResolved(h.record.check.generation, "10.0.0.9", nil, nil)

	if h.record.Data().ResolveChecked && !h.record.Data().ResolveMatches && len(h.raised()) > 0 {
		t.Error("took in an answer after it was closed")
	}
}

func TestCheck_TheAlertSaysWhoResolvedAndIsTitledAsHavingAnUnexpectedValue(t *testing.T) {
	h := newHarness(t, "A")
	h.record.deps.Resolver = "1.1.1.1"
	h.resolve = func() ([]string, error) { return []string{"10.0.0.1"}, nil }

	h.read("10.0.0.9")
	h.advance(16 * time.Minute)

	raised := h.raised()
	if len(raised) != 1 || !strings.Contains(raised[0].Message, "the DNS server 1.1.1.1 resolves www.example.com") ||
		!strings.HasSuffix(raised[0].Title, "has an unexpected value") {
		t.Fatalf("%+v", raised)
	}

	h = newHarness(t, "A")
	h.resolve = func() ([]string, error) { return []string{"10.0.0.1"}, nil }
	h.read("10.0.0.9")
	h.advance(16 * time.Minute)

	if raised := h.raised(); len(raised) != 1 || !strings.Contains(raised[0].Message, "this machine resolves") {
		t.Errorf("%+v", raised)
	}
}

func TestCheck_ALookupThatFailsLeavesWhatWasLastSeenAsItWas(t *testing.T) {
	h := newHarness(t, "A")
	h.resolve = func() ([]string, error) { return []string{"10.0.0.9"}, nil }
	h.read("10.0.0.9")
	seen := h.record.Data().ResolveTime

	h.resolve = func() ([]string, error) { return nil, errors.New("i/o timeout") }
	h.advance(recheckEvery)
	h.read("10.0.0.9")

	d := h.record.Data()
	if !d.ResolveTime.Equal(seen) || len(d.ResolvedValues) != 1 || !d.ResolveMatches || d.ResolveError == "" {
		t.Errorf("%+v", d)
	}
}
