package common

import (
	"github.com/avanha/pmaas-plugin-porkbun/data"
)

type StatusAndEntities struct {
	Status     data.PluginStatus
	DnsRecords []data.DnsRecordData
}

type EntityStore interface {
	GetStatusAndEntities() (StatusAndEntities, error)
}
