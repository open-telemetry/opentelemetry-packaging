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

### JMX authentication

When the target JVM requires authentication, set `OTEL_JMX_USERNAME` and `OTEL_JMX_PASSWORD` in `jmx-scraper.env` rather than `/etc/opentelemetry/jmx-scraper/config.properties`.
See [Security](#security) for why credentials belong there.

For SASL profiles, `OTEL_JMX_REMOTE_PROFILE` selects the mechanism and `OTEL_JMX_REALM` supplies the realm that `SASL/DIGEST-MD5` and `TLS SASL/DIGEST-MD5` require.
The scraper installs the SASL provider reflectively, which the Java module system restricts, so the unit sets `JDK_JAVA_OPTIONS=--add-exports=java.security.sasl/com.sun.security.sasl=ALL-UNNAMED` to permit it.
Java 9 to 15 allow the access but log a four-line illegal-access warning, and Java 16 and later refuse it: the scraper then logs `SASL unsupported in current environment` and every SASL profile fails to authenticate.
Override `JDK_JAVA_OPTIONS` in `jmx-scraper.env` only if you keep that export.

> [!NOTE]
> The export is carried in `JDK_JAVA_OPTIONS` rather than on the unit's `ExecStart` line because the scraper JAR targets Java 8 bytecode and this package suggests `java8-runtime` as an alternative runtime.
> On a Java 8 JVM `--add-exports` is an unrecognized option, so putting it on the command line would stop the service from starting at all on a runtime we explicitly support.
> Only the Java 9 and later launcher reads `JDK_JAVA_OPTIONS`, which is exactly the range where the export matters, so Java 8 ignores it harmlessly.
> The option is spelled `--add-exports=...` rather than with a space, because systemd splits an `Environment=` value on whitespace into separate assignments and the JVM would then be handed a bare `--add-exports`.

`otel.jmx.remote.registry.ssl` carries no secret, so it can stay in `config.properties`; set it when the target JVM runs with `com.sun.management.jmxremote.registry.ssl=true`.

### Other properties

`otel.jmx.config` points at custom metric definitions, and `otel.jmx.target.source` chooses where the definitions for `otel.jmx.target.system` come from (`auto`, `instrumentation`, or `legacy`).
See the [upstream jmx-scraper documentation](https://github.com/open-telemetry/opentelemetry-java-contrib/tree/main/jmx-scraper) for the full reference.

The file also accepts the OpenTelemetry Java SDK's autoconfiguration properties, so `otel.exporter.otlp.protocol`, `otel.exporter.otlp.endpoint`, `otel.service.name`, `otel.resource.attributes`, and `otel.metric.export.interval` all work here.
See the [SDK autoconfiguration reference](https://opentelemetry.io/docs/languages/java/configuration/) for that set.

`otel.exporter.otlp.headers` is the exception, because it is where an OTLP API key goes.
Set `OTEL_EXPORTER_OTLP_HEADERS` in `jmx-scraper.env` instead, for the reason given under [Security](#security).

### Environment overrides

The optional environment file at `/etc/opentelemetry/jmx-scraper/jmx-scraper.env` overrides the defaults the unit sets.
Set `JMX_SCRAPER_CONFIG` to keep the configuration somewhere other than the default location.

The unit resolves `java` through `PATH`, so set `PATH` there to choose a specific JVM when the host has several:

```sh
PATH=/opt/myjdk/bin:/usr/bin:/bin
```

Restart the service after editing the file.

## Verifying the configuration

Check that the configured endpoint is reachable before enabling the service:

```sh
sudo systemd-run --wait --pipe --collect --service-type=exec -p EnvironmentFile=-/etc/opentelemetry/jmx-scraper/jmx-scraper.env -p Environment=JDK_JAVA_OPTIONS=--add-exports=java.security.sasl/com.sun.security.sasl=ALL-UNNAMED -p DynamicUser=yes -p ProtectHome=yes /usr/bin/env java -jar /usr/lib/opentelemetry-jmx-scraper/opentelemetry-jmx-scraper.jar -test -config /etc/opentelemetry/jmx-scraper/config.properties
```

The command connects, prints `JMX connection test OK` or `JMX connection test ERROR`, and exits without scraping or exporting anything.
Run it after every change to `config.properties` or `jmx-scraper.env`.

> [!IMPORTANT]
> Running the JAR straight from a shell acts differently than the service, and will report a failure the service would not have.
> The reason is that credentials live in `jmx-scraper.env`, which a plain shell does not read and which is mode 0640, so a non-root operator cannot source it.
> A shell also runs as the invoking user rather than the service's transient one, so it can reach a relocated `JMX_SCRAPER_CONFIG` that the sandboxed service cannot.
> `systemd-run` reproduces the unit's environment and sandbox instead.

## Service

Enable and start the service after configuring a target.

```sh
systemctl enable --now opentelemetry-jmx-scraper.service
```

The package installs the unit without enabling or starting it.

## Security

The service runs under `DynamicUser=yes`, so systemd allocates it a transient unprivileged user at every start.
The scraper reads a configuration file and opens outbound connections, so it needs no privileges of its own.

This is why the two configuration files have different permissions:

- `config.properties` is world-readable, because the service reads it as its transient user. Restricting it stops the service from reading its own configuration.
- `jmx-scraper.env` is installed mode 0640, because systemd reads it as root before dropping privileges. The service's own user cannot read it.

**Put secrets in `jmx-scraper.env`, never in `config.properties`.**
Every scraper property has an environment variable equivalent — `otel.jmx.password` is `OTEL_JMX_PASSWORD` — so credentials and OTLP headers can live entirely in the restricted file.

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
