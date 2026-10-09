# OpenTelemetry Ruby Auto-Instrumentation

This package installs the upstream OpenTelemetry Ruby auto-instrumentation
distribution as a pinned gem closure under `/usr/lib/opentelemetry/ruby/glibc`.

The injector uses `ruby_auto_instrumentation_agent_path_prefix` to set
`RUBYOPT` and `OTEL_RUBY_ADDITIONAL_GEM_PATH` for Ruby processes.

The upstream distribution requires Ruby 3.3 or newer. The native
`google-protobuf` gem is selected for the target package architecture.

The exact shipped dependency closure is available at
`/usr/share/doc/opentelemetry-ruby-autoinstrumentation/Gemfile.lock`.

Ruby auto-instrumentation does not currently consume the OpenTelemetry
file-based declarative configuration, so this package does not ship an
`otel-config.yaml`.
