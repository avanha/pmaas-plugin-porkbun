package dnsRecord

import (
	"fmt"
	"time"

	"github.com/avanha/pmaas-plugin-porkbun/data"
	"github.com/avanha/pmaas-plugin-porkbun/entities"
	"github.com/avanha/pmaas-plugin-porkbun/events"
	"github.com/avanha/pmaas-plugin-porkbun/internal/common"
	"github.com/avanha/pmaas-spi"
)

type DnsRecord struct {
	container                      spi.IPMAASContainer
	id                             string
	pmaasEntityId                  string
	domain                         string
	currentData                    data.DnsRecordData
	onEntityStubAvailableListeners []func(event events.DnsRecordEntityStubAvailableEvent)
	stub                           *DnsRecordStub
	requestHandlerFn               func(request common.Request) error
}

func NewDnsRecord(
	container spi.IPMAASContainer,
	id string,
	domain string,
	recordType string,
	name string,
	requestHandlerFn func(request common.Request) error,
	onEntityStubAvailableListeners []func(event events.DnsRecordEntityStubAvailableEvent)) *DnsRecord {
	return &DnsRecord{
		container: container,
		id:        id,
		domain:    domain,
		currentData: data.DnsRecordData{
			Name: name,
			Type: recordType,
		},
		requestHandlerFn:               requestHandlerFn,
		onEntityStubAvailableListeners: onEntityStubAvailableListeners,
	}
}

func (r *DnsRecord) Id() string {
	return r.id
}

func (r *DnsRecord) Name() string {
	return r.currentData.Name
}

func (r *DnsRecord) Data() data.DnsRecordData {
	return r.currentData
}

func (r *DnsRecord) UpdateValue(value string) error {
	fmt.Printf("Received request to update DNS record %s to value %s\n", r.currentData.Name, value)
	resultCh := make(chan common.DnsRecordResult)
	request := common.Request{
		RequestType: common.RequestTypeUpdateDnsRecord,
		ResultCh:    resultCh,
		UpdateDnsRecordRequest: common.UpdateDnsRecordRequest{
			Domain:      r.domain,
			CurrentData: r.currentData,
			NewValue:    value,
		},
	}

	err := r.requestHandlerFn(request)

	if err != nil {
		return fmt.Errorf("failed to enqueue DNS record %s update: %v", r.currentData.Name, err)
	}

	go readAndProcessResult(r, resultCh, r.processUpdateValueResult, "update DNS record")

	return nil
}

func (r *DnsRecord) processUpdateValueResult(result common.DnsRecordResult) {
	if result.Error == nil {
		fmt.Printf("Updated DNS record %s successfully: %s\n", r.currentData.Name, result.Message)
		r.updateData(&result.CurrentData)
		r.currentData.LastModifiedTime = result.CurrentData.LastModifiedTime
		r.currentData.UpdateSuccessCount++
	} else {
		fmt.Printf("Error updating DNS record %s: %v\n", r.currentData.Name, result.Error)
		r.currentData.LastError = result.Error
		r.currentData.LastErrorTime = time.Now()
		r.currentData.UpdateErrorCount++
	}
}

func (r *DnsRecord) ClearPmaasEntityId() {
	r.pmaasEntityId = ""
}

func (r *DnsRecord) SetPmaasEntityId(id string) {
	if r.pmaasEntityId != "" {
		panic(fmt.Errorf("DnsRecord %s already has pmass entity id %s", r.id, r.pmaasEntityId))
	}

	r.pmaasEntityId = id
}

func (r *DnsRecord) ProcessConfiguredListeners(container spi.IPMAASContainer) {
	r.processOnEntityStubAvailableListeners(container)
}

func (r *DnsRecord) processOnEntityStubAvailableListeners(container spi.IPMAASContainer) {
	numListeners := len(r.onEntityStubAvailableListeners)
	if numListeners == 0 {
		return
	}

	event := events.DnsRecordEntityStubAvailableEvent{EntityStub: r.GetStub(container)}
	invocations := make([]func(), numListeners)

	for i, listener := range r.onEntityStubAvailableListeners {
		invocations[i] = func() { listener(event) }
	}

	err := container.EnqueueOnServerGoRoutine(invocations)

	if err != nil {
		fmt.Printf("error enqueuing DnsRecordEntityStubAvailableEvent listener invocations: %v\n", err)
	}
}

func (r *DnsRecord) GetStub(container spi.IPMAASContainer) entities.DnsRecord {
	if r.stub == nil {
		r.stub = NewDnsRecordStub(
			r.id,
			&common.ThreadSafeEntityWrapper[entities.DnsRecord]{
				Container: container,
				Entity:    r,
			})
	}

	return r.stub
}

func (r *DnsRecord) PmaasEntityId() string {
	return r.pmaasEntityId
}

func (r *DnsRecord) CloseStubIfPresent() {
	if r.stub != nil {
		r.stub.Close()
		r.stub = nil
	}
}

func (r *DnsRecord) Refresh() error {
	resultCh := make(chan common.DnsRecordResult)
	request := common.Request{
		RequestType: common.RequestTypeGetDnsRecord,
		ResultCh:    resultCh,
		GetDnsRecordRequest: common.GetDnsRecordRequest{
			Domain: r.domain,
			Type:   r.currentData.Type,
			Name:   r.currentData.Name,
		},
	}

	err := r.requestHandlerFn(request)

	if err != nil {
		return fmt.Errorf("failed to enqueue DNS record %s retrieval: %v", r.currentData.Name, err)
	}

	go readAndProcessResult(r, resultCh, r.processGetDnsRecordResult, "DNS record retrieval")

	return nil
}

func (r *DnsRecord) processGetDnsRecordResult(result common.DnsRecordResult) {
	if result.Error == nil {
		fmt.Printf("%T DNS record %s: %s\n", r, result.CurrentData.Name, result.Message)
		r.updateData(&result.CurrentData)
		r.currentData.GetSuccessCount++
	} else {
		fmt.Printf("Error retrieving DNS record %s: %v\n", r.currentData.Name, result.Error)
		r.currentData.LastError = result.Error
		r.currentData.LastErrorTime = time.Now()
		r.currentData.GetErrorCount++
	}
}

func (r *DnsRecord) updateData(data *data.DnsRecordData) {
	r.currentData.LastUpdateTime = data.LastUpdateTime
	r.currentData.Value = data.Value
	r.currentData.Ttl = data.Ttl
	r.currentData.Priority = data.Priority
	r.currentData.Notes = data.Notes
	r.currentData.Type = data.Type
	r.currentData.Name = data.Name
}

func readAndProcessResult[T any](r *DnsRecord, resultCh <-chan T, processFn func(T), resultDescription string) {
	result := <-resultCh
	err := r.container.EnqueueOnPluginGoRoutine(func() { processFn(result) })

	if err != nil {
		fmt.Printf("%T Error processing %s result: %v\n", r, resultDescription, err)
	}
}
