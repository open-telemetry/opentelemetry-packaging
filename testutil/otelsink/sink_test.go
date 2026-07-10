// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otelsink_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/open-telemetry/opentelemetry-packaging/testutil/otelsink"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

const waitTimeout = 5 * time.Second

func strAttr(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}

func resource(testID string, extra ...*commonpb.KeyValue) *resourcepb.Resource {
	attrs := append([]*commonpb.KeyValue{strAttr(otelsink.TestIDAttribute, testID)}, extra...)
	return &resourcepb.Resource{Attributes: attrs}
}

func sendGRPCTraces(t *testing.T, endpoint string, req *coltracepb.ExportTraceServiceRequest) {
	t.Helper()
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	_, err = coltracepb.NewTraceServiceClient(conn).Export(context.Background(), req)
	require.NoError(t, err)
}

func postProto(t *testing.T, url string, msg proto.Message) {
	t.Helper()
	body, err := proto.Marshal(msg)
	require.NoError(t, err)
	resp, err := http.Post(url, "application/x-protobuf", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestTraces exercises the trace path over both transports, the trace query
// helpers, and test.id scoping (a foreign resource's span must be invisible).
func TestTraces(t *testing.T) {
	t.Parallel()
	sink := otelsink.Start(t)

	serverSpan := &tracepb.Span{
		Name:       "GET /",
		Kind:       tracepb.Span_SPAN_KIND_SERVER,
		TraceId:    make([]byte, 16),
		SpanId:     make([]byte, 8),
		Attributes: []*commonpb.KeyValue{strAttr("http.request.method", "GET")},
	}
	clientSpan := &tracepb.Span{
		Name:       "SELECT names",
		Kind:       tracepb.Span_SPAN_KIND_CLIENT,
		TraceId:    make([]byte, 16),
		SpanId:     make([]byte, 8),
		Attributes: []*commonpb.KeyValue{strAttr("db.system", "sqlite")},
	}
	scoped := func(spans ...*tracepb.Span) *coltracepb.ExportTraceServiceRequest {
		return &coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
			Resource:   resource(sink.TestID(), strAttr("service.name", "unit-under-test")),
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}},
		}}}
	}

	// Server span over gRPC, client span over HTTP.
	sendGRPCTraces(t, sink.GRPCEndpoint(), scoped(serverSpan))
	postProto(t, sink.HTTPEndpoint()+"/v1/traces", scoped(clientSpan))

	// A different test's telemetry that must never surface in our queries.
	foreign := &coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource:   resource("some-other-test-ffff"),
		ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{Name: "leaked", TraceId: make([]byte, 16), SpanId: make([]byte, 8)}}}},
	}}}
	sendGRPCTraces(t, sink.GRPCEndpoint(), foreign)

	traces := sink.WaitForTraces(t, waitTimeout, func(tr *otelsink.Traces) bool { return tr.Len() >= 2 })

	assert.Equal(t, 2, traces.Len(), "foreign span must be filtered out by test.id")
	assert.ElementsMatch(t, []string{"GET /", "SELECT names"}, traces.Names())
	assert.NotContains(t, traces.Names(), "leaked")

	assert.Equal(t, 1, traces.WithName("GET /").Len())
	assert.Equal(t, 1, traces.WithKind(tracepb.Span_SPAN_KIND_CLIENT).Len())
	assert.Equal(t, 1, traces.WithSpanAttribute("db.system").Len())
	assert.Equal(t, 1, traces.WithSpanAttributeValue("http.request.method", "GET").Len())
	assert.Equal(t, 2, traces.WithResourceAttribute("service.name", "unit-under-test").Len())
	assert.Equal(t, 0, traces.WithSpanAttributeValue("http.request.method", "POST").Len())
}

