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

`otel.jmx.target.system` selects which metric definitions the scraper applies, as one or more comma-separated values from:

<!-- BEGIN GENERATED target systems (make update-jmx-target-systems) -->
- `activemq`
- `camel`
- `cassandra`
- `experimental-cassandra`
- `experimental-kafka-connect`
- `hadoop`
- `hbase`
- `jetty`
- `jvm`
- `kafka`
- `kafka-consumer`
- `kafka-producer`
- `solr`
- `tomcat`
- `wildfly`
<!-- END GENERATED -->

For example, `jvm,tomcat` collects the JVM metrics alongside the Tomcat ones.
That list is what the packaged scraper accepts: a name is valid when the scraper JAR carries either `<name>.yaml` or `jmx/rules/<name>.yaml`.
For what each target system collects, see the [upstream jmx-scraper documentation](https://github.com/open-telemetry/opentelemetry-java-contrib/tree/main/jmx-scraper).

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

## Troubleshooting

The scraper logs to the journal, which is the only place its errors are visible:

```sh
journalctl -u opentelemetry-jmx-scraper.service -f
```

A rejected configuration exits 1 and a missing Java runtime exits 127.
The unit sets `Restart=on-failure`, so systemd restarts the service every five seconds instead of leaving it in the failed state.
`systemctl status` therefore reports a misconfigured service as activating rather than failed.

An unreachable JMX endpoint is not a startup failure.
The scraper keeps running, logs `Failed to establish JMX connection`, and retries until the endpoint answers.
