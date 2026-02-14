package common

const (
	RequestTypeGetDnsRecord    = 1
	RequestTypeUpdateDnsRecord = 2
)

type Request struct {
	RequestType            int
	ResultCh               chan DnsRecordResult
	GetDnsRecordRequest    GetDnsRecordRequest
	UpdateDnsRecordRequest UpdateDnsRecordRequest
}

type Response struct {
	Error   error
	Message string
}
