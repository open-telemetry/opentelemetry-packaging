# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Export one trace via the pure-Python gRPC transport (_pygrpc).

Driven by the Go harness in pyprotogrpc_test.go: builds an OTLP trace export
request with the vendored pyproto message classes (no google-protobuf, no
grpcio) and sends it over _pygrpc to the endpoint given on the command line.
"""

import argparse
import os
import sys

from opentelemetry._proto.collector.trace.v1.trace_service_pb2 import (
    ExportTraceServiceRequest,
)
from opentelemetry._proto.common.v1.common_pb2 import AnyValue, KeyValue
from opentelemetry._proto.resource.v1.resource_pb2 import Resource
from opentelemetry._proto.trace.v1.trace_pb2 import ResourceSpans, ScopeSpans, Span

from opentelemetry.exporter.otlp._proto.grpc._pygrpc.client import (
    Channel,
    Compression,
)

TRACE_SERVICE_EXPORT = (
    "/opentelemetry.proto.collector.trace.v1.TraceService/Export"
)


def build_request(span_name, scenario, test_id):
    attributes = [
        KeyValue(key="probe.scenario", value=AnyValue(string_value=scenario)),
    ]
    if scenario == "large":
        # Push the request well past the 64 KiB initial flow-control window
        # so the transport must handle WINDOW_UPDATE frames mid-body.
        attributes.append(
            KeyValue(
                key="probe.padding",
                value=AnyValue(string_value="x" * (256 * 1024)),
            )
        )
    span = Span(
        trace_id=os.urandom(16),
        span_id=os.urandom(8),
        name=span_name,
        kind=2,  # SERVER
        start_time_unix_nano=1,
        end_time_unix_nano=2,
        attributes=attributes,
    )
    return ExportTraceServiceRequest(
        resource_spans=[
            ResourceSpans(
                resource=Resource(
                    attributes=[
                        KeyValue(
                            key="service.name",
                            value=AnyValue(string_value="pyprotogrpc-probe"),
                        ),
                        # otelsink scopes every query to this attribute.
                        KeyValue(
                            key="test.id",
                            value=AnyValue(string_value=test_id),
                        ),
                    ]
                ),
                scope_spans=[ScopeSpans(spans=[span])],
            )
        ]
    )


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--endpoint", required=True)
    parser.add_argument("--span-name", required=True)
    parser.add_argument("--test-id", required=True)
    parser.add_argument(
        "--scenario", choices=("basic", "gzip", "large"), default="basic"
    )
    args = parser.parse_args()

    request = build_request(args.span_name, args.scenario, args.test_id)
    compression = (
        Compression.Gzip if args.scenario == "gzip" else Compression.NoCompression
    )

    channel = Channel(args.endpoint, use_tls=False)
    try:
        response = channel.unary_call(
            TRACE_SERVICE_EXPORT,
            request.SerializeToString(),
            timeout=10.0,
            compression=compression,
        )
    finally:
        channel.close()

    print("OK response_bytes={}".format(len(response)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
