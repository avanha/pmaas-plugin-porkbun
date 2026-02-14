package events

import (
	"pmaas.io/plugins/porkbun/entities"
	"pmaas.io/spi/events"
)

type DnsRecordEntityStubAvailableEvent struct {
	events.EntityEvent
	EntityStub entities.DnsRecord
}
