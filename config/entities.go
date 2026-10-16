package config

import (
	"fmt"

	"github.com/avanha/pmaas-plugin-porkbun/data"
	"github.com/avanha/pmaas-plugin-porkbun/entities"
	"github.com/avanha/pmaas-plugin-porkbun/events"
	spicommon "github.com/avanha/pmaas-spi/common"
)

type Domain struct {
	Name       string
	DnsRecords map[string]*DnsRecord
}

func NewDomain(name string) *Domain {
	return &Domain{
		Name:       name,
		DnsRecords: make(map[string]*DnsRecord),
	}
}

func (d *Domain) AddDnsRecord(recordType string, name string) *DnsRecord {
	key := fmt.Sprintf("%s_%s.%s", name, recordType, d.Name)
	dnsRecord := &DnsRecord{
		Type: recordType,
		Name: name,
	}
	d.DnsRecords[key] = dnsRecord

	return dnsRecord
}

// DnsRecord is the assembly's handle on a DNS record. It's usable from the moment it's configured: calls fail
// until the plugin has started and registered the record, and work from then on, without the assembly having to
// find the record's stub. Entity gives it as the entities.DnsRecord interface.
type DnsRecord struct {
	Type  string
	Name  string
	Value string

	stub spicommon.StubHolder[entities.DnsRecord]
}

// AddOnEntityStubAvailableListener adds a function to be called, on the server's goroutine, with the record's stub
// once it's available.
func (r *DnsRecord) AddOnEntityStubAvailableListener(eventListener func(event events.DnsRecordEntityStubAvailableEvent)) {
	r.stub.AddOnStubAvailableListener(eventListener)
}

// StubHolder is for the plugin, which provides the stub through it.
func (r *DnsRecord) StubHolder() *spicommon.StubHolder[entities.DnsRecord] {
	return &r.stub
}

// Entity is the record as the interface the plugin registers, forwarding every call to the plugin once there is a
// stub, and failing, or answering with what's configured, until then.
func (r *DnsRecord) Entity() entities.DnsRecord {
	return dnsRecordEntity{record: r}
}

func (r *DnsRecord) UpdateValue(value string) error {
	return r.Entity().UpdateValue(value)
}

// dnsRecordEntity forwards entities.DnsRecord to the stub in its record's holder.
type dnsRecordEntity struct {
	record *DnsRecord
}

var _ entities.DnsRecord = dnsRecordEntity{}

func (e dnsRecordEntity) Name() string {
	return e.record.Name
}

func (e dnsRecordEntity) Data() data.DnsRecordData {
	stub, err := e.record.stub.Stub()
	if err != nil {
		return data.DnsRecordData{Name: e.record.Name, Type: e.record.Type}
	}

	return stub.Data()
}

func (e dnsRecordEntity) UpdateValue(value string) error {
	stub, err := e.record.stub.Stub()
	if err != nil {
		return fmt.Errorf("unable to update DNS record %s %s: %w", e.record.Type, e.record.Name, err)
	}

	return stub.UpdateValue(value)
}
