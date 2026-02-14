package porkbun

import (
	"context"
	"fmt"
	"sync"
	"time"

	"pmaas.io/common/queue"
	"pmaas.io/plugins/porkbun/config"
	"pmaas.io/plugins/porkbun/data"
	"pmaas.io/plugins/porkbun/entities"
	"pmaas.io/plugins/porkbun/internal/common"
	"pmaas.io/plugins/porkbun/internal/dnsRecord"
	"pmaas.io/plugins/porkbun/internal/http"
	"pmaas.io/plugins/porkbun/internal/worker"
	"pmaas.io/spi"
)

type plugin struct {
	config               config.PluginConfig
	container            spi.IPMAASContainer
	entityCounter        int
	dnsRecords           map[string]*dnsRecord.DnsRecord
	requestCh            chan common.Request
	requestQueue         *queue.RequestQueue[common.Request]
	requestRetryingQueue *queue.RetryingRequestQueue[common.Request, common.DnsRecordResult]
	worker               *worker.Worker
	workersWg            sync.WaitGroup
	httpHandler          *http.Handler
	cancelFn             context.CancelFunc
	running              bool
}

type Plugin interface {
	spi.IPMAASPlugin2
}

func NewPlugin(config config.PluginConfig) Plugin {
	return &plugin{
		config:      config,
		dnsRecords:  make(map[string]*dnsRecord.DnsRecord),
		requestCh:   make(chan common.Request),
		httpHandler: http.NewHandler(),
	}
}

func (p *plugin) Init(container spi.IPMAASContainer) {
	p.container = container
	p.processConfig()
	p.httpHandler.Init(container, &entityStoreAdapter{parent: p})
	p.requestQueue = queue.NewRequestQueue(p.requestCh)
	p.requestRetryingQueue = queue.NewRetryingRequestQueue(
		getResultChannel,
		exchangeResultChannel,
		createErrorResponse,
		isFailedResult,
		canRetryRequest,
		p.requestQueue)
	p.worker = worker.NewPorkBunWorker(p.config.ApiKey, p.config.ApiSecret, p.requestCh)
}

func getResultChannel(request *common.Request) chan common.DnsRecordResult {
	return request.ResultCh
}

func exchangeResultChannel(
	request *common.Request, newChannel chan common.DnsRecordResult) chan common.DnsRecordResult {
	currentChannel := request.ResultCh
	request.ResultCh = newChannel
	return currentChannel
}

func createErrorResponse(err error) common.DnsRecordResult {
	return common.DnsRecordResult{Error: err}
}

func isFailedResult(result *common.DnsRecordResult) bool {
	return result.Error != nil
}

func canRetryRequest(_ *common.Request, _ *common.DnsRecordResult,
	attempts int, _ time.Time) bool {
	return attempts < 11
}

func (p *plugin) Start() {
	p.registerEntities()
	ctx, cancel := context.WithCancel(context.Background())
	p.cancelFn = cancel
	p.workersWg.Go(p.requestQueue.Run)
	p.workersWg.Go(p.requestRetryingQueue.Run)
	p.workersWg.Go(func() { p.worker.Run(ctx) })
	go func() { p.poll(ctx) }()
	p.running = true
}

func (p *plugin) Stop() {
	// Must use StopAsync
}

func (p *plugin) StopAsync() chan func() {
	fmt.Printf("%T Stopping...\n", p)
	p.running = false
	p.requestRetryingQueue.Stop()
	p.requestQueue.Stop()
	p.cancelFn()
	callbackCh := make(chan func())
	go func() {
		fmt.Printf("%T Waiting for workers to finish...\n", p)
		p.workersWg.Wait()
		callbackCh <- func() { p.onWorkersStopped(callbackCh) }
	}()

	return callbackCh
}

func (p *plugin) onWorkersStopped(callbackCh chan func()) {
	fmt.Printf("%T Workers stopped, deregistering entities...\n", p)
	p.deregisterEntities()
	close(callbackCh)
}

func (p *plugin) processConfig() {
	for _, configuredDomain := range p.config.Domains {
		for _, configuredDnsRecord := range configuredDomain.DnsRecords {
			key := fmt.Sprintf("%s_%s.%s",
				configuredDnsRecord.Name, configuredDomain.Name, configuredDnsRecord.Type)
			dnsRecordInstance := dnsRecord.NewDnsRecord(
				p.container,
				fmt.Sprintf("DnsRecord_%v", p.nextEntityId()),
				configuredDomain.Name,
				configuredDnsRecord.Type,
				configuredDnsRecord.Name,
				p.enqueueRequest,
				configuredDnsRecord.OnEntityStubAvailableListeners())
			p.dnsRecords[key] = dnsRecordInstance
		}
	}
}

