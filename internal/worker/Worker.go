package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/avanha/pmaas-plugin-porkbun/data"
	"github.com/avanha/pmaas-plugin-porkbun/internal/common"
)

type Worker struct {
	ApiKey          string
	ApiSecret       string
	credentialsBody []byte
	requestCh       chan common.Request
	err             atomic.Value
}

func NewPorkBunWorker(apiKey string, apiSecret string, requestCh chan common.Request) *Worker {
	return &Worker{
		ApiKey:    apiKey,
		ApiSecret: apiSecret,
		requestCh: requestCh,
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
		fmt.Printf("Unable to serialize credentials: %s\n", err)
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
			w.processRequest(request)
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

func (w *Worker) processRequest(request common.Request) {
	fmt.Printf("%T Received request, type %d\n", w, request.RequestType)
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
			resultCh,
			fmt.Errorf("error to retrieving DNS record: %w", err),
			"DNS record retrieval failed")
		return
	}

	now := time.Now()

	completeDnsRecordRequestWithSuccess(
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
	var currentRecord ResponseDnsRecordMessage
	var err error
	var updateTime time.Time
	if request.CurrentData.LastUpdateTime.Before(time.Now().Add(-5 * time.Minute)) {
		currentRecord, err = w.getDnsRecord(request.Domain, request.CurrentData.Type, request.CurrentData.Name)

		if err != nil {
			completeDnsRecordRequestWithError(
				resultCh,
				fmt.Errorf("error retrieving DNS record: %w", err),
				"DNS record update failed")
			return
		}
		updateTime = time.Now()
	} else {
		// Synthesize ResponseDnsRecordMessage from current data
		currentRecord = ResponseDnsRecordMessage{
			RecordWithIdMessage: RecordWithIdMessage{
				Id: request.CurrentData.Id,
			},
			DnsRecordMessage: DnsRecordMessage{
				Name:    request.CurrentData.Name,
				Type:    request.CurrentData.Type,
				Content: request.CurrentData.Value,
				Ttl:     strconv.Itoa(int(request.CurrentData.Ttl)),
				Prio:    strconv.Itoa(int(request.CurrentData.Priority)),
				Notes:   request.CurrentData.Notes,
			},
		}
		updateTime = request.CurrentData.LastUpdateTime
	}

	if currentRecord.Content == request.NewValue {
		completeDnsRecordRequestWithSuccess(
			resultCh,
			&currentRecord,
			&updateTime,
			&request.CurrentData.LastModifiedTime,
			fmt.Sprintf("DNS record %s %s %s already has value \"%s\", no update needed",
				request.Domain, request.CurrentData.Type, request.CurrentData.Name, request.NewValue),
			"DNS record update")
		return
	}

	currentRecord, err = w.updateDnsRecord(&currentRecord, request)

	if err != nil {
		completeDnsRecordRequestWithError(
			resultCh,
			fmt.Errorf("error updating DNS record: %w", err),
			"DNS record update failed")
		return
	}

	now := time.Now()
	completeDnsRecordRequestWithSuccess(
		resultCh,
		&currentRecord,
		&updateTime,
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

	fmt.Printf("%T Retrieved DNS record: %+v\n", w, responseMessage)

	if responseMessage.Status != "SUCCESS" {
		return ResponseDnsRecordMessage{},
			fmt.Errorf("retrieval unsuccessful: %w", err)
	}

	recordCount := len(responseMessage.Records)
	if recordCount == 0 {
		return ResponseDnsRecordMessage{},
			fmt.Errorf("no DNS records found for %s %s %s",
				domain, recordType, name)
	} else if recordCount > 1 {
		fmt.Printf("%T Warning: multiple DNS records found for %s %s %s, using first one\n",
			w, domain, recordType, name)
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

	response, err := http.Post(uri, "application/json", bytes.NewReader(jsonBytes))

	if err != nil {
		return fmt.Errorf("http post failed: %w", err)
	}
	defer func() { closeResponse(response) }()

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

func closeResponse(response *http.Response) {
	if response != nil {
		closeErr := response.Body.Close()

		if closeErr != nil {
			fmt.Printf("Error closing response body: %s\n", closeErr)
		}
	}
}

func buildDnsRecordData(record *ResponseDnsRecordMessage, lastUpdateTime *time.Time) data.DnsRecordData {
	ttlInt, err := strconv.Atoi(record.Ttl)

	if err != nil {
		fmt.Printf("Error parsing TTL from \"%s\": %s\n", record.Ttl, err)
	}

	priorityInt, err := strconv.Atoi(record.Prio)

	if err != nil {
		fmt.Printf("Error parsing priority from \"%s\": %s\n", record.Ttl, err)
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
	resultCh chan common.DnsRecordResult,
	record *ResponseDnsRecordMessage,
	lastUpdateTime *time.Time,
	lastModifiedTime *time.Time,
	message string,
	logMessage string) {
	if resultCh == nil {
		fmt.Printf("%s: %s\n", logMessage, message)
	} else {
		recordData := buildDnsRecordData(record, lastUpdateTime)

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

func completeDnsRecordRequestWithError(resultCh chan common.DnsRecordResult, err error, logMessage string) {
	if resultCh == nil {
		fmt.Printf("%s: %s\n", logMessage, err)
	} else {
		resultCh <- common.DnsRecordResult{
			Error: err,
		}
		close(resultCh)
	}
}
