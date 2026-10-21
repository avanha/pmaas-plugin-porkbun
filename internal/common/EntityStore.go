package common

import (
	"github.com/avanha/pmaas-plugin-porkbun/data"
)

type StatusAndEntities struct {
	Status     data.PluginStatus
	DnsRecords []data.DnsRecordData
}

// ForceResult is what forcing an update did: how many records were sent their value again, how many were only checked again, and
// what could not be done.
type ForceResult struct {
	Updated int
	Checked int
	Failed  []string
}

type EntityStore interface {
	GetStatusAndEntities() (StatusAndEntities, error)

	// ForceUpdate checks every record again, and sends the value last asked for again to those that don't have it.
	ForceUpdate() (ForceResult, error)
}
