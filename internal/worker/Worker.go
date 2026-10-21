package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/avanha/pmaas-plugin-porkbun/data"
	"github.com/avanha/pmaas-plugin-porkbun/internal/common"
	"github.com/avanha/pmaas-plugin-porkbun/internal/resolve"
	spicommon "github.com/avanha/pmaas-spi/common"
)

type Worker struct {
	log             *slog.Logger
	ApiKey          string
	ApiSecret       string
	credentialsBody []byte
	httpClient      spicommon.HttpClient
	requestCh       chan common.Request
	err             atomic.Value
}

// ErrInvalidCredentials indicates the Porkbun API rejected the configured API key/secret.
// Porkbun always responds with HTTP 200 and a JSON body ({"status":"ERROR", ...}) for API-level
// failures, so this is detected from the response message rather than the HTTP status code.
// This is a permanent, non-retryable condition: retrying with the same credentials will never
// succeed, unlike transient errors (network failures, rate limiting, 5xx-equivalents).
var ErrInvalidCredentials = errors.New("porkbun: invalid API key/secret")

// ErrNotSaved is that Porkbun accepted an update of a record and, asked for the record afterwards, still has the old value.
var ErrNotSaved = errors.New("porkbun: the update was not saved")

// isInvalidCredentialsMessage checks a Porkbun error message for the known text it returns
// when the configured API key or secret API key is invalid.
func isInvalidCredentialsMessage(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "invalid api key") || strings.Contains(lower, "invalid secret api key")
}

// wrapStatusError builds an error from a non-SUCCESS Porkbun API response, wrapping
// ErrInvalidCredentials when the message indicates the API key/secret was rejected so callers
// can distinguish this permanent failure from transient ones via errors.Is.
func wrapStatusError(prefix string, status string, message string) error {
	err := fmt.Errorf("%s, status: %s, message: %s", prefix, status, message)

	if isInvalidCredentialsMessage(message) {
		return fmt.Errorf("%w: %s", ErrInvalidCredentials, err)
	}

	return err
}

func NewPorkBunWorker(log *slog.Logger, apiKey string, apiSecret string, requestCh chan common.Request) *Worker {
	return &Worker{
		log:        log,
		ApiKey:     apiKey,
		ApiSecret:  apiSecret,
		httpClient: &spicommon.DefaultHttpClient{},
		requestCh:  requestCh,
	}
}

func (w *Worker) Run(ctx context.Context) {
	credentialsBodyBytes, err := json.Marshal(CredsMessage{
		ApiKey:       w.ApiKey,
		SecretApiKey: w.ApiSecret,
	})

	if err == nil {
		w.credentialsBody = credentialsBodyBytes
	} else {
		w.log.Error("unable to serialize credentials", "error", err)
	}

	for run := true; run; {
		select {
		case <-ctx.Done():
			run = false
			if ctx.Err() != nil && !errors.Is(ctx.Err(), context.Canceled) {
				w.err.Store(fmt.Errorf("porkBunWorker received unexpected error from context: %w", ctx.Err()))
			}
			break
		case request := <-w.requestCh:
			w.processRequest(&request)
			break
		}
	}

	// Drain the request channel
	for request := range w.requestCh {
		w.cancelRequest(&request)
	}
}

func (w *Worker) Err() error {
	if err := w.err.Load(); err != nil {
		return err.(error)
	}

	return nil
}

func (w *Worker) cancelRequest(request *common.Request) {
	if request.ResultCh != nil {
		request.ResultCh <- common.DnsRecordResult{
			Error: errors.New("request cancelled"),
		}
	}
}

func (w *Worker) processRequest(request *common.Request) {
	w.log.Debug("received request, type", "requestType", request.RequestType)
	switch request.RequestType {
	case common.RequestTypeGetDnsRecord:
		w.processGetDnsRecordRequest(&request.GetDnsRecordRequest, request.ResultCh)
		break
	case common.RequestTypeUpdateDnsRecord:
		w.processUpdateDnsRecordRequest(&request.UpdateDnsRecordRequest, request.ResultCh)
		break
	}
}

func (w *Worker) processGetDnsRecordRequest(
	request *common.GetDnsRecordRequest,
	resultCh chan common.DnsRecordResult) {
	currentRecord, err := w.getDnsRecord(request.Domain, request.Type, request.Name)

	if err != nil {
		completeDnsRecordRequestWithError(
			w.log,
			resultCh,
			fmt.Errorf("error retrieving DNS record: %w", err),
			"DNS record retrieval failed")
		return
	}

	now := time.Now()

	completeDnsRecordRequestWithSuccess(
		w.log,
		resultCh,
		&currentRecord,
		&now,
		nil,
		"Retrieved successfully",
		"DNS record retrieval")
}

