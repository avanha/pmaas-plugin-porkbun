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

func TestRecordTemplate_AGoodResolutionIsAGreenTickAndWhenAndABadOneARedCrossAndWhatItResolvesTo(t *testing.T) {
	record := &data.DnsRecordData{
		Name: "www", Type: "A", Value: "10.0.0.2", ResolveSupported: true, ResolveChecked: true, ResolveMatches: true,
		ResolvedValues: []string{"10.0.0.2"}, ResolveTime: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
	}

	out := render(t, "dns_record.htmlt", record)

	if !strings.Contains(out, "resolve-ok") || !strings.Contains(out, "as of 12:00:00 PM") || strings.Contains(out, "resolve-bad") {
		t.Errorf("%s", out)
	}

	// A good one doesn't give the address again: it is the record's value, which is above.
	if strings.Contains(out, "Resolves to") {
		t.Errorf("the address was given twice: %s", out)
	}

	record.ResolveMatches = false
	record.ResolvedValues = []string{"2605:a601:a9c5:602:1e69:7aff:fe0d:b1e1", "10.0.0.3"}
	record.Problem = "this machine resolves <b>x</b>"

	out = render(t, "dns_record.htmlt", record)

	if !strings.Contains(out, "resolve-bad") || !strings.Contains(out, "as of 12:00:00 PM") || strings.Contains(out, "resolve-ok") ||
		!strings.Contains(out, "Resolves to") || !strings.Contains(out, "2605:a601:a9c5:602:1e69:7aff:fe0d:b1e1, 10.0.0.3") || !strings.Contains(out, "record-problem") {
		t.Errorf("%s", out)
	}

	// The time is not on the line with the address, which is long and would wrap it.
	resolvesTo := out[strings.Index(out, "record-resolved-to"):]
	if strings.Contains(resolvesTo, "12:00:00") {
		t.Errorf("the time is with the address: %s", resolvesTo)
	}

	if strings.Contains(out, "<b>x</b>") {
		t.Errorf("a problem became markup: %s", out)
	}

	record.ResolvedValues = nil

	if out := render(t, "dns_record.htmlt", record); !strings.Contains(out, "nothing") {
		t.Errorf("%s", out)
	}

	record.ResolveChecked, record.Problem = false, ""

	if out := render(t, "dns_record.htmlt", record); !strings.Contains(out, "Not checked yet") || strings.Contains(out, "resolve-bad") {
		t.Errorf("%s", out)
	}

	record.ResolveSupported = false

	if out := render(t, "dns_record.htmlt", record); strings.Contains(out, "Resolves") {
		t.Errorf("%s", out)
	}
}

func TestRecordTemplate_SaysWhichServerItWasLookedUpThrough(t *testing.T) {
	record := &data.DnsRecordData{Name: "www", Type: "A", ResolveSupported: true, ResolveChecked: true, ResolveMatches: true, Resolver: "1.1.1.1"}

	if out := render(t, "dns_record.htmlt", record); !strings.Contains(out, "Looked up through 1.1.1.1") {
		t.Errorf("%s", out)
	}

	record.Resolver = ""

	if out := render(t, "dns_record.htmlt", record); !strings.Contains(out, "this machine") {
		t.Errorf("%s", out)
	}
}
