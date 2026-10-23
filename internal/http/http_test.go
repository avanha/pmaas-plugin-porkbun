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

func TestRecordTemplate_ATickAndWhenAreOnTheLineOfTheAddressAndThereIsNoOtherLineWhenItResolvesToIt(t *testing.T) {
	record := &data.DnsRecordData{
		Name: "www", Type: "AAAA", Value: "2605:a601:a9c5:600:1e69:7aff:fe0d:b1e1", ResolveSupported: true, ResolveChecked: true, ResolveMatches: true,
		ResolvedValues: []string{"2605:a601:a9c5:600:1e69:7aff:fe0d:b1e1"}, ResolveTime: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
	}

	out := render(t, "dns_record.htmlt", record)

	// The value, the mark and the time are in one block, in that order.
	line := out[strings.Index(out, `class="record-value-line"`):]
	line = line[:strings.Index(line, "</div>")]

	for _, want := range []string{"2605:a601:a9c5:600:1e69:7aff:fe0d:b1e1", "resolve-ok", "as of 12:00:00 PM"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q is not on the line of the address: %s", want, line)
		}
	}

	if strings.Index(line, "2605:") > strings.Index(line, "resolve-ok") || strings.Index(line, "resolve-ok") > strings.Index(line, "as of") {
		t.Errorf("not in order: %s", line)
	}

	// When it checks out there is nothing else about it.
	if strings.Contains(out, "Resolves") || strings.Contains(out, "resolve-bad") {
		t.Errorf("a line for what it resolves to when it resolves to the value: %s", out)
	}
}

func TestRecordTemplate_ARecordThatDoesNotResolveToItsValueHasACrossOnTheLineOfTheAddressAndWhatItResolvesToBelow(t *testing.T) {
	record := &data.DnsRecordData{
		Name: "www", Type: "AAAA", Value: "10.0.0.2", ResolveSupported: true, ResolveChecked: true,
		ResolvedValues: []string{"2605:a601:a9c5:602:1e69:7aff:fe0d:b1e1", "10.0.0.3"}, ResolveTime: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		Problem: "this machine resolves <b>x</b>",
	}

	out := render(t, "dns_record.htmlt", record)

	line := out[strings.Index(out, `class="record-value-line"`):]
	line = line[:strings.Index(line, "</div>")]

	if !strings.Contains(line, "resolve-bad") || !strings.Contains(line, "as of 12:00:00 PM") || strings.Contains(line, "resolve-ok") {
		t.Errorf("%s", line)
	}

	below := out[strings.Index(out, "record-resolved-to"):]

	if !strings.Contains(below, "Resolves to") || !strings.Contains(below, "2605:a601:a9c5:602:1e69:7aff:fe0d:b1e1, 10.0.0.3") ||
		strings.Contains(below, "12:00:00") || !strings.Contains(out, "record-problem") {
		t.Errorf("%s", out)
	}

	if strings.Contains(out, "<b>x</b>") {
		t.Errorf("a problem became markup: %s", out)
	}

	record.ResolvedValues = nil

	if out := render(t, "dns_record.htmlt", record); !strings.Contains(out, "nothing") {
		t.Errorf("%s", out)
	}
}

func TestRecordTemplate_NotCheckedYetShowsNothingButAFailedLookupIsSaid(t *testing.T) {
	record := &data.DnsRecordData{Name: "www", Type: "A", Value: "10.0.0.2", ResolveSupported: true}

	out := render(t, "dns_record.htmlt", record)
	if strings.Contains(out, "resolve-") || strings.Contains(out, "as of") || strings.Contains(out, "Resolves") || strings.Contains(out, "lookup failed") {
		t.Errorf("%s", out)
	}

	record.ResolveError = "i/o timeout"

	if out := render(t, "dns_record.htmlt", record); !strings.Contains(out, "i/o timeout") || strings.Contains(out, "resolve-ok") {
		t.Errorf("%s", out)
	}

	record.ResolveSupported = false

	if out := render(t, "dns_record.htmlt", record); strings.Contains(out, "i/o timeout") || strings.Contains(out, "as of") {
		t.Errorf("%s", out)
	}

	// A record that has no value yet has nothing to be ticked.
	record = &data.DnsRecordData{Name: "www", Type: "A", ResolveSupported: true, ResolveChecked: true, ResolveMatches: true}

	if out := render(t, "dns_record.htmlt", record); !strings.Contains(out, "Waiting for update") || strings.Contains(out, "resolve-ok") {
		t.Errorf("%s", out)
	}
}

func TestRecordTemplate_SaysWhichServerItWasLookedUpThrough(t *testing.T) {
	record := &data.DnsRecordData{Name: "www", Type: "A", Value: "10.0.0.2", ResolveSupported: true, ResolveChecked: true, ResolveMatches: true, Resolver: "1.1.1.1"}

	if out := render(t, "dns_record.htmlt", record); !strings.Contains(out, "Looked up through 1.1.1.1") {
		t.Errorf("%s", out)
	}

	record.Resolver = ""

	if out := render(t, "dns_record.htmlt", record); !strings.Contains(out, "this machine") {
		t.Errorf("%s", out)
	}
}
