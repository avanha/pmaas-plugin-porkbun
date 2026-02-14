package http

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"

	"pmaas.io/plugins/porkbun/data"
	"pmaas.io/plugins/porkbun/internal/common"
	"pmaas.io/spi"
)

//go:embed content/static content/templates
var contentFS embed.FS

var dnsRecordTemplate = spi.TemplateInfo{
	Name:   "dns_record",
	Paths:  []string{"templates/dns_record.htmlt"},
	Styles: []string{"css/dns_record.css"},
}

var statusTemplate = spi.TemplateInfo{
	Name:   "porkbun_status",
	Paths:  []string{"templates/porkbun_status.htmlt"},
	Styles: []string{"css/porkbun_status.css"},
}

type Handler struct {
	container   spi.IPMAASContainer
	entityStore common.EntityStore
}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) Init(container spi.IPMAASContainer, entityStore common.EntityStore) {
	h.container = container
	h.entityStore = entityStore
	container.ProvideContentFS(&contentFS, "content")
	container.EnableStaticContent("static")
	container.AddRoute("/plugins/porkbun/", h.handleHttpListRequest)
	container.RegisterEntityRenderer(
		reflect.TypeOf((*data.PluginStatus)(nil)).Elem(),
		h.statusDataRendererFactory)
	container.RegisterEntityRenderer(
		reflect.TypeOf((*data.DnsRecordData)(nil)).Elem(),
		h.dnsRecordDataRendererFactory)
}

func (h *Handler) handleHttpListRequest(writer http.ResponseWriter, request *http.Request) {
	result, err := h.entityStore.GetStatusAndEntities()

	if err != nil {
		fmt.Printf("porkbun.http handleHttpListRequest: Error retrieving entities: %s\n", err)
		result = common.StatusAndEntities{}
	}

	sort.SliceStable(result.DnsRecords, func(i, j int) bool {
		if result.DnsRecords[i].Name == result.DnsRecords[j].Name {
			return result.DnsRecords[i].Type < result.DnsRecords[j].Type
		}

		return result.DnsRecords[i].Name < result.DnsRecords[j].Name
	})

	// Convert the slice of structs to a slice of any
	entityListSize := len(result.DnsRecords)
	entityPointers := make([]any, entityListSize)

	for i := 0; i < entityListSize; i++ {
		entityPointers[i] = &result.DnsRecords[i]
	}

	h.container.RenderList(
		writer,
		request,
		spi.RenderListOptions{
			Title:  "porkbun",
			Header: &result.Status,
		},
		entityPointers)
}

func (h *Handler) statusDataRendererFactory() (spi.EntityRenderer, error) {
	// Load the template
	template, err := h.container.GetTemplate(&statusTemplate)

	if err != nil {
		return spi.EntityRenderer{}, fmt.Errorf("unable to load porkbun_status template: %v", err)
	}

	// Declare a function that casts the entity to the expected type and evaluates it via the template loaded above
	renderer := func(w io.Writer, entity any) error {
		status, ok := entity.(*data.PluginStatus)

		if !ok {
			return errors.New("item is not an instance of *PluginStatus")
		}

		err := template.Instance.Execute(w, status)

		if err != nil {
			return fmt.Errorf("unable to execute plugin_status template: %w", err)
		}

		return nil
	}

	return spi.EntityRenderer{
		StreamingRenderFunc: renderer,
		Styles:              template.Styles,
		Scripts:             template.Scripts,
	}, nil
}

func (h *Handler) dnsRecordDataRendererFactory() (spi.EntityRenderer, error) {
	// Load the template
	template, err := h.container.GetTemplate(&dnsRecordTemplate)

	if err != nil {
		return spi.EntityRenderer{}, fmt.Errorf("unable to load dns_record template: %v", err)
	}

	// Declare a function that casts the entity to the expected type and evaluates it via the template loaded above
	renderer := func(w io.Writer, entity any) error {
		dnsRecordData, ok := entity.(*data.DnsRecordData)

		if !ok {
			return errors.New("item is not an instance of *DsnRecordData")
		}

		err := template.Instance.Execute(w, dnsRecordData)

		if err != nil {
			return fmt.Errorf("unable to execute dnsRecord template: %w", err)
		}

		return nil
	}

	return spi.EntityRenderer{
		StreamingRenderFunc: renderer,
		Styles:              template.Styles,
		Scripts:             template.Scripts,
	}, nil
}
