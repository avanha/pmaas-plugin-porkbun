package events

import (
	"github.com/avanha/pmaas-plugin-porkbun/entities"
	"github.com/avanha/pmaas-spi/events"
)

type DnsRecordEntityStubAvailableEvent struct {
	events.EntityEvent
	EntityStub entities.DnsRecord
}
