# OpenTelemetry JMX scraper

This package runs the OpenTelemetry JMX scraper as a systemd service.
It connects to a configured JMX endpoint and exports metrics through OTLP.

## Requirements

The scraper needs a Java runtime, which this package does not install.
The runtime is a suggested dependency rather than a required one, so `apt` and `dnf` do not pull it in automatically.

On Debian and Ubuntu:

```sh
apt install default-jre
```

On RPM-based distributions:

```sh
dnf install java-headless
```

The service unit resolves `java` through the `PATH` of the service, so whichever JVM is first on that `PATH` is used.
Without one, the service fails to start with `env: java: No such file or directory`.

## Configuration

Edit `/etc/opentelemetry/jmx-scraper/config.properties` and set at least `otel.jmx.service.url` and `otel.jmx.target.system`.
The file also supports the scraper's other Java properties, including JMX authentication and OTLP exporter settings.

The optional environment file at `/etc/opentelemetry/jmx-scraper/jmx-scraper.env` can override `JMX_SCRAPER_CONFIG` or set `PATH` to select a Java runtime.

## Verifying the configuration

Check that the configured endpoint is reachable before enabling the service:

```sh
java -jar /usr/lib/opentelemetry-jmx-scraper/opentelemetry-jmx-scraper.jar -test -config /etc/opentelemetry/jmx-scraper/config.properties
```

The command connects, prints `JMX connection test OK` or `JMX connection test ERROR`, and exits without scraping or exporting anything.
Run it after every change to `config.properties`.

## Service

Enable and start the service after configuring a target.

```sh
systemctl enable --now opentelemetry-jmx-scraper.service
```

The package installs the unit without enabling or starting it.
