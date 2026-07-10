// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otelsink

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
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

// readLines returns each JSONL line from a signal file, or nil if the file does
// not exist yet (no telemetry has arrived).
func readLines(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("otelsink: open %s: %v", path, err)
	}
	defer f.Close()

	var lines [][]byte
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		b := scanner.Bytes()
		if len(strings.TrimSpace(string(b))) == 0 {
			continue
		}
		lines = append(lines, append([]byte(nil), b...))
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("otelsink: read %s: %v", path, err)
	}
	return lines
}

func unmarshalEach[T proto.Message](t *testing.T, path string, newMsg func() T) []T {
	t.Helper()
	var out []T
	for _, line := range readLines(t, path) {
		msg := newMsg()
		if err := protojson.Unmarshal(line, msg); err != nil {
			t.Fatalf("otelsink: parse %s: %v", path, err)
		}
		out = append(out, msg)
	}
	return out
}

// --- attribute helpers ---

func findAttr(attrs []*commonpb.KeyValue, key string) (*commonpb.AnyValue, bool) {
	for _, kv := range attrs {
		if kv.GetKey() == key {
			return kv.GetValue(), true
		}
	}
	return nil, false
}

// AttrString renders an attribute value as a string for equality checks. It
// covers the scalar value kinds; composite kinds render via their proto text.
func AttrString(v *commonpb.AnyValue) string {
	switch val := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return val.StringValue
	case *commonpb.AnyValue_BoolValue:
		if val.BoolValue {
			return "true"
		}
		return "false"
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(val.IntValue, 10)
	case *commonpb.AnyValue_DoubleValue:
		return strconv.FormatFloat(val.DoubleValue, 'g', -1, 64)
	default:
		return strings.TrimSpace(protojson.Format(v))
	}
}

func hasAttr(attrs []*commonpb.KeyValue, key string) bool {
	_, ok := findAttr(attrs, key)
	return ok
}

func attrEquals(attrs []*commonpb.KeyValue, key, value string) bool {
	v, ok := findAttr(attrs, key)
	return ok && AttrString(v) == value
}

// --- traces ---

// SpanView is a span together with the resource and scope that produced it.
type SpanView struct {
	Resource *resourcepb.Resource
	Scope    *commonpb.InstrumentationScope
	Span     *tracepb.Span
}

// Traces is a queryable, test-scoped view over exported spans.
type Traces struct{ spans []SpanView }

// Traces returns all spans currently on disk that carry this sink's test.id.
func (s *Sink) Traces(t *testing.T) *Traces {
	t.Helper()
	var spans []SpanView
	reqs := unmarshalEach(t, filepath.Join(s.dir, tracesFile), func() *coltracepb.ExportTraceServiceRequest {
		return &coltracepb.ExportTraceServiceRequest{}
	})
	for _, req := range reqs {
		for _, rs := range req.GetResourceSpans() {
			if !attrEquals(rs.GetResource().GetAttributes(), TestIDAttribute, s.testID) {
				continue
			}
			for _, ss := range rs.GetScopeSpans() {
				for _, span := range ss.GetSpans() {
					spans = append(spans, SpanView{Resource: rs.GetResource(), Scope: ss.GetScope(), Span: span})
				}
			}
		}
	}
	return &Traces{spans: spans}
}

// Spans returns the spans in this view.
func (tr *Traces) Spans() []SpanView { return tr.spans }

// Len returns the number of spans in this view.
func (tr *Traces) Len() int { return len(tr.spans) }

func (tr *Traces) filter(keep func(SpanView) bool) *Traces {
	var out []SpanView
	for _, sv := range tr.spans {
		if keep(sv) {
			out = append(out, sv)
		}
	}
	return &Traces{spans: out}
}

// WithName keeps spans whose name equals name.
func (tr *Traces) WithName(name string) *Traces {
	return tr.filter(func(sv SpanView) bool { return sv.Span.GetName() == name })
}

// WithKind keeps spans of the given kind.
func (tr *Traces) WithKind(kind tracepb.Span_SpanKind) *Traces {
	return tr.filter(func(sv SpanView) bool { return sv.Span.GetKind() == kind })
}

// WithSpanAttribute keeps spans that carry the given span attribute key.
func (tr *Traces) WithSpanAttribute(key string) *Traces {
	return tr.filter(func(sv SpanView) bool { return hasAttr(sv.Span.GetAttributes(), key) })
}

// WithSpanAttributeValue keeps spans whose span attribute key renders to value.
func (tr *Traces) WithSpanAttributeValue(key, value string) *Traces {
	return tr.filter(func(sv SpanView) bool { return attrEquals(sv.Span.GetAttributes(), key, value) })
}

// WithResourceAttribute keeps spans whose resource attribute key renders to value.
func (tr *Traces) WithResourceAttribute(key, value string) *Traces {
	return tr.filter(func(sv SpanView) bool { return attrEquals(sv.Resource.GetAttributes(), key, value) })
}

// Names returns the names of the spans in this view.
func (tr *Traces) Names() []string {
	out := make([]string, 0, len(tr.spans))
	for _, sv := range tr.spans {
		out = append(out, sv.Span.GetName())
	}
	return out
}

// --- metrics ---

// MetricView is a metric together with the resource and scope that produced it.
type MetricView struct {
	Resource *resourcepb.Resource
	Scope    *commonpb.InstrumentationScope
	Metric   *metricspb.Metric
}

// Metrics is a queryable, test-scoped view over exported metrics.
type Metrics struct{ metrics []MetricView }

