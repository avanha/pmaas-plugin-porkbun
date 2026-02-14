package common

import (
	"pmaas.io/plugins/porkbun/data"
)

type StatusAndEntities struct {
	Status     data.PluginStatus
	DnsRecords []data.DnsRecordData
}

type EntityStore interface {
	GetStatusAndEntities() (StatusAndEntities, error)
}
