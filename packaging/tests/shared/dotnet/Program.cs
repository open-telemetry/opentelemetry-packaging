// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Minimal ASP.NET HTTP server for integration testing.
// Auto-instrumented by the OpenTelemetry .NET agent via the injector.

var builder = WebApplication.CreateBuilder(args);
var app = builder.Build();

app.MapGet("/", () => "OK");

app.Run("http://0.0.0.0:5000");
