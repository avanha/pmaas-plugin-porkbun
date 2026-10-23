package porkbun

import (
	"context"
	"errors"
	"fmt"
	"github.com/avanha/pmaas-spi/logging"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/avanha/pmaas-common/queue"
	"github.com/avanha/pmaas-plugin-porkbun/config"
	"github.com/avanha/pmaas-plugin-porkbun/data"
	"github.com/avanha/pmaas-plugin-porkbun/entities"
	"github.com/avanha/pmaas-plugin-porkbun/internal/common"
	"github.com/avanha/pmaas-plugin-porkbun/internal/dnsRecord"
	"github.com/avanha/pmaas-plugin-porkbun/internal/http"
	"github.com/avanha/pmaas-plugin-porkbun/internal/resolve"
	"github.com/avanha/pmaas-plugin-porkbun/internal/worker"
	"github.com/avanha/pmaas-spi"
)

func NewPluginConfig() config.PluginConfig {
	return config.PluginConfig{
		Domains: make(map[string]*config.Domain),
	}
}

type plugin struct {
	log                  *slog.Logger
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
	spi.IPMAASPlugin
}

func NewPlugin(config config.PluginConfig) Plugin {
	return &plugin{
		config:      config,
		dnsRecords:  make(map[string]*dnsRecord.DnsRecord),
		requestCh:   make(chan common.Request),
		httpHandler: http.NewHandler(),
	}
}

func (p *plugin) ShortName() string {
	return "porkbun"
}

func (p *plugin) Init(container spi.IPMAASContainer) {
	p.container = container
	p.log = logging.For(container)
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
	p.worker = worker.NewPorkBunWorker(logging.For(container, "worker"), p.config.ApiKey, p.config.ApiSecret, p.requestCh)
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

func canRetryRequest(_ *common.Request, response *common.DnsRecordResult,
	attempts int, _ time.Time) bool {
	// A rejected API key/secret is a permanent failure: retrying with the same credentials will
	// never succeed, so surface it immediately instead of masking it behind the retry loop.
	if response != nil && errors.Is(response.Error, worker.ErrInvalidCredentials) {
		return false
	}

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

func (p *plugin) Stop() chan func() {
	p.log.Info("stopping")
	p.running = false

	for _, record := range p.dnsRecords {
		record.Close()
	}

	p.requestRetryingQueue.Stop()
	p.requestQueue.Stop()
	p.cancelFn()
	callbackCh := make(chan func())
	go func() {
		p.log.Debug("waiting for workers to finish")
		p.workersWg.Wait()
		callbackCh <- func() { p.onWorkersStopped(callbackCh) }
	}()

	return callbackCh
}

func (p *plugin) onWorkersStopped(callbackCh chan func()) {
	p.log.Debug("workers stopped, deregistering entities")
	p.deregisterEntities()
	close(callbackCh)
}

func (p *plugin) processConfig() {
	for _, configuredDomain := range p.config.Domains {
		for _, configuredDnsRecord := range configuredDomain.DnsRecords {
			key := fmt.Sprintf("%s_%s.%s",
				configuredDnsRecord.Name, configuredDomain.Name, configuredDnsRecord.Type)
			resolver := p.config.Resolver
			if configuredDnsRecord.Resolver != "" {
				resolver = configuredDnsRecord.Resolver
			}

			dnsRecordInstance := dnsRecord.NewDnsRecord(
				p.container,
				fmt.Sprintf("DnsRecord_%v", p.nextEntityId()),
				configuredDomain.Name,
				configuredDnsRecord.Type,
				configuredDnsRecord.Name,
				p.enqueueRequest,
				configuredDnsRecord.StubHolder(),
				dnsRecord.Deps{AlertSource: p.ShortName(), Lookup: resolve.Server(resolver), Resolver: resolver})
			p.dnsRecords[key] = dnsRecordInstance
		}
	}
}

func (p *plugin) registerEntities() {
	for key, record := range p.dnsRecords {
		// This lambda captures the plugin instance and the hostInstance
		// and passes it to the entity manager.  However, entities are deregistered on plugin
		// stop, so this will not leak resources, and is OK,
		var recordStubFactoryFn spi.EntityStubFactoryFunc = func() (any, error) {
			return record.GetStub(), nil
		}
		pmassEntityId, err := p.container.RegisterEntity(
			record.Id(), entities.DnsRecordType, record.Name(), recordStubFactoryFn)

		if err != nil {
			p.log.Error("error registering", "key", key, "error", err)
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
				p.log.Error("error deregistering DnsRecord", "name", name, "error", err)
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
			p.log.Error("error enqueueing refresh", "error", refreshError)
		}
	})

	if err != nil {
		p.log.Error("unable to enqueue refresh")
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

// forceUpdate checks every record again, and sends the value last asked for again to those that have anything else, at Porkbun or
// where this machine's resolver looks. Must be called from the main plugin goroutine.
func (p *plugin) forceUpdate() (common.ForceResult, error) {
	if !p.running {
		return common.ForceResult{}, fmt.Errorf("plugin is not running")
	}

	var result common.ForceResult

	for key, record := range p.dnsRecords {
		updated, err := record.Force()

		switch {
		case err != nil:
			result.Failed = append(result.Failed, fmt.Sprintf("%s: %v", key, err))
		case updated:
			result.Updated++
		default:
			result.Checked++
		}
	}

	sort.Strings(result.Failed)
	p.log.Info("forced an update", "updated", result.Updated, "checked", result.Checked, "failed", len(result.Failed))

	return result, nil
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
