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
}
