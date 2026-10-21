package dnsRecord

import (
	"errors"
	"testing"

	"github.com/avanha/pmaas-plugin-porkbun/entities"
	"github.com/avanha/pmaas-plugin-porkbun/internal/common"
	"github.com/avanha/pmaas-spi"
	spicommon "github.com/avanha/pmaas-spi/common"
)

type fakeContainer struct {
	spi.IPMAASContainer
	batches [][]func()
}

func (c *fakeContainer) EnqueueOnServerGoRoutine(invocations []func()) error {
	c.batches = append(c.batches, invocations)
	return nil
}

func newRecord(holder *spicommon.StubHolder[entities.DnsRecord]) (*DnsRecord, *fakeContainer) {
	container := &fakeContainer{}
	record := NewDnsRecord(container, "DnsRecord_1", "example.com", "A", "www",
		func(common.Request) error { return nil }, holder, Deps{})

	return record, container
}

func TestProcessConfiguredListeners_ProvidesTheStubAndTellsTheListenersWhatItIs(t *testing.T) {
	var holder spicommon.StubHolder[entities.DnsRecord]
	record, container := newRecord(&holder)
	record.SetPmaasEntityId("pmaas-id")

	var heard []spicommon.StubAvailableEvent[entities.DnsRecord]
	holder.AddOnStubAvailableListener(func(event spicommon.StubAvailableEvent[entities.DnsRecord]) { heard = append(heard, event) })

	record.ProcessConfiguredListeners(container)

	if stub, err := holder.Stub(); err != nil || stub != record.GetStub() {
		t.Fatalf("the holder's stub: %v, %v", stub, err)
	}

	container.batches[0][0]()

	if len(heard) != 1 {
		t.Fatalf("heard %d", len(heard))
	}

	event := heard[0]
	if event.Id != "pmaas-id" || event.EntityType != entities.DnsRecordType || event.Name != "www" || event.EntityStub != record.GetStub() {
		t.Errorf("event: %+v", event)
	}
}

func TestCloseStubIfPresent_WithdrawsTheStubSoNobodyCallsOneThatPanics(t *testing.T) {
	var holder spicommon.StubHolder[entities.DnsRecord]
	record, container := newRecord(&holder)
	record.ProcessConfiguredListeners(container)

	record.CloseStubIfPresent()

	if _, err := holder.Stub(); !errors.Is(err, spicommon.ErrStubNotAvailable) {
		t.Errorf("got %v", err)
	}
}

func TestCloseStubIfPresent_WithdrawsEvenIfThereWasNeverAStub(t *testing.T) {
	var holder spicommon.StubHolder[entities.DnsRecord]
	record, _ := newRecord(&holder)

	record.CloseStubIfPresent() // mustn't panic
}
