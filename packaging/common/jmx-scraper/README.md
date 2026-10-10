# OpenTelemetry JMX scraper

This package runs the OpenTelemetry JMX scraper as a systemd service.
It connects to a configured JMX endpoint and exports metrics through OTLP.

## Configuration

Edit `/etc/opentelemetry/jmx-scraper/config.properties` and set at least `otel.jmx.service.url` and `otel.jmx.target.system`.
The file also supports the scraper's other Java properties, including JMX authentication and OTLP exporter settings.

The optional environment file at `/etc/opentelemetry/jmx-scraper/jmx-scraper.env` can override `JMX_SCRAPER_CONFIG` or set `PATH` to select a Java runtime.

## Service

Enable and start the service after configuring a target.

```sh
systemctl enable --now opentelemetry-jmx-scraper.service
```

The package suggests a Java runtime and does not install or enable the service automatically.
