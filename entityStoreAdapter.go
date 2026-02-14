package porkbun

import (
	"pmaas.io/plugins/porkbun/internal/common"
	"pmaas.io/spi"
)

type entityStoreAdapter struct {
	parent *plugin
}

func (e entityStoreAdapter) GetStatusAndEntities() (common.StatusAndEntities, error) {
	// HTTP requests come in on arbitrary goroutines, so execute getEntities on the main plugin goroutine to get all
	// states atomically.
	return spi.ExecValueFunctionOnPluginGoRoutine(
		e.parent.container,
		e.parent.getStatusAndEntities,
		func() common.StatusAndEntities { return common.StatusAndEntities{} },
		"unable to get status and entities")
}
