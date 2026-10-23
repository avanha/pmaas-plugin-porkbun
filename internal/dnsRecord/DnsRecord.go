package dnsRecord

import (
	"fmt"
	"github.com/avanha/pmaas-spi/logging"
	"log/slog"

	"github.com/avanha/pmaas-plugin-porkbun/data"
	"github.com/avanha/pmaas-plugin-porkbun/entities"
	"github.com/avanha/pmaas-plugin-porkbun/events"
	"github.com/avanha/pmaas-plugin-porkbun/internal/common"
	"github.com/avanha/pmaas-spi"
	spicommon "github.com/avanha/pmaas-spi/common"
	spievents "github.com/avanha/pmaas-spi/events"
)

type DnsRecord struct {
	log              *slog.Logger
	container        spi.IPMAASContainer
	id               string
	pmaasEntityId    string
	domain           string
	currentData      data.DnsRecordData
	stubHolder       *spicommon.StubHolder[entities.DnsRecord]
	stub             *DnsRecordStub
	requestHandlerFn func(request common.Request) error
	deps             Deps
	check            checkState
}

func NewDnsRecord(
	container spi.IPMAASContainer,
	id string,
	domain string,
	recordType string,
	name string,
	requestHandlerFn func(request common.Request) error,
	stubHolder *spicommon.StubHolder[entities.DnsRecord],
	deps Deps) *DnsRecord {
	return &DnsRecord{
		deps:      deps.withDefaults(container),
		log:       logging.For(container, "dnsrecord"),
		container: container,
		id:        id,
		domain:    domain,
		currentData: data.DnsRecordData{
			Name:     name,
			Type:     recordType,
			Resolver: deps.Resolver,
		},
		requestHandlerFn: requestHandlerFn,
		stubHolder:       stubHolder,
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
	r.log.Debug("received request to update DNS record to value", "name", r.currentData.Name, "value", value)

	return r.sendUpdate(value, false)
}

// sendUpdate has the record changed to the value, which is then what it is meant to be: it is checked against what Porkbun holds and
// what resolves. Force has it sent even if Porkbun says the record has the value already.
func (r *DnsRecord) sendUpdate(value string, force bool) error {
	resultCh := make(chan common.DnsRecordResult)
	request := common.Request{
		RequestType: common.RequestTypeUpdateDnsRecord,
		ResultCh:    resultCh,
		UpdateDnsRecordRequest: common.UpdateDnsRecordRequest{
			Domain:      r.domain,
			CurrentData: r.currentData,
			NewValue:    value,
			Force:       force,
		},
	}

	err := r.requestHandlerFn(request)

	if err != nil {
		return fmt.Errorf("failed to enqueue DNS record %s update: %v", r.currentData.Name, err)
	}

	r.check.requested = value
	r.currentData.RequestedValue = value
	r.check.pendingUpdates++

	go readAndProcessResult(r, resultCh, r.processUpdateValueResult, "update DNS record")

	return nil
}

func (r *DnsRecord) processUpdateValueResult(result common.DnsRecordResult) {
	r.check.pendingUpdates = max(r.check.pendingUpdates-1, 0)

	if result.Error == nil {
		r.log.Debug("updated DNS record successfully", "name", r.currentData.Name, "message", result.Message)
		r.updateData(&result.CurrentData)

		if result.CurrentData.LastModifiedTime.After(r.currentData.LastModifiedTime) {
			// Changed now: what resolves is meant to be different from before, and has until the TTL and a grace period to be. An alert
			// for what resolved before is over.
			r.restartResolveGrace()
		}

		r.currentData.LastModifiedTime = result.CurrentData.LastModifiedTime
		r.currentData.UpdateSuccessCount++
		r.check.apiProblem = ""
		r.afterRead()
	} else {
		r.log.Error("error updating DNS record", "name", r.currentData.Name, "error", result.Error)
		r.currentData.LastError = result.Error
		r.currentData.LastErrorTime = r.deps.Now()
		r.currentData.UpdateErrorCount++
		r.check.apiProblem = fmt.Sprintf("the value %q could not be saved at Porkbun: %v", r.check.requested, result.Error)
		r.syncAlert()
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

// ProcessConfiguredListeners hands the record's stub to the assembly: through the holder it shares with the
// assembly's config, which keeps it, and calls the listeners the assembly added. Call once the entity is registered.
func (r *DnsRecord) ProcessConfiguredListeners(container spi.IPMAASContainer) {
	event := events.DnsRecordEntityStubAvailableEvent{
		EntityEvent: spievents.EntityEvent{Id: r.pmaasEntityId, EntityType: entities.DnsRecordType, Name: r.currentData.Name},
		EntityStub:  r.GetStub(),
	}

	if err := r.stubHolder.Provide(container, event); err != nil {
		r.log.Error("error providing the stub of DNS record", "name", r.currentData.Name, "error", err)
	}
}

// GetStub returns a proxy struct that implements the DnsRecord interface.  The function ensures
// that only one stub is created for entity.  This function is not thread-safe but that is because
// it's only called from the plugin goroutine, whether direcftly or via the PMAAS server.
func (r *DnsRecord) GetStub() entities.DnsRecord {
	if r.stub == nil {
		r.stub = NewDnsRecordStub(r.log,
			r.id,
			&spicommon.ThreadSafeEntityWrapper[entities.DnsRecord]{
				Container: r.container,
				Entity:    r,
			})
	}

	return r.stub
}

func (r *DnsRecord) PmaasEntityId() string {
	return r.pmaasEntityId
}

func (r *DnsRecord) CloseStubIfPresent() {
	// Before closing it, so that nobody is handed a stub that's no use.
	r.stubHolder.Withdraw()

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
		r.log.Debug("DNS record", "name", result.CurrentData.Name, "message", result.Message)
		r.updateData(&result.CurrentData)
		r.currentData.GetSuccessCount++
		r.afterRead()
	} else {
		r.log.Error("error retrieving DNS record", "name", r.currentData.Name, "error", result.Error)
		r.currentData.LastError = result.Error
		r.currentData.LastErrorTime = r.deps.Now()
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
		r.log.Error("error processing result", "operation", resultDescription, "error", err)
	}
}
