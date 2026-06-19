// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Minimal HTTP server for integration testing.
// Auto-instrumented by the OpenTelemetry Node.js agent via the injector.

const http = require("http");

const server = http.createServer((req, res) => {
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end("OK\n");
});

server.listen(3000, () => {
  console.log("Test server listening on port 3000");
});
