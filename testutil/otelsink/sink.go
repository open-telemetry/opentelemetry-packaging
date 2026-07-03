// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package otelsink provides an in-process OTLP sink for integration tests.
//
// A Sink accepts OTLP over both gRPC (an "OTLP/gRPC" endpoint) and HTTP (an
// "OTLP/HTTP protobuf" endpoint) on ephemeral ports, and appends every received
// Export request to a per-signal file (traces.jsonl, metrics.jsonl, logs.jsonl)
// as one protojson object per line. Those files are the source of truth for the
// assertion API in query.go and wait.go, so the same assertions work whether the
// sink runs in the test process (container tests) or as a host process a VM
// exports to.
//
// Each Sink carries a unique test.id. Workloads stamp it on their resource via
// OTEL_RESOURCE_ATTRIBUTES (see Env), and every query is scoped to it, so a
// single shared sink never lets one test observe another's telemetry.
package otelsink

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// Signal file names written under the sink's directory.
const (
	tracesFile  = "traces.jsonl"
	metricsFile = "metrics.jsonl"
	logsFile    = "logs.jsonl"
)

// TestIDAttribute is the resource attribute key used to attribute telemetry to
// the test that produced it.
const TestIDAttribute = "test.id"

// hostGateway is the DNS name testcontainers exposes inside a container for
// reaching host ports declared via ContainerRequest.HostAccessPorts.
const hostGateway = "host.testcontainers.internal"

// Sink is a running in-process OTLP receiver. Create one with Start.
type Sink struct {
	testID   string
	dir      string
	grpcPort int
	httpPort int

	grpcServer *grpc.Server
	httpServer *http.Server

	mu sync.Mutex // serializes appends across concurrent gRPC/HTTP handlers
}

// Start launches a sink on ephemeral gRPC and HTTP ports, writing signal files
// into a per-test temporary directory. The sink is shut down automatically when
// the test finishes.
func Start(t *testing.T) *Sink {
	t.Helper()

	s := &Sink{
		testID: newTestID(t.Name()),
		dir:    t.TempDir(),
	}

	grpcLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("otelsink: listen gRPC: %v", err)
	}
	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = grpcLn.Close()
		t.Fatalf("otelsink: listen HTTP: %v", err)
	}
	s.grpcPort = grpcLn.Addr().(*net.TCPAddr).Port
	s.httpPort = httpLn.Addr().(*net.TCPAddr).Port

	s.grpcServer = grpc.NewServer()
	coltracepb.RegisterTraceServiceServer(s.grpcServer, &traceService{sink: s})
	colmetricspb.RegisterMetricsServiceServer(s.grpcServer, &metricsService{sink: s})
	collogspb.RegisterLogsServiceServer(s.grpcServer, &logsService{sink: s})
	go func() { _ = s.grpcServer.Serve(grpcLn) }()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", s.httpHandler(func() proto.Message { return &coltracepb.ExportTraceServiceRequest{} }, tracesFile, &coltracepb.ExportTraceServiceResponse{}))
	mux.HandleFunc("/v1/metrics", s.httpHandler(func() proto.Message { return &colmetricspb.ExportMetricsServiceRequest{} }, metricsFile, &colmetricspb.ExportMetricsServiceResponse{}))
	mux.HandleFunc("/v1/logs", s.httpHandler(func() proto.Message { return &collogspb.ExportLogsServiceRequest{} }, logsFile, &collogspb.ExportLogsServiceResponse{}))
	s.httpServer = &http.Server{Handler: mux}
	go func() { _ = s.httpServer.Serve(httpLn) }()

	t.Cleanup(func() {
		s.grpcServer.Stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(shutdownCtx)
	})

	return s
}

// TestID returns the unique identifier stamped on this sink's telemetry.
func (s *Sink) TestID() string { return s.testID }

// Dir returns the directory holding the per-signal files.
func (s *Sink) Dir() string { return s.dir }

