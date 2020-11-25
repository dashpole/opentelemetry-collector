// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package prometheusreceiver

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery"
	"github.com/prometheus/prometheus/pkg/relabel"
	"github.com/prometheus/prometheus/scrape"
	"go.uber.org/zap"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver/prometheusreceiver/internal"
)

const (
	hashLabel = "__tmp_hash"
)

var (
	statefulsetIndexMatcher = regexp.MustCompile(`.*-([0-9]+)$`)
)

// pReceiver is the type that provides Prometheus scraper/receiver functionality.
type pReceiver struct {
	cfg        *Config
	consumer   consumer.MetricsConsumer
	cancelFunc context.CancelFunc

	logger *zap.Logger
}

// New creates a new prometheus.Receiver reference.
func newPrometheusReceiver(logger *zap.Logger, cfg *Config, next consumer.MetricsConsumer) *pReceiver {
	pr := &pReceiver{
		cfg:      cfg,
		consumer: next,
		logger:   logger,
	}
	return pr
}

// Start is the method that starts Prometheus scraping and it
// is controlled by having previously defined a Configuration using perhaps New.
func (r *pReceiver) Start(ctx context.Context, host component.Host) error {
	discoveryCtx, cancel := context.WithCancel(context.Background())
	r.cancelFunc = cancel

	logger := internal.NewZapToGokitLogAdapter(r.logger)

	discoveryManager := discovery.NewManager(discoveryCtx, logger)
	discoveryCfg := make(map[string]discovery.Configs)
	for _, scrapeConfig := range r.cfg.PrometheusConfig.ScrapeConfigs {
		discoveryCfg[scrapeConfig.JobName] = scrapeConfig.ServiceDiscoveryConfigs
	}
	if err := discoveryManager.ApplyConfig(discoveryCfg); err != nil {
		return err
	}
	go func() {
		if err := discoveryManager.Run(); err != nil {
			r.logger.Error("Discovery manager failed", zap.Error(err))
			host.ReportFatalError(err)
		}
	}()

	var jobsMap *internal.JobsMap
	if !r.cfg.UseStartTimeMetric {
		jobsMap = internal.NewJobsMap(2 * time.Minute)
	}
	ocaStore := internal.NewOcaStore(ctx, r.consumer, r.logger, jobsMap, r.cfg.UseStartTimeMetric, r.cfg.StartTimeMetricRegex, r.cfg.Name())

	scrapeManager := scrape.NewManager(logger, ocaStore)

	prometheusConfig := r.cfg.PrometheusConfig

	if r.cfg.PodName != "" {
		matches := statefulsetIndexMatcher.FindStringSubmatch(r.cfg.PodName)
		if len(matches) != 2 {
			err := fmt.Errorf("unable to get shard from pod name: %v", r.cfg.PodName)
			r.logger.Error("Prometheus Sharding failed", zap.Error(err))
			host.ReportFatalError(err)
		}
		shard, err := strconv.ParseUint(matches[1], 10, 64)
		if err != nil {
			r.logger.Error("Prometheus Sharding failed", zap.Error(err))
			host.ReportFatalError(err)
		}
		if shard >= r.cfg.Shards {
			err = fmt.Errorf("invalid shard configuration: shard %v, total shards: %v", shard, r.cfg.Shards)
			r.logger.Error("Prometheus Sharding failed", zap.Error(err))
			host.ReportFatalError(err)
		}
		prometheusConfig = r.shardConfig(prometheusConfig, shard, r.cfg.Shards)
	}

	ocaStore.SetScrapeManager(scrapeManager)
	if err := scrapeManager.ApplyConfig(prometheusConfig); err != nil {
		return err
	}
	go func() {
		if err := scrapeManager.Run(discoveryManager.SyncCh()); err != nil {
			r.logger.Error("Scrape manager failed", zap.Error(err))
			host.ReportFatalError(err)
		}
	}()
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		r.logger.Fatal(http.ListenAndServe(":8080", nil).Error())
	}()
	return nil
}

// Shutdown stops and cancels the underlying Prometheus scrapers.
func (r *pReceiver) Shutdown(context.Context) error {
	r.cancelFunc()
	return nil
}

func (r *pReceiver) shardConfig(config *config.Config, shard, totalShards uint64) *config.Config {
	regex := relabel.MustNewRegexp(fmt.Sprintf("%d", shard))
	relabelConfigs := []*relabel.Config{
		{
			SourceLabels: model.LabelNames{model.AddressLabel},
			Action:       relabel.HashMod,
			TargetLabel:  hashLabel,
			Modulus:      totalShards,
		},
		{
			SourceLabels: model.LabelNames{hashLabel},
			Action:       relabel.Keep,
			Regex:        regex,
		},
	}
	for _, sc := range config.ScrapeConfigs {
		sc.RelabelConfigs = append(sc.RelabelConfigs, relabelConfigs...)
	}

	r.logger.Info("Prometheus configuration with sharding", zap.Uint64("Shard", shard), zap.Reflect("Regex", regex), zap.Reflect("Config", config))
	return config
}
