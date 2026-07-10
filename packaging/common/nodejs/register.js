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
  // Any throw here (missing/invalid config file, experimental startNodeSDK
  // API drift) would otherwise abort the --require phase and kill the host
  // process before the application starts; degrade to uninstrumented instead.
  try {
    const { startNodeSDK } = require(path.join(modules, '@opentelemetry/sdk-node'));
    const { getNodeAutoInstrumentations } = require(
      path.join(modules, '@opentelemetry/auto-instrumentations-node')
    );
    if (typeof startNodeSDK !== 'function') {
      throw new TypeError('startNodeSDK is not exported by @opentelemetry/sdk-node');
    }
    const sdk = startNodeSDK({ instrumentations: [getNodeAutoInstrumentations()] });
    if (!sdk || typeof sdk.shutdown !== 'function') {
      throw new TypeError('startNodeSDK did not return an SDK with a shutdown() method');
    }
    // Bound the flush: a hung exporter must not delay process exit forever.
    const flush = () =>
      Promise.race([
        sdk.shutdown().catch(() => {}),
        new Promise((resolve) => setTimeout(resolve, 3000).unref()),
      ]);
    process.on('beforeExit', () => {
      flush();
    });
    // `once`, and re-raise after the flush settles: a persistent listener
    // would remove Node's default terminate behavior and make the application
    // ignore SIGTERM (systemd stop, docker stop) entirely. When the
    // application has its own SIGTERM handlers, they received the original
    // signal and own the process lifetime — re-raising would deliver the
    // signal to them a second time.
    process.once('SIGTERM', () => {
      flush().then(() => {
        if (process.listenerCount('SIGTERM') === 0) {
          process.kill(process.pid, 'SIGTERM');
        }
      });
    });
  } catch (err) {
    process.stderr.write(
      '[opentelemetry-nodejs-autoinstrumentation] WARN: cannot auto-instrument Node.js process: ' +
        (err && err.message ? err.message : String(err)) +
        '\n'
    );
  }
} else {
  require(
    path.join(modules, '@opentelemetry/auto-instrumentations-node/build/src/register.js')
  );
}
