package dashboardmetrics

import (
	"context"
	"time"

	"go.uber.org/dig"

	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/hive.go/runtime/event"
	"github.com/iotaledger/hive.go/runtime/timeutil"
	"github.com/iotaledger/iota-core/components/restapi"
	"github.com/iotaledger/iota-core/pkg/daemon"
	"github.com/iotaledger/iota-core/pkg/protocol"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
)

func init() {
	Component = &app.Component{
		Name:      "DashboardMetrics",
		DepsFunc:  func(cDeps dependencies) { deps = cDeps },
		Configure: configure,
		Run:       run,
		IsEnabled: func(c *dig.Container) bool {
			return restapi.ParamsRestAPI.Enabled
		},
	}
}

var (
	Component *app.Component
	deps      dependencies
)

type dependencies struct {
	dig.In

	Protocol         *protocol.Protocol
	AppInfo          *app.Info
	RestRouteManager *restapi.RestRouteManager
}

func configure() error {
	// check if RestAPI plugin is disabled
	//if !Component.App().IsComponentEnabled(restapi.Component.Name) {
	//	Component.LogPanic("RestAPI plugin needs to be enabled to use the dashboard metrics plugin")
	//}

	return nil
}

func run() error {
	Component.Logger().Infof("Starting %s ...", Component.Name)
	registerLocalMetrics()
	if err := Component.Daemon().BackgroundWorker("DashboardMetricsUpdater", func(ctx context.Context) {
		// Do not block until the Ticker is shutdown because we might want to start multiple Tickers and we can
		// safely ignore the last execution when shutting down.
		timeutil.NewTicker(func() {
			measureAttachedBPS()
			measurePerComponentCounter()
		}, 1*time.Second, ctx)

		// Wait before terminating so we get correct log blocks from the daemon regarding the shutdown order.
		<-ctx.Done()
	}, daemon.PriorityDashboardMetrics); err != nil {
		Component.LogPanicf("failed to start worker: %s", err)
	}

	return nil
}

func registerLocalMetrics() {
	// increase received BPS counter whenever we attached a block
	deps.Protocol.Events.Engine.BlockDAG.BlockAttached.Hook(func(block *blocks.Block) {
		blockCountPerComponentMutex.Lock()
		defer blockCountPerComponentMutex.Unlock()
		increaseReceivedBPSCounter()
	}, event.WithWorkerPool(Component.WorkerPool))
}
