import { OTLPTraceExporter } from '@opentelemetry/exporter-trace-otlp-http';
import { HttpInstrumentation } from '@opentelemetry/instrumentation-http';
import { resourceFromAttributes } from '@opentelemetry/resources';
import { BatchSpanProcessor, NodeTracerProvider } from '@opentelemetry/sdk-trace-node';

/** What tracing reads from the environment, which the stack sets where the tier traces. */
export interface TracingEnvironment {
  /** The collector's OTLP over HTTP endpoint, such as `http://localhost:4318`; unset, nothing is traced. */
  readonly OTEL_EXPORTER_OTLP_ENDPOINT?: string | undefined;
  /** The service's name in its traces. */
  readonly OTEL_SERVICE_NAME?: string | undefined;
}

/**
 * Starts OpenTelemetry's tracing when `env` names a collector: a span for
 * every request the server takes but the load balancer's health checks, and
 * one for every call it makes over HTTP (DynamoDB's included), sent in
 * batches to the collector, which passes them to X-Ray. Returns the
 * provider, whose `shutdown()` sends what is left; undefined, starting
 * nothing, when `env` names no collector, as in development.
 *
 * It patches `node:http` and `node:https` as they are loaded, so it runs
 * before anything else loads them: `src/start_tracing.ts`, which
 * `src/main.ts` imports first, calls it.
 */
export function startTracing(env: TracingEnvironment): NodeTracerProvider | undefined {
  const endpoint = env.OTEL_EXPORTER_OTLP_ENDPOINT;
  if (endpoint === undefined || endpoint === '') {
    return undefined;
  }
  const provider = new NodeTracerProvider({
    resource: resourceFromAttributes({ 'service.name': env.OTEL_SERVICE_NAME ?? 'service' }),
    spanProcessors: [
      new BatchSpanProcessor(
        new OTLPTraceExporter({ url: `${endpoint.replace(/\/+$/, '')}/v1/traces` }),
      ),
    ],
  });
  provider.register();
  new HttpInstrumentation({
    // The load balancer checks every task every 15 seconds: no trace of it.
    ignoreIncomingRequestHook: (request) => request.url === '/health',
  }).setTracerProvider(provider);
  return provider;
}
