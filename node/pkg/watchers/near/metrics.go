package near

import (
	"context"
	"time"

	"github.com/certusone/wormhole/node/pkg/common"
	"github.com/certusone/wormhole/node/pkg/p2p"
	gossipv1 "github.com/certusone/wormhole/node/pkg/proto/gossip/v1"
	"github.com/certusone/wormhole/node/pkg/readiness"
	"github.com/certusone/wormhole/node/pkg/supervisor"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/wormhole-foundation/wormhole/sdk/vaa"
	"go.uber.org/zap"
)

type eventType int

const (
	EVENT_FINALIZED_CACHE_MISS eventType = iota
	EVENT_NEAR_MESSAGE_CONFIRMED
	EVENT_NEAR_API_HTTP_ERR // NEAR API returned a status code other than 200
	EVENT_NEAR_WATCHER_TOO_FAR_BEHIND
)

func (e *Watcher) runMetrics(ctx context.Context) error {
	logger := supervisor.Logger(ctx)

	reg := prometheus.NewRegistry()

	wormholeMsgCounter := 0
	var wormholeMsgTotalTime time.Duration = 0

	currentNearHeight := promauto.With(reg).NewGauge(
		prometheus.GaugeOpts{
			Name: "wormhole_near_current_height",
			Help: "Height of the highest block that has been processed. (Transactions from prior blocks may still be waiting).",
		})

	wormholeTxAvgDuration := promauto.With(reg).NewGauge(
		prometheus.GaugeOpts{
			Name: "wormhole_near_tx_avg_duration",
			Help: "Average duration it takes for a wormhole message to be processed in milliseconds",
		})

	txQuequeLen := promauto.With(reg).NewGauge(
		prometheus.GaugeOpts{
			Name: "wormhole_near_tx_queque",
			Help: "Current Near transaction processing queque length",
		})

	chunkQuequeLen := promauto.With(reg).NewGauge(
		prometheus.GaugeOpts{
			Name: "wormhole_near_chunk_queque",
			Help: "Current Near chunk processing queque length",
		})

	nearMessagesConfirmed := promauto.With(reg).NewCounter(
		prometheus.CounterOpts{
			Name: "wormhole_near_observations_confirmed_total",
			Help: "Total number of verified Near observations found",
		})

	nearFinalizedCacheMisses := promauto.With(reg).NewCounter(
		prometheus.CounterOpts{
			Name: "wormhole_near_finalized_cache_misses",
			Help: "Number of times the watcher needed to check the finalization status of a block by walking the chain forward because the finalization status was not cached.",
		})

	nearRpcErrorCounter := promauto.With(reg).NewCounter(
		prometheus.CounterOpts{
			Name: "wormhole_near_rpc_error",
			Help: "NEAR RPC Error Counter",
		})

	var highestBlockHeightProcessed uint64 = 0

	metricsIntervalTimer := time.NewTicker(metricsInterval) // this is just one ms for the first iteration.

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-metricsIntervalTimer.C:
			// compute and publish periodic metrics
			l1 := e.transactionProcessingQueue.Len()
			l2 := len(e.chunkProcessingQueue)
			txQuequeLen.Set(float64(l1))
			chunkQuequeLen.Set(float64(l2))
			logger.Info("metrics", zap.Int("txQuequeLen", l1), zap.Int("chunkQuequeLen", l2))

		case height := <-e.eventChanBlockProcessedHeight:
			if highestBlockHeightProcessed < height {
				highestBlockHeightProcessed = height

				currentNearHeight.Set(float64(height))
				p2p.DefaultRegistry.SetNetworkStats(vaa.ChainIDNear, &gossipv1.Heartbeat_Network{
					Height:          int64(height),
					ContractAddress: e.wormholeAccount,
				})
				readiness.SetReady(common.ReadinessNearSyncing)

				logger.Info("newHeight", zap.String("log_msg_type", "height"), zap.Uint64("height", height))
			}
		case event := <-e.eventChan:
			switch event {
			case EVENT_FINALIZED_CACHE_MISS:
				nearFinalizedCacheMisses.Inc()
			case EVENT_NEAR_MESSAGE_CONFIRMED:
				nearMessagesConfirmed.Inc()
			case EVENT_NEAR_WATCHER_TOO_FAR_BEHIND:
				logger.Error("NEAR Watcher fell behind too far", zap.String("log_msg_type", "watcher_behind"))
				p2p.DefaultRegistry.AddErrorCount(vaa.ChainIDNear, 1)
			case EVENT_NEAR_API_HTTP_ERR:
				nearRpcErrorCounter.Inc()
			}
		case d := <-e.eventChanTxProcessedDuration:
			wormholeMsgCounter++
			wormholeMsgTotalTime += d
			avgDurationMs := wormholeMsgTotalTime.Milliseconds() / int64(wormholeMsgCounter)
			wormholeTxAvgDuration.Set(float64(avgDurationMs))
		}
	}
}
