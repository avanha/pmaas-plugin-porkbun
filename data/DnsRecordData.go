package data

import "time"

type DnsRecordData struct {
	Id                 string
	Name               string
	Type               string
	Value              string
	Ttl                int32
	Priority           int32
	Notes              string
	LastUpdateTime     time.Time
	LastModifiedTime   time.Time
	GetSuccessCount    int
	GetErrorCount      int
	UpdateSuccessCount int
	UpdateErrorCount   int
	LastError          error
	LastErrorTime      time.Time

	// RequestedValue is what the assembly last asked the record to be, or empty if it has not asked.
	RequestedValue string

	// Problem is what is wrong with the record now, which is also the message of its alert: Porkbun does not have the value that was asked
	// for, or this machine resolves the record to something else for longer than the record's TTL and a grace period. Empty if nothing is.
	Problem string

	// ResolveSupported is whether the record's type can be checked against this machine's resolver, and ResolveChecked whether it has
	// been. ResolvedValues is what the resolver answered at ResolveTime, ResolveMatches whether the record's value is among them, and
	// ResolveError why the resolver could not say, if it could not.
	ResolveSupported bool
	ResolveChecked   bool
	ResolveTime      time.Time
	ResolvedValues   []string
	ResolveMatches   bool
	ResolveError     string
}