func (w *Worker) processUpdateDnsRecordRequest(
	request *common.UpdateDnsRecordRequest,
	resultCh chan common.DnsRecordResult) {
	// From what Porkbun has now, never from a copy kept from an earlier read: a copy that was wrong would have an update that is
	// needed skipped as not needed.
	currentRecord, err := w.getDnsRecord(request.Domain, request.CurrentData.Type, request.CurrentData.Name)

	if err != nil {
		completeDnsRecordRequestWithError(
			w.log,
			resultCh,
			fmt.Errorf("error retrieving DNS record: %w", err),
			"DNS record update failed")
		return
	}

	readTime := time.Now()

	if !request.Force && resolve.Same(currentRecord.Type, currentRecord.Content, request.NewValue) {
		completeDnsRecordRequestWithSuccess(
			w.log,
			resultCh,
			&currentRecord,
			&readTime,
			&request.CurrentData.LastModifiedTime,
			fmt.Sprintf("DNS record %s %s %s already has value \"%s\", no update needed",
				request.Domain, request.CurrentData.Type, request.CurrentData.Name, request.NewValue),
			"DNS record update")
		return
	}

	if _, err = w.updateDnsRecord(&currentRecord, request); err != nil {
		completeDnsRecordRequestWithError(
			w.log,
			resultCh,
			fmt.Errorf("error updating DNS record: %w", err),
			"DNS record update failed")
		return
	}

	// Porkbun said it was done. It is only so if it says so when asked.
	savedRecord, err := w.getDnsRecord(request.Domain, request.CurrentData.Type, request.CurrentData.Name)

	if err != nil {
		completeDnsRecordRequestWithError(
			w.log,
			resultCh,
			fmt.Errorf("unable to read the DNS record back after updating it: %w", err),
			"DNS record update could not be verified")
		return
	}

	if !resolve.Same(savedRecord.Type, savedRecord.Content, request.NewValue) {
		completeDnsRecordRequestWithError(
			w.log,
			resultCh,
			fmt.Errorf("%w: %s %s %s was updated to \"%s\" and is still \"%s\"",
				ErrNotSaved, request.Domain, request.CurrentData.Type, request.CurrentData.Name, request.NewValue, savedRecord.Content),
			"DNS record update was not saved")
		return
	}

	now := time.Now()
	completeDnsRecordRequestWithSuccess(
		w.log,
		resultCh,
		&savedRecord,
		&now,
		&now,
		"Updated successfully",
		"DNS record update")
}

func (w *Worker) getDnsRecord(domain string, recordType string, name string) (ResponseDnsRecordMessage, error) {
	uri := fmt.Sprintf("https://api.porkbun.com/api/json/v3/dns/retrieveByNameType/%s/%s/%s",
		domain, recordType, name)
	requestMessage := CredsMessage{
		ApiKey:       w.ApiKey,
		SecretApiKey: w.ApiSecret,
	}
	responseMessage := RetrieveDnsRerecordResponseMessage{}
	err := w.executeHttpPost(uri, &requestMessage, &responseMessage)

	if err != nil {
		return ResponseDnsRecordMessage{},
			fmt.Errorf("error retrieving %s %s %s DNS record: %s",
				domain, recordType, name, err)
	}

	w.log.Debug("retrieved DNS record", "responseMessage", responseMessage)

	if responseMessage.Status != "SUCCESS" {
		return ResponseDnsRecordMessage{}, wrapStatusError(
			"retrieval unsuccessful", responseMessage.Status, responseMessage.Message)
	}

	recordCount := len(responseMessage.Records)
	if recordCount == 0 {
		return ResponseDnsRecordMessage{},
			fmt.Errorf("no DNS records found for %s %s %s",
				domain, recordType, name)
	} else if recordCount > 1 {
		w.log.Warn("multiple DNS records found, using first one", "domain", domain, "recordType", recordType, "name", name)
	}

	currentRecord := responseMessage.Records[0]
	domainSuffix := "." + domain

	if strings.HasSuffix(currentRecord.Name, domainSuffix) {
		currentRecord.Name = strings.TrimSuffix(currentRecord.Name, domainSuffix)
	}

	return currentRecord, nil
}

