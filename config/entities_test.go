package config

import (
	"errors"
	"testing"

	"github.com/avanha/pmaas-plugin-porkbun/data"
	"github.com/avanha/pmaas-plugin-porkbun/entities"
	"github.com/avanha/pmaas-plugin-porkbun/events"
	"github.com/avanha/pmaas-spi"
	spicommon "github.com/avanha/pmaas-spi/common"
)

type fakeStub struct {
	updates []string
	err     error
	data    data.DnsRecordData
}

func (s *fakeStub) Name() string               { return s.data.Name }
func (s *fakeStub) Data() data.DnsRecordData   { return s.data }
func (s *fakeStub) UpdateValue(v string) error { s.updates = append(s.updates, v); return s.err }

var _ entities.DnsRecord = (*fakeStub)(nil)

type fakeContainer struct {
	spi.IPMAASContainer
	batches [][]func()
}

func (c *fakeContainer) EnqueueOnServerGoRoutine(invocations []func()) error {
	c.batches = append(c.batches, invocations)
	return nil
}

func provide(t *testing.T, record *DnsRecord, stub entities.DnsRecord) *fakeContainer {
	t.Helper()

	container := &fakeContainer{}
	err := record.StubHolder().Provide(container, events.DnsRecordEntityStubAvailableEvent{EntityStub: stub})
	if err != nil {
		t.Fatal(err)
	}

	return container
}

func newRecord() *DnsRecord {
	return NewDomain("example.com").AddDnsRecord("A", "www")
}

func TestDomain_AddDnsRecordKeepsWhatItWasGiven(t *testing.T) {
	domain := NewDomain("example.com")
	record := domain.AddDnsRecord("AAAA", "nucd")

	if domain.DnsRecords["nucd_AAAA.example.com"] != record || record.Type != "AAAA" || record.Name != "nucd" {
		t.Errorf("records: %+v, record: %+v", domain.DnsRecords, record)
	}
}

func TestDnsRecord_UpdatingBeforeThePluginHasStartedFailsRatherThanPanics(t *testing.T) {
	err := newRecord().UpdateValue("1.2.3.4")

	if !errors.Is(err, spicommon.ErrStubNotAvailable) {
		t.Errorf("got %v", err)
	}
}

func TestDnsRecord_UpdatingForwardsToTheStubOnceItIsProvided(t *testing.T) {
	record := newRecord()
	stub := &fakeStub{}
	provide(t, record, stub)

	if err := record.UpdateValue("1.2.3.4"); err != nil {
		t.Fatal(err)
	}

	if len(stub.updates) != 1 || stub.updates[0] != "1.2.3.4" {
		t.Errorf("updates: %v", stub.updates)
	}

	stub.err = errors.New("rejected")

	if err := record.UpdateValue("5.6.7.8"); !errors.Is(err, stub.err) {
		t.Errorf("the stub's error wasn't returned: %v", err)
	}
}

func TestDnsRecord_UpdatingAfterTheStubIsWithdrawnFails(t *testing.T) {
	record := newRecord()
	provide(t, record, &fakeStub{})
	record.StubHolder().Withdraw()

	if err := record.UpdateValue("1.2.3.4"); !errors.Is(err, spicommon.ErrStubNotAvailable) {
		t.Errorf("got %v", err)
	}
}

func TestDnsRecord_EntityIsTheInterfaceAndAnswersWithWhatIsConfiguredUntilThereIsAStub(t *testing.T) {
	record := newRecord()
	var entity entities.DnsRecord = record.Entity()

	if entity.Name() != "www" {
		t.Errorf("name: %q", entity.Name())
	}

	if got := entity.Data(); got.Name != "www" || got.Type != "A" || got.Value != "" {
		t.Errorf("data before the stub: %+v", got)
	}

	provide(t, record, &fakeStub{data: data.DnsRecordData{Name: "www", Type: "A", Value: "9.9.9.9"}})

	if got := entity.Data(); got.Value != "9.9.9.9" {
		t.Errorf("data from the stub: %+v", got)
	}

	// The same entity value, taken before the stub existed, is the one that now works.
	if err := entity.UpdateValue("1.1.1.1"); err != nil {
		t.Errorf("update: %v", err)
	}
}

func TestDnsRecord_ListenersAddedWithTheOldEventTypeAreCalledWithTheStub(t *testing.T) {
	record := newRecord()

	var heard []events.DnsRecordEntityStubAvailableEvent
	record.AddOnEntityStubAvailableListener(func(event events.DnsRecordEntityStubAvailableEvent) { heard = append(heard, event) })

	stub := &fakeStub{}
	container := provide(t, record, stub)

	if len(heard) != 0 || len(container.batches) != 1 {
		t.Fatalf("heard %d, batches %d", len(heard), len(container.batches))
	}

	container.batches[0][0]()

	if len(heard) != 1 || heard[0].EntityStub != entities.DnsRecord(stub) {
		t.Errorf("heard %+v", heard)
	}
}
