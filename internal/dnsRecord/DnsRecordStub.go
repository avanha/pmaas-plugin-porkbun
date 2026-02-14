package dnsRecord

import (
	"fmt"
	"sync/atomic"

	"github.com/avanha/pmaas-plugin-porkbun/data"
	"github.com/avanha/pmaas-plugin-porkbun/entities"
	spicommon "github.com/avanha/pmaas-spi/common"
)

type DnsRecordStub struct {
	pmaasEntityId          string
	closeFn                func() error
	entityWrapperReference atomic.Pointer[spicommon.ThreadSafeEntityWrapper[entities.DnsRecord]]
}

func NewDnsRecordStub(pmaasEntityId string, entityWrapper *spicommon.ThreadSafeEntityWrapper[entities.DnsRecord]) *DnsRecordStub {
	stub := &DnsRecordStub{
		pmaasEntityId: pmaasEntityId,
	}

	stub.entityWrapperReference.Store(entityWrapper)

	stub.closeFn = func() error {
		if stub.entityWrapperReference.CompareAndSwap(entityWrapper, nil) {
			stub.closeFn = nil
			return nil
		}

		return fmt.Errorf("failed to clear entity wrapper, current value does not match expected value")
	}

	return stub
}

func (s *DnsRecordStub) Data() data.DnsRecordData {
	return spicommon.ThreadSafeEntityWrapperExecValueFunc(
		s.entityWrapperReference.Load(),
		func(target entities.DnsRecord) data.DnsRecordData { return target.Data() })
}

func (s *DnsRecordStub) Name() string {
	return spicommon.ThreadSafeEntityWrapperExecValueFunc(
		s.entityWrapperReference.Load(),
		func(target entities.DnsRecord) string { return target.Name() })
}

func (s *DnsRecordStub) UpdateValue(value string) error {
	return spicommon.ThreadSafeEntityWrapperExecValueFunc(
		s.entityWrapperReference.Load(),
		func(target entities.DnsRecord) error { return target.UpdateValue(value) })
}

func (s *DnsRecordStub) Close() {
	closeFn := s.closeFn

	if closeFn == nil {
		return
	}

	err := closeFn()

	if err != nil {
		fmt.Printf("Failed to close DnsRecordStub %s: %v", s.Name(), err)
	}
}
