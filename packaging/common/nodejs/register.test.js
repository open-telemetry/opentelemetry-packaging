// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Unit tests for register.js, the entry point the injector loads with
// `node --require`.
//
// The tests stage a throwaway copy of register.js next to a fake node_modules
// tree and run it in a child process, exactly as the injector does. The fake
// packages stand in for @opentelemetry/auto-instrumentations-node and
// @opentelemetry/sdk-node and print markers when they are loaded, so each test
// asserts on which entry point register.js actually reached. Nothing here
// downloads from npm or needs a container.
//
// Run with: make nodejs-unit-tests
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

const REGISTER_SOURCE = path.join(__dirname, 'register.js');

// Markers the fake packages print when one of their entry points is reached.
const REGISTER_HOOK = 'MARK:register-hook';
const START_NODE_SDK = 'MARK:start-node-sdk';
const INSTRUMENTATIONS = 'MARK:instrumentations';

const WARN_PREFIX = '[opentelemetry-nodejs-autoinstrumentation] WARN:';

function writePackage(modulesDir, name, pkgJson, files) {
  const dir = path.join(modulesDir, ...name.split('/'));
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(
    path.join(dir, 'package.json'),
    JSON.stringify({ name, version: '0.0.0-test', ...pkgJson })
  );
  for (const [relPath, contents] of Object.entries(files)) {
    const target = path.join(dir, relPath);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, contents);
  }
}

// Stages an installation tree: register.js plus a node_modules/ beside it.
//
// registerFile  the path, inside the fake auto-instrumentations-node package,
//               that its "./register" export points at. Varying it proves
//               register.js follows the exports map rather than a file layout.
// sdkNode       'ok' | 'no-export' | 'missing'
function stage({ registerFile = 'dist/register.cjs', sdkNode = 'ok' } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'otel-register-test-'));
  fs.copyFileSync(REGISTER_SOURCE, path.join(root, 'register.js'));

  const modules = path.join(root, 'node_modules');

  writePackage(
    modules,
    '@opentelemetry/auto-instrumentations-node',
    {
      main: './dist/index.cjs',
      exports: {
        '.': { require: { default: './dist/index.cjs' } },
        './register': { require: { default: `./${registerFile}` } },
      },
    },
    {
      'dist/index.cjs':
        'module.exports = { getNodeAutoInstrumentations: () => {' +
        `  console.log(${JSON.stringify(INSTRUMENTATIONS)});` +
        '  return "fake-instrumentations";' +
        '} };\n',
      [registerFile]: `console.log(${JSON.stringify(REGISTER_HOOK)});\n`,
    }
  );

  if (sdkNode !== 'missing') {
    const startNodeSDK =
      sdkNode === 'no-export'
        ? ''
        : 'startNodeSDK: (config) => {' +
          `  console.log(${JSON.stringify(START_NODE_SDK)}, JSON.stringify(config.instrumentations));` +
          '  return { shutdown: () => Promise.resolve() };' +
          '},';
    writePackage(
      modules,
      '@opentelemetry/sdk-node',
      { main: './index.cjs' },
      { 'index.cjs': `module.exports = { ${startNodeSDK} };\n` }
    );
  }

  return root;
}

// Loads the staged register.js into an otherwise empty Node process, the way
// the injector's --require does, and returns what it printed.
function run(root, env = {}) {
  const result = spawnSync(
    process.execPath,
    ['--require', path.join(root, 'register.js'), '-e', ''],
    {
      encoding: 'utf8',
      // A clean environment: the real one may carry OTEL_* variables that
      // would route register.js down the other branch.
      env: { PATH: process.env.PATH, ...env },
    }
  );
  return { status: result.status, stdout: result.stdout, stderr: result.stderr };
}

test('without OTEL_CONFIG_FILE it loads the upstream register hook', () => {
  const { status, stdout, stderr } = run(stage());

  assert.equal(status, 0);
  assert.match(stdout, new RegExp(REGISTER_HOOK));
  assert.doesNotMatch(stdout, new RegExp(START_NODE_SDK));
  assert.equal(stderr, '');
});

test('it follows the "./register" export, not the file layout behind it', () => {
  // 0.82.0 moved the hook from build/src/register.js to dist/register.cjs
  // while keeping the "./register" subpath export. Any future move is the
  // same shape of change, so the test renames the file behind the export.
  const { status, stdout } = run(stage({ registerFile: 'lib/register-moved.cjs' }));

  assert.equal(status, 0);
  assert.match(stdout, new RegExp(REGISTER_HOOK));
});

test('with OTEL_CONFIG_FILE it boots startNodeSDK with the auto-instrumentations', () => {
  const root = stage();
  const configFile = path.join(root, 'otel-config.yaml');
  fs.writeFileSync(configFile, 'file_format: "1.0-rc.1"\n');

  const { status, stdout, stderr } = run(root, { OTEL_CONFIG_FILE: configFile });

  assert.equal(status, 0);
  assert.match(stdout, new RegExp(START_NODE_SDK));
  assert.match(stdout, new RegExp(INSTRUMENTATIONS));
  // The declarative path must not also run the env-var register hook.
  assert.doesNotMatch(stdout, new RegExp(REGISTER_HOOK));
  assert.equal(stderr, '');
});

test('it warns and keeps the process alive when startNodeSDK is not exported', () => {
  const root = stage({ sdkNode: 'no-export' });

  const { status, stdout, stderr } = run(root, { OTEL_CONFIG_FILE: '/nonexistent.yaml' });

  assert.equal(status, 0, 'a broken SDK must not kill the host process');
  assert.ok(stderr.startsWith(WARN_PREFIX), `unexpected stderr: ${stderr}`);
  assert.doesNotMatch(stdout, new RegExp(START_NODE_SDK));
});

test('it warns and keeps the process alive when @opentelemetry/sdk-node is absent', () => {
  // Resolution failures are part of what the declarative branch degrades on:
  // an unresolvable package must warn, not abort the --require phase.
  const root = stage({ sdkNode: 'missing' });

  const { status, stderr } = run(root, { OTEL_CONFIG_FILE: '/nonexistent.yaml' });

  assert.equal(status, 0, 'a missing SDK package must not kill the host process');
  assert.ok(stderr.startsWith(WARN_PREFIX), `unexpected stderr: ${stderr}`);
});

test('a broken installation fails loudly on the env-var path', () => {
  // Deliberate asymmetry with the declarative branch above: without
  // OTEL_CONFIG_FILE there is no fallback to degrade to, and an
  // auto-instrumentations-node that cannot be resolved means the package
  // itself is broken, so the failure is not swallowed.
  const root = stage();
  fs.rmSync(path.join(root, 'node_modules', '@opentelemetry', 'auto-instrumentations-node'), {
    recursive: true,
  });

  const { status, stderr } = run(root);

  assert.notEqual(status, 0);
  assert.match(stderr, /Cannot find module/);
});
