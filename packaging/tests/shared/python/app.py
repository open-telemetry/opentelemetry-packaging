# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Minimal stdlib-only HTTP server for integration testing.
#
# Auto-instrumented by the OpenTelemetry Python agent, which is activated by
# prepending /usr/lib/opentelemetry/python to PYTHONPATH so the interpreter runs
# the bundled sitecustomize.py at startup. Each GET handler runs an in-memory
# sqlite3 query; the bundled opentelemetry-instrumentation-sqlite3 turns that
# into a database client span. The handler also emits a stdlib logging record,
# which the agent's log auto-instrumentation exports as an OTLP log. The Go test
# drives HTTP traffic and asserts on the exported telemetry via the otel-sink.
#
# Only the standard library is used so the application environment carries no
# OpenTelemetry packages of its own. That keeps the agent's double-instrumentation
# and dependency-conflict safety guards from self-deactivating.

import logging
import sqlite3
from http.server import BaseHTTPRequestHandler, HTTPServer

# When the agent's log auto-instrumentation is enabled it attaches an OTLP
# LoggingHandler to the root logger, so these records are exported as OTLP logs
# without the application importing any OpenTelemetry package.
logger = logging.getLogger("python-testapp")


def run_query():
    conn = sqlite3.connect(":memory:")
    try:
        cur = conn.cursor()
        cur.execute("CREATE TABLE names (id INTEGER PRIMARY KEY, name TEXT)")
        cur.execute("INSERT INTO names (name) VALUES ('opentelemetry')")
        cur.execute("SELECT name FROM names")
        return cur.fetchall()
    finally:
        conn.close()


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        rows = run_query()
        logger.error("request handled: %s", rows)
        body = ("OK {}\n".format(rows)).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    server = HTTPServer(("0.0.0.0", 8080), Handler)
    print("Test server listening on port 8080", flush=True)
    server.serve_forever()
