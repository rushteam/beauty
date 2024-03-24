package tracing

import (
	"context"
	"log"
	"time"

	"github.com/rushteam/beauty/pkg/core"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"

	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

var meter metric.Meter

type metricComponent struct{}

func (c *metricComponent) Name() string {
	return "metric"
}

func (c *metricComponent) Init() context.CancelFunc {
	return newMetric()
}

func NewMetric() core.Component {
	return &metricComponent{}
}

func newMetric() context.CancelFunc {
	metricExporter, err := stdoutmetric.New(
		stdoutmetric.WithPrettyPrint(),
	)
	if err != nil {
		log.Fatal(err)
	}
	meterProvider := sdkmetric.NewMeterProvider(
		// metric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			metricExporter,
			sdkmetric.WithInterval(5*time.Second), // default 1m, for test 5s
		)),
	)
	otel.SetMeterProvider(meterProvider)
	return func() {
		_ = meterProvider.Shutdown(context.Background())
	}
}

func Meter() metric.Meter {
	if meter == nil {
		meter = otel.GetMeterProvider().Meter("beauty")
	}
	return meter
}
