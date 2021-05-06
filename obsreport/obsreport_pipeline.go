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

package obsreport

import (
	"context"
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/tag"
	"go.opentelemetry.io/collector/consumer/pdata"
)

const (
	// PipelineKey the name of the pipeline
	PipelineKey = "pipeline"

	MetricLatencyKey = "metric_processing_duration_seconds"
)

var (
	tagKeyPipeline, _             = tag.NewKey(PipelineKey)
	e2ePrefix                     = PipelineKey + nameSep
	mPipelineMetricLatencyMetrics = stats.Float64(
		e2ePrefix+MetricLatencyKey,
		"Duration of handling a metric in the pipeline.",
		stats.UnitSeconds)
)

func RecordPipelineDuration(ctx context.Context, metrics pdata.Metrics) {
	startTime := metrics.CreationTime()
	stats.Record(
		ctx,
		mPipelineMetricLatencyMetrics.M(time.Since(startTime).Seconds()))
}
