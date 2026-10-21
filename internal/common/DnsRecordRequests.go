package common

import "github.com/avanha/pmaas-plugin-porkbun/data"

type DnsRecordResult struct {
	Error       error
	Message     string
	CurrentData data.DnsRecordData
}

type GetDnsRecordRequest struct {
	Domain string
	Type   string
	Name   string
}

type UpdateDnsRecordRequest struct {
	Domain      string
	CurrentData data.DnsRecordData
	NewValue    string

	// Force is that the update is sent although Porkbun already says the record has the value, to have it saved again.
	Force bool
}
