package config

import (
	"fmt"
	"slices"

	"pmaas.io/plugins/porkbun/entities"
	"pmaas.io/plugins/porkbun/events"
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
		Type:                           recordType,
		Name:                           name,
		onEntityStubAvailableListeners: make([]func(event events.DnsRecordEntityStubAvailableEvent), 0),
	}
	dnsRecord.AddOnEntityStubAvailableListener(dnsRecord.onEntityStubAvailable)
	d.DnsRecords[key] = dnsRecord

	return dnsRecord
}

type DnsRecord struct {
	Type                           string
	Name                           string
	Value                          string
	entityStub                     entities.DnsRecord
	onEntityStubAvailableListeners []func(event events.DnsRecordEntityStubAvailableEvent)
}

func (r *DnsRecord) AddOnEntityStubAvailableListener(eventListener func(event events.DnsRecordEntityStubAvailableEvent)) {
	r.onEntityStubAvailableListeners = append(r.onEntityStubAvailableListeners, eventListener)
}

func (r *DnsRecord) UpdateValue(value string) error {
	if r.entityStub == nil {
		return fmt.Errorf("unable to update DNS record %s %s: entity stub is not available", r.Type, r.Name)
	}

	return r.entityStub.UpdateValue(value)
}

func (r *DnsRecord) OnEntityStubAvailableListeners() []func(event events.DnsRecordEntityStubAvailableEvent) {
	return slices.Clone(r.onEntityStubAvailableListeners)
}

func (r *DnsRecord) onEntityStubAvailable(event events.DnsRecordEntityStubAvailableEvent) {
	r.entityStub = event.EntityStub
}
