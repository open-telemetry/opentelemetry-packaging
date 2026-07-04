// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Entry point loaded via `node --require` (by the injector or manually).
//
// The upstream auto-instrumentations-node register hook only supports
// environment-variable configuration and silently ignores OTEL_CONFIG_FILE.
// startNodeSDK (experimental, @opentelemetry/sdk-node) is the entry point that
// implements declarative configuration: it reads OTEL_CONFIG_FILE itself, via
// @opentelemetry/configuration, and builds every provider from the parsed
// file instead of from environment variables.
//
// This wrapper therefore only routes: with OTEL_CONFIG_FILE set, boot through
// startNodeSDK (passing the auto-instrumentations, which the file cannot
// express); without it, defer to the upstream register hook unchanged.
'use strict';

const path = require('path');
const modules = path.join(__dirname, 'node_modules');

if (process.env.OTEL_CONFIG_FILE) {
  const { startNodeSDK } = require(path.join(modules, '@opentelemetry/sdk-node'));
  const { getNodeAutoInstrumentations } = require(
    path.join(modules, '@opentelemetry/auto-instrumentations-node')
  );
  const sdk = startNodeSDK({ instrumentations: [getNodeAutoInstrumentations()] });
  const shutdown = () => {
    sdk.shutdown().catch(() => {});
  };
  process.on('beforeExit', shutdown);
  process.on('SIGTERM', shutdown);
} else {
  require(
    path.join(modules, '@opentelemetry/auto-instrumentations-node/build/src/register.js')
  );
}