// Metrics returns all metrics currently on disk that carry this sink's test.id.
func (s *Sink) Metrics(t *testing.T) *Metrics {
	t.Helper()
	var metrics []MetricView
	reqs := unmarshalEach(t, filepath.Join(s.dir, metricsFile), func() *colmetricspb.ExportMetricsServiceRequest {
		return &colmetricspb.ExportMetricsServiceRequest{}
	})
	for _, req := range reqs {
		for _, rm := range req.GetResourceMetrics() {
			if !attrEquals(rm.GetResource().GetAttributes(), TestIDAttribute, s.testID) {
				continue
			}
			for _, sm := range rm.GetScopeMetrics() {
				for _, metric := range sm.GetMetrics() {
					metrics = append(metrics, MetricView{Resource: rm.GetResource(), Scope: sm.GetScope(), Metric: metric})
				}
			}
		}
	}
	return &Metrics{metrics: metrics}
}

// Metrics returns the metrics in this view.
func (m *Metrics) Metrics() []MetricView { return m.metrics }

// Len returns the number of metrics in this view.
func (m *Metrics) Len() int { return len(m.metrics) }

func (m *Metrics) filter(keep func(MetricView) bool) *Metrics {
	var out []MetricView
	for _, mv := range m.metrics {
		if keep(mv) {
			out = append(out, mv)
		}
	}
	return &Metrics{metrics: out}
}

// WithName keeps metrics whose name equals name.
func (m *Metrics) WithName(name string) *Metrics {
	return m.filter(func(mv MetricView) bool { return mv.Metric.GetName() == name })
}

// Sums keeps sum (counter) metrics.
func (m *Metrics) Sums() *Metrics {
	return m.filter(func(mv MetricView) bool { return mv.Metric.GetSum() != nil })
}

// Gauges keeps gauge metrics.
func (m *Metrics) Gauges() *Metrics {
	return m.filter(func(mv MetricView) bool { return mv.Metric.GetGauge() != nil })
}

// Histograms keeps histogram metrics.
func (m *Metrics) Histograms() *Metrics {
	return m.filter(func(mv MetricView) bool { return mv.Metric.GetHistogram() != nil })
}

// Names returns the names of the metrics in this view.
func (m *Metrics) Names() []string {
	out := make([]string, 0, len(m.metrics))
	for _, mv := range m.metrics {
		out = append(out, mv.Metric.GetName())
	}
	return out
}

// NumberDataPointAttributes returns whether any Sum/Gauge/Histogram datapoint
// on the metric carries the given attribute key. It is a convenience for
// asserting on datapoint-level attributes without unpacking the metric union
// by hand.
func (mv MetricView) NumberDataPointAttributes(key string) bool {
	var dps []*metricspb.NumberDataPoint
	switch {
	case mv.Metric.GetSum() != nil:
		dps = mv.Metric.GetSum().GetDataPoints()
	case mv.Metric.GetGauge() != nil:
		dps = mv.Metric.GetGauge().GetDataPoints()
	case mv.Metric.GetHistogram() != nil:
		for _, dp := range mv.Metric.GetHistogram().GetDataPoints() {
			if hasAttr(dp.GetAttributes(), key) {
				return true
			}
		}
		return false
	}
	for _, dp := range dps {
		if hasAttr(dp.GetAttributes(), key) {
			return true
		}
	}
	return false
}

// --- logs ---

// LogView is a log record together with the resource and scope that produced it.
type LogView struct {
	Resource *resourcepb.Resource
	Scope    *commonpb.InstrumentationScope
	Record   *logspb.LogRecord
}

// Logs is a queryable, test-scoped view over exported log records.
type Logs struct{ records []LogView }

// Logs returns all log records currently on disk that carry this sink's test.id.
func (s *Sink) Logs(t *testing.T) *Logs {
	t.Helper()
	var records []LogView
	reqs := unmarshalEach(t, filepath.Join(s.dir, logsFile), func() *collogspb.ExportLogsServiceRequest {
		return &collogspb.ExportLogsServiceRequest{}
	})
	for _, req := range reqs {
		for _, rl := range req.GetResourceLogs() {
			if !attrEquals(rl.GetResource().GetAttributes(), TestIDAttribute, s.testID) {
				continue
			}
			for _, sl := range rl.GetScopeLogs() {
				for _, record := range sl.GetLogRecords() {
					records = append(records, LogView{Resource: rl.GetResource(), Scope: sl.GetScope(), Record: record})
				}
			}
		}
	}
	return &Logs{records: records}
}

// Records returns the log records in this view.
func (l *Logs) Records() []LogView { return l.records }

// Len returns the number of log records in this view.
func (l *Logs) Len() int { return len(l.records) }

func (l *Logs) filter(keep func(LogView) bool) *Logs {
	var out []LogView
	for _, lv := range l.records {
		if keep(lv) {
			out = append(out, lv)
		}
	}
	return &Logs{records: out}
}

// WithSeverityAtLeast keeps records whose severity number is >= min.
func (l *Logs) WithSeverityAtLeast(min logspb.SeverityNumber) *Logs {
	return l.filter(func(lv LogView) bool { return lv.Record.GetSeverityNumber() >= min })
}

// WithBodyContaining keeps records whose string body contains substr.
func (l *Logs) WithBodyContaining(substr string) *Logs {
	return l.filter(func(lv LogView) bool {
		return strings.Contains(AttrString(lv.Record.GetBody()), substr)
	})
}

// WithAttribute keeps records that carry the given log attribute key.
func (l *Logs) WithAttribute(key string) *Logs {
	return l.filter(func(lv LogView) bool { return hasAttr(lv.Record.GetAttributes(), key) })
}

// Bodies returns the string bodies of the records in this view.
func (l *Logs) Bodies() []string {
	out := make([]string, 0, len(l.records))
	for _, lv := range l.records {
		out = append(out, AttrString(lv.Record.GetBody()))
	}
	return out
}
