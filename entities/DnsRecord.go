package entities

import (
	"reflect"

	"github.com/avanha/pmaas-plugin-porkbun/data"
)

type DnsRecord interface {
	Name() string
	UpdateValue(value string) error
	Data() data.DnsRecordData
}

var DnsRecordType = reflect.TypeOf((*DnsRecord)(nil)).Elem()
