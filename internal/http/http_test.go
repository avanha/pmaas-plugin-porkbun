package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/avanha/pmaas-plugin-porkbun/data"
	"github.com/avanha/pmaas-plugin-porkbun/internal/common"
)

type fakeStore struct {
	result common.ForceResult
	err    error
	forced int
}

func (s *fakeStore) GetStatusAndEntities() (common.StatusAndEntities, error) {
	return common.StatusAndEntities{}, nil
}

func (s *fakeStore) ForceUpdate() (common.ForceResult, error) {
	s.forced++

	return s.result, s.err
}

func render(t *testing.T, file string, value any) string {
	t.Helper()

	compiled, err := template.New("t").ParseFS(contentFS, "content/templates/"+file)
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := compiled.ExecuteTemplate(&out, file, value); err != nil {
		t.Fatal(err)
	}

	return out.String()
}

func TestForceRoute_ForcesAndSaysWhatItDidAndRefusesAGet(t *testing.T) {
	store := &fakeStore{result: common.ForceResult{Updated: 1, Checked: 2, Failed: []string{"x: nope"}}}
	h := &Handler{entityStore: store}

	response, err := h.handleForceRequest(nil, httptest.NewRequest(http.MethodPost, "/plugins/porkbun/force", nil), &forceRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if got := response.(forceResponse); got.Updated != 1 || got.Checked != 2 || len(got.Failed) != 1 || store.forced != 1 {
		t.Errorf("%+v %d", got, store.forced)
	}

	if _, err := h.handleForceRequest(nil, httptest.NewRequest(http.MethodGet, "/plugins/porkbun/force", nil), &forceRequest{}); err == nil || store.forced != 1 {
		t.Error("a GET was accepted")
	}

	store.err = errors.New("plugin is not running")

	if _, err := h.handleForceRequest(nil, httptest.NewRequest(http.MethodPost, "/plugins/porkbun/force", nil), &forceRequest{}); err == nil {
		t.Error("a failure was reported as done")
	}
}

func TestStatusTemplate_HasTheButton(t *testing.T) {
	if out := render(t, "porkbun_status.htmlt", &data.PluginStatus{}); !strings.Contains(out, `class="force-update"`) {
		t.Error(out)
	}
}

func TestRecordTemplate_ShowsWhatResolvesAndWhatIsWrongWithoutLettingItBecomeMarkup(t *testing.T) {
	record := &data.DnsRecordData{
		Name: "www", Type: "A", Value: "10.0.0.2", ResolveSupported: true, ResolveChecked: true,
		ResolvedValues: []string{"10.0.0.1", "10.0.0.3"}, ResolveTime: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		Problem: "this machine resolves <b>x</b>",
	}

	out := render(t, "dns_record.htmlt", record)

	if !strings.Contains(out, "10.0.0.1, 10.0.0.3") || !strings.Contains(out, "resolved-different") || !strings.Contains(out, "record-problem") {
		t.Errorf("%s", out)
	}

	if strings.Contains(out, "<b>x</b>") {
		t.Errorf("a problem became markup: %s", out)
	}

	record.ResolveChecked, record.ResolveMatches, record.Problem = true, true, ""
	record.ResolvedValues = nil

	if out := render(t, "dns_record.htmlt", record); strings.Contains(out, "record-problem") || !strings.Contains(out, "nothing") {
		t.Errorf("%s", out)
	}

	record.ResolveSupported = false

	if out := render(t, "dns_record.htmlt", record); strings.Contains(out, "Resolves to") {
		t.Errorf("%s", out)
	}
}
