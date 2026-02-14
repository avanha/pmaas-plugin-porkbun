package worker

type CredsMessage struct {
	SecretApiKey string `json:"secretapikey"`
	ApiKey       string `json:"apikey"`
}