func (w *Worker) updateDnsRecord(
	currentRecord *ResponseDnsRecordMessage,
	request *common.UpdateDnsRecordRequest) (ResponseDnsRecordMessage, error) {
	updateRequestMessage := EditDnsRecordRequestMessage{
		CredsMessage: CredsMessage{
			ApiKey:       w.ApiKey,
			SecretApiKey: w.ApiSecret,
		},
		DnsRecordMessage: DnsRecordMessage{
			Type:  currentRecord.Type,
			Ttl:   currentRecord.Ttl,
			Notes: currentRecord.Notes,
			Prio:  currentRecord.Prio,
			// The Name in the get record response includes the domain,
			// so we can't use it directly
			Name:    request.CurrentData.Name,
			Content: request.NewValue,
		},
	}

	uri := fmt.Sprintf("https://api.porkbun.com/api/json/v3/dns/edit/%s/%s", request.Domain, currentRecord.Id)
	responseMessage := StatusMessage{}
	err := w.executeHttpPost(uri, &updateRequestMessage, &responseMessage)

	if err != nil {
		return ResponseDnsRecordMessage{},
			fmt.Errorf(
				"error sending update DNS record request: %w", err)
	}

	if responseMessage.Status != "SUCCESS" {
		return ResponseDnsRecordMessage{}, wrapStatusError(
			"update unsuccessful", responseMessage.Status, responseMessage.Message)
	}

	// Copy the current record and update with changed values
	updatedRecord := *currentRecord
	updatedRecord.Content = request.NewValue

	return updatedRecord, nil
}

func (w *Worker) executeHttpPost(uri string, body any, result any) error {
	jsonBytes, err := json.Marshal(body)

	if err != nil {
		return fmt.Errorf("error serializing request body: %w", err)
	}

	response, err := w.httpClient.Post(uri, "application/json", bytes.NewReader(jsonBytes))

	if err != nil {
		return fmt.Errorf("http post failed: %w", err)
	}
	defer func() { closeResponse(w.log, response) }()

	responseBytes, err := io.ReadAll(response.Body)

	if err != nil {
		return fmt.Errorf("error reading response body: %w", err)
	}

	err = json.Unmarshal(responseBytes, result)

	if err != nil {
		return fmt.Errorf("error unmarshalling response: %w (body: %s)", err, string(responseBytes))
	}

	return nil
}

func closeResponse(log *slog.Logger, response *http.Response) {
	if response != nil {
		closeErr := response.Body.Close()

		if closeErr != nil {
			log.Error("error closing response body", "error", closeErr)
		}
	}
}

func buildDnsRecordData(log *slog.Logger, record *ResponseDnsRecordMessage, lastUpdateTime *time.Time) data.DnsRecordData {
	ttlInt, err := strconv.Atoi(record.Ttl)

	if err != nil {
		log.Warn("error parsing TTL", "ttl", record.Ttl, "error", err)
	}

	priorityInt, err := strconv.Atoi(record.Prio)

	if err != nil {
		log.Warn("error parsing priority", "priority", record.Prio, "error", err)
	}

	return data.DnsRecordData{
		Id:             record.Id,
		Name:           record.Name,
		Type:           record.Type,
		Value:          record.Content,
		Ttl:            int32(ttlInt),
		Priority:       int32(priorityInt),
		Notes:          record.Notes,
		LastUpdateTime: *lastUpdateTime,
	}
}

func completeDnsRecordRequestWithSuccess(
	log *slog.Logger,
	resultCh chan common.DnsRecordResult,
	record *ResponseDnsRecordMessage,
	lastUpdateTime *time.Time,
	lastModifiedTime *time.Time,
	message string,
	logMessage string) {
	if resultCh == nil {
		log.Info(logMessage, "message", message)
	} else {
		recordData := buildDnsRecordData(log, record, lastUpdateTime)

		if lastModifiedTime != nil {
			recordData.LastModifiedTime = *lastModifiedTime
		}

		resultCh <- common.DnsRecordResult{
			Message:     message,
			CurrentData: recordData,
		}
		close(resultCh)
	}
}

func completeDnsRecordRequestWithError(log *slog.Logger, resultCh chan common.DnsRecordResult, err error, logMessage string) {
	if resultCh == nil {
		log.Error(logMessage, "error", err)
	} else {
		resultCh <- common.DnsRecordResult{
			Error: err,
		}
		close(resultCh)
	}
}