// TestMetrics exercises the metric path over HTTP and the metric query helpers
// across all three data shapes (sum, gauge, histogram).
func TestMetrics(t *testing.T) {
	t.Parallel()
	sink := otelsink.Start(t)

	sum := &metricspb.Metric{Name: "http.server.request.count", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
		DataPoints: []*metricspb.NumberDataPoint{{
			Attributes: []*commonpb.KeyValue{strAttr("http.response.status_code", "200")},
			Value:      &metricspb.NumberDataPoint_AsInt{AsInt: 3},
		}},
	}}}
	gauge := &metricspb.Metric{Name: "process.memory.usage", Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
		DataPoints: []*metricspb.NumberDataPoint{{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 42.5}}},
	}}}
	histSum := 12.0
	histogram := &metricspb.Metric{Name: "http.server.request.duration", Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
		DataPoints: []*metricspb.HistogramDataPoint{{
			Attributes: []*commonpb.KeyValue{strAttr("http.request.method", "GET")},
			Count:      2, Sum: &histSum,
		}},
	}}}

	postProto(t, sink.HTTPEndpoint()+"/v1/metrics", &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource:     resource(sink.TestID()),
			ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{sum, gauge, histogram}}},
		}},
	})

	metrics := sink.WaitForMetrics(t, waitTimeout, func(m *otelsink.Metrics) bool { return m.Len() >= 3 })

	assert.ElementsMatch(t,
		[]string{"http.server.request.count", "process.memory.usage", "http.server.request.duration"},
		metrics.Names())
	assert.Equal(t, 1, metrics.Sums().Len())
	assert.Equal(t, 1, metrics.Gauges().Len())
	assert.Equal(t, 1, metrics.Histograms().Len())
	assert.Equal(t, 1, metrics.WithName("http.server.request.count").Len())

	counter := metrics.WithName("http.server.request.count").Metrics()
	require.Len(t, counter, 1)
	assert.True(t, counter[0].NumberDataPointAttributes("http.response.status_code"))
	assert.False(t, counter[0].NumberDataPointAttributes("nonexistent"))

	hist := metrics.WithName("http.server.request.duration").Metrics()
	require.Len(t, hist, 1)
	assert.True(t, hist[0].NumberDataPointAttributes("http.request.method"))
	assert.False(t, hist[0].NumberDataPointAttributes("nonexistent"))
}

// TestLogs exercises the log path over gRPC and the log query helpers.
func TestLogs(t *testing.T) {
	t.Parallel()
	sink := otelsink.Start(t)

	info := &logspb.LogRecord{
		SeverityNumber: logspb.SeverityNumber_SEVERITY_NUMBER_INFO,
		Body:           &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "server listening"}},
	}
	errRec := &logspb.LogRecord{
		SeverityNumber: logspb.SeverityNumber_SEVERITY_NUMBER_ERROR,
		Body:           &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "query failed"}},
		Attributes:     []*commonpb.KeyValue{strAttr("exception.type", "OperationalError")},
	}

	conn, err := grpc.NewClient(sink.GRPCEndpoint(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	_, err = collogspb.NewLogsServiceClient(conn).Export(context.Background(), &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource:  resource(sink.TestID()),
			ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{info, errRec}}},
		}},
	})
	require.NoError(t, err)

	logs := sink.WaitForLogs(t, waitTimeout, func(l *otelsink.Logs) bool { return l.Len() >= 2 })

	assert.Equal(t, 2, logs.Len())
	assert.ElementsMatch(t, []string{"server listening", "query failed"}, logs.Bodies())
	assert.Equal(t, 1, logs.WithSeverityAtLeast(logspb.SeverityNumber_SEVERITY_NUMBER_ERROR).Len())
	assert.Equal(t, 2, logs.WithSeverityAtLeast(logspb.SeverityNumber_SEVERITY_NUMBER_INFO).Len())
	assert.Equal(t, 1, logs.WithBodyContaining("failed").Len())
	assert.Equal(t, 1, logs.WithAttribute("exception.type").Len())
}

// TestWaitTimesOutCleanly documents that the wait helpers return the satisfying
// view once telemetry arrives, even when it arrives after the first poll.
func TestWaitForLateArrival(t *testing.T) {
	t.Parallel()
	sink := otelsink.Start(t)

	go func() {
		time.Sleep(400 * time.Millisecond)
		postProto(t, sink.HTTPEndpoint()+"/v1/traces", &coltracepb.ExportTraceServiceRequest{
			ResourceSpans: []*tracepb.ResourceSpans{{
				Resource:   resource(sink.TestID()),
				ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{Name: "late", TraceId: make([]byte, 16), SpanId: make([]byte, 8)}}}},
			}},
		})
	}()

	traces := sink.WaitForTraces(t, waitTimeout, otelsink.NonEmpty)
	assert.Equal(t, []string{"late"}, traces.Names())
}
