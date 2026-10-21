package porkbun

import (
	"github.com/avanha/pmaas-plugin-porkbun/internal/common"
	"github.com/avanha/pmaas-spi"
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

func (e entityStoreAdapter) ForceUpdate() (common.ForceResult, error) {
	type outcome struct {
		result common.ForceResult
		err    error
	}

	done, err := spi.Exec(e.parent.container, func() outcome {
		result, err := e.parent.forceUpdate()

		return outcome{result: result, err: err}
	})
	if err != nil {
		return common.ForceResult{}, err
	}

	return done.result, done.err
}
