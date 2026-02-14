package worker

type StatusMessage struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type RetrieveDnsRerecordResponseMessage struct {
	StatusMessage
	Records []ResponseDnsRecordMessage `json:"records"`
}

type RecordWithIdMessage struct {
	Id string `json:"id"`
}

type DnsRecordMessage struct {
	Name    string `json:"name,omitempty"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Ttl     string `json:"ttl,omitempty"`
	Prio    string `json:"prio,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

type ResponseDnsRecordMessage struct {
	RecordWithIdMessage
	DnsRecordMessage
}

type EditDnsRecordRequestMessage struct {
	CredsMessage
	DnsRecordMessage
}
