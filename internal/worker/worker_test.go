package worker

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/avanha/pmaas-plugin-porkbun/data"
	"github.com/avanha/pmaas-plugin-porkbun/internal/common"
)

// fakePorkbun answers the API's posts: what the record holds, which an edit changes if it is let to.
type fakePorkbun struct {
	content   string
	acceptAll bool // an edit is acknowledged and saved
	ignore    bool // an edit is acknowledged and not saved
	calls     []string
	failEdit  bool
}

func (f *fakePorkbun) Get(string) (*http.Response, error) { return nil, errors.New("not used") }

func (f *fakePorkbun) Post(uri string, _ string, body io.Reader) (*http.Response, error) {
	f.calls = append(f.calls, uri[strings.LastIndex(uri, "/api/json/v3/")+len("/api/json/v3/"):])
	_, _ = io.ReadAll(body)

	var reply string

	switch {
	case strings.Contains(uri, "/dns/retrieveByNameType/"):
		reply = fmt.Sprintf(`{"status":"SUCCESS","records":[{"id":"7","name":"www.example.com","type":"A","content":%q,"ttl":"600","prio":"0"}]}`, f.content)
	case strings.Contains(uri, "/dns/edit/"):
		if f.failEdit {
			reply = `{"status":"ERROR","message":"nope"}`
			break
		}

		if !f.ignore {
			f.content = "NEW"
		}

		reply = `{"status":"SUCCESS"}`
	}

	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(reply))}, nil
}

func newTestWorker(api *fakePorkbun) *Worker {
	return &Worker{log: slog.New(slog.DiscardHandler), httpClient: api}
}

func update(w *Worker, newValue string, force bool) common.DnsRecordResult {
	resultCh := make(chan common.DnsRecordResult, 1)
	w.processUpdateDnsRecordRequest(&common.UpdateDnsRecordRequest{
		Domain: "example.com", NewValue: newValue, Force: force,
		CurrentData: data.DnsRecordData{Name: "www", Type: "A"},
	}, resultCh)

	return <-resultCh
}

func TestUpdate_ReadsTheRecordBackAndReportsWhatPorkbunHolds(t *testing.T) {
	api := &fakePorkbun{content: "OLD"}
	result := update(newTestWorker(api), "NEW", false)

	if result.Error != nil || result.CurrentData.Value != "NEW" || result.CurrentData.LastModifiedTime.IsZero() {
		t.Fatalf("%+v", result)
	}

	if got := strings.Join(api.calls, " "); !strings.HasPrefix(got, "dns/retrieveByNameType/") || strings.Count(got, "retrieveByNameType") != 2 || !strings.Contains(got, "dns/edit/") {
		t.Errorf("a read, the edit, and a read again: %v", api.calls)
	}
}

func TestUpdate_AnEditPorkbunAcknowledgesAndDoesNotSaveIsAnError(t *testing.T) {
	api := &fakePorkbun{content: "OLD", ignore: true}
	result := update(newTestWorker(api), "NEW", false)

	if !errors.Is(result.Error, ErrNotSaved) {
		t.Fatalf("%+v", result)
	}
}

func TestUpdate_ADecisionNotToUpdateIsFromAFreshReadNotACopy(t *testing.T) {
	api := &fakePorkbun{content: "NEW"}
	result := update(newTestWorker(api), "NEW", false)

	if result.Error != nil || result.CurrentData.Value != "NEW" || len(api.calls) != 1 {
		t.Fatalf("%+v %v", result, api.calls)
	}

	// Another value than what the last read said: Porkbun is asked, and changed.
	api = &fakePorkbun{content: "OTHER"}

	if result := update(newTestWorker(api), "NEW", false); result.Error != nil || result.CurrentData.Value != "NEW" {
		t.Errorf("%+v", result)
	}
}

func TestUpdate_ForceSendsTheEditEvenIfPorkbunSaysItIsTheValue(t *testing.T) {
	api := &fakePorkbun{content: "NEW"}
	result := update(newTestWorker(api), "NEW", true)

	if result.Error != nil || !strings.Contains(strings.Join(api.calls, " "), "dns/edit/") || result.CurrentData.LastModifiedTime.IsZero() {
		t.Fatalf("%+v %v", result, api.calls)
	}
}

func TestUpdate_AnEditPorkbunRefusesIsAnError(t *testing.T) {
	api := &fakePorkbun{content: "OLD", failEdit: true}

	if result := update(newTestWorker(api), "NEW", false); result.Error == nil || errors.Is(result.Error, ErrNotSaved) {
		t.Fatalf("%+v", result)
	}
}