// GRPCEndpoint returns the host:port for an OTLP/gRPC client running on the host
// (e.g., in-process tests).
func (s *Sink) GRPCEndpoint() string { return fmt.Sprintf("127.0.0.1:%d", s.grpcPort) }

// HTTPEndpoint returns the base URL for an OTLP/HTTP client running on the host.
func (s *Sink) HTTPEndpoint() string { return fmt.Sprintf("http://127.0.0.1:%d", s.httpPort) }

// HostAccessPorts returns the host ports a testcontainers ContainerRequest must
// expose (via HostAccessPorts) so a containerized workload can reach the sink at
// host.testcontainers.internal.
func (s *Sink) HostAccessPorts() []int { return []int{s.grpcPort, s.httpPort} }

// Env returns the OTLP environment for a containerized workload: an OTLP/HTTP
// protobuf endpoint reachable from inside the container, and the per-test
// resource attribute. Merge these with the workload's own variables (e.g.,
// OTEL_SERVICE_NAME, exporter selection).
func (s *Sink) Env() map[string]string {
	return map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": fmt.Sprintf("http://%s:%d", hostGateway, s.httpPort),
		"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
		"OTEL_RESOURCE_ATTRIBUTES":    fmt.Sprintf("%s=%s", TestIDAttribute, s.testID),
	}
}

// writeSignal appends one Export request to the given signal file as a single
// protojson line.
func (s *Sink) writeSignal(file string, msg proto.Message) error {
	line, err := protojson.Marshal(msg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(s.dir, file), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

// httpHandler returns an OTLP/HTTP handler that decodes an Export request
// (protobuf or protojson), appends it to the given signal file, and replies with
// an empty success response in the client's content type.
func (s *Sink) httpHandler(newReq func() proto.Message, file string, resp proto.Message) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		contentType := r.Header.Get("Content-Type")
		req := newReq()
		if strings.HasPrefix(contentType, "application/json") {
			err = protojson.Unmarshal(body, req)
		} else {
			err = proto.Unmarshal(body, req)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.writeSignal(file, req); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if strings.HasPrefix(contentType, "application/json") {
			out, _ := protojson.Marshal(resp)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(out)
			return
		}
		out, _ := proto.Marshal(resp)
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(out)
	}
}

// --- gRPC service handlers ---

type traceService struct {
	coltracepb.UnimplementedTraceServiceServer
	sink *Sink
}

func (t *traceService) Export(_ context.Context, req *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	if err := t.sink.writeSignal(tracesFile, req); err != nil {
		return nil, err
	}
	return &coltracepb.ExportTraceServiceResponse{}, nil
}

type metricsService struct {
	colmetricspb.UnimplementedMetricsServiceServer
	sink *Sink
}

func (m *metricsService) Export(_ context.Context, req *colmetricspb.ExportMetricsServiceRequest) (*colmetricspb.ExportMetricsServiceResponse, error) {
	if err := m.sink.writeSignal(metricsFile, req); err != nil {
		return nil, err
	}
	return &colmetricspb.ExportMetricsServiceResponse{}, nil
}

type logsService struct {
	collogspb.UnimplementedLogsServiceServer
	sink *Sink
}

func (l *logsService) Export(_ context.Context, req *collogspb.ExportLogsServiceRequest) (*collogspb.ExportLogsServiceResponse, error) {
	if err := l.sink.writeSignal(logsFile, req); err != nil {
		return nil, err
	}
	return &collogspb.ExportLogsServiceResponse{}, nil
}

// newTestID builds a unique test identifier from the test name plus a short
// random suffix, so repeated runs and parallel subtests never collide.
func newTestID(name string) string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	sanitized := strings.NewReplacer("/", "_", " ", "_").Replace(name)
	return fmt.Sprintf("%s-%s", sanitized, hex.EncodeToString(buf[:]))
}
