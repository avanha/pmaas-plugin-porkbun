package events

import (
	"github.com/avanha/pmaas-plugin-porkbun/entities"
	spicommon "github.com/avanha/pmaas-spi/common"
)

// DnsRecordEntityStubAvailableEvent is how the assembly is handed a DNS record's stub, once the plugin has
// registered the record. It has the entity's Id, EntityType and Name, and the stub as EntityStub.
type DnsRecordEntityStubAvailableEvent = spicommon.StubAvailableEvent[entities.DnsRecord]