func (p *plugin) registerEntities() {
	for key, record := range p.dnsRecords {
		pmassEntityId, err := p.container.RegisterEntity(record.Id(), entities.DnsRecordType, record.Name(), nil)

		if err != nil {
			fmt.Printf("Error registering %s: %v\n", key, err)
			continue
		}

		record.SetPmaasEntityId(pmassEntityId)
		record.ProcessConfiguredListeners(p.container)
	}
}

func (p *plugin) deregisterEntities() {
	for name, record := range p.dnsRecords {
		if record.PmaasEntityId() != "" {
			err := p.container.DeregisterEntity(record.PmaasEntityId())

			if err == nil {
				record.ClearPmaasEntityId()
			} else {
				fmt.Printf("Error deregistering DnsRecord %s: %v\n", name, err)
			}
		}

		record.CloseStubIfPresent()
	}
}

func (p *plugin) nextEntityId() int {
	p.entityCounter = p.entityCounter + 1
	return p.entityCounter
}

func (p *plugin) poll(ctx context.Context) {
	// Initial pause
	timer := time.NewTimer(20 * time.Second)

	select {
	case <-ctx.Done():
		timer.Stop()
		return
	case <-timer.C:
	}

	p.enqueueRefresh()

	// Refresh every 4 hours
	ticker := time.NewTicker(4 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.enqueueRefresh()
		}
	}
}

func (p *plugin) enqueueRefresh() {
	err := p.container.EnqueueOnPluginGoRoutine(func() {
		refreshError := p.refresh()

		if refreshError != nil {
			fmt.Printf("%T: Error enqueueing refresh: %v\n", p, refreshError)
		}
	})

	if err != nil {
		fmt.Printf("%T: Unable to enque refresh\n", p)
	}
}

func (p *plugin) refresh() error {
	if !p.running {
		return fmt.Errorf("plugin is not running")
	}

	errors := make([]error, 0)

	for _, record := range p.dnsRecords {
		err := record.Refresh()

		if err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("errors encountered during refresh: %v", errors)
	}

	return nil
}

// enqueueRequest sends a request to the plugin's worker(s) if the plugin is running.
// Returns an error if unable to add to the queue.  Must be called from the main plugin goroutine since it
// reads the running state of the plugin.
func (p *plugin) enqueueRequest(request common.Request) error {
	if !p.running {
		return fmt.Errorf("unable to enqueue request, plugin is not running")
	}

	err := p.requestRetryingQueue.Enqueue(&request)

	if err != nil {
		return fmt.Errorf("unable to enqueue request: %w", err)
	}

	return err
}

// getStatusAndEntities retrieves the plugin status and a list of currently registered trackable entities.
// It does not perform any synchronization, so it should only be called from the plugin's main GoRoutine.
func (p *plugin) getStatusAndEntities() common.StatusAndEntities {
	queStats := p.requestQueue.Stats()
	retryQueueStats := p.requestRetryingQueue.Stats()
	var totalSuccessCount, totalErrorCount int
	var lastError error
	var lastErrorMessage string
	var lastErrorTime time.Time

	dnsRecordDatas := make([]data.DnsRecordData, len(p.dnsRecords))
	i := 0

	for _, entity := range p.dnsRecords {
		entityData := entity.Data()
		dnsRecordDatas[i] = entityData
		totalSuccessCount = totalSuccessCount + entityData.GetSuccessCount + entityData.UpdateSuccessCount
		totalErrorCount = totalErrorCount + entityData.GetErrorCount + entityData.UpdateErrorCount

		if entityData.LastErrorTime.After(lastErrorTime) {
			lastErrorTime = entityData.LastErrorTime
			lastError = entityData.LastError
		}

		i++
	}

	if lastError != nil {
		lastErrorMessage = lastError.Error()
	}

	return common.StatusAndEntities{
		Status: data.PluginStatus{
			CurrentQueueSize:       queStats.CurrentCount,
			PeakQueueSize:          queStats.PeakCount,
			PeakQueueSizeTime:      queStats.PeakCountTime,
			CurrentRetryQueueSize:  retryQueueStats.CurrentCount,
			PeakRetryQueueSize:     retryQueueStats.PeakCount,
			PeakRetryQueueSizeTime: retryQueueStats.PeakCountTime,
			PeakFailedAttempts:     retryQueueStats.PeakFailedAttempts,
			PeakFailedAttemptsTime: retryQueueStats.PeakFailedAttemptsTime,
			TotalSuccessCount:      totalSuccessCount,
			TotalErrorCount:        totalErrorCount,
			LastErrorMessage:       lastErrorMessage,
			LastErrorTime:          lastErrorTime,
		},
		DnsRecords: dnsRecordDatas,
	}
}
