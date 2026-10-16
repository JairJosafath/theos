import { trace } from '@opentelemetry/api';
import { afterEach, describe, expect, it } from 'vitest';

import { startTracing, stopTracing } from './tracing';

afterEach(() => {
  trace.disable();
});

describe('startTracing', () => {
  it('starts nothing when no collector is named, as in development', () => {
    for (const env of [{}, { OTEL_EXPORTER_OTLP_ENDPOINT: '' }]) {
      expect(startTracing(env)).toBeUndefined();
      expect(trace.getTracer('test').startSpan('request').isRecording()).toBe(false);
    }
  });

  it('records spans for the collector the environment names, as in production', async () => {
    const provider = startTracing({
      OTEL_EXPORTER_OTLP_ENDPOINT: 'http://localhost:4318',
      OTEL_SERVICE_NAME: 'orders',
    });
    expect(provider).toBeDefined();
    // Not ended, so nothing is sent to a collector that isn't there.
    expect(trace.getTracer('test').startSpan('request').isRecording()).toBe(true);
    await provider?.shutdown();
  });
});

describe('stopTracing', () => {
  it('logs, and never rejects, when the last spans can’t be sent', async () => {
    const logged: Readonly<Record<string, unknown>>[] = [];
    const gone = { shutdown: () => Promise.reject(new Error('connect ECONNREFUSED')) };

    await expect(stopTracing(gone, (entry) => logged.push(entry))).resolves.toBeUndefined();

    expect(logged).toEqual([
      expect.objectContaining({ level: 'ERROR', error: 'connect ECONNREFUSED' }),
    ]);
  });

  it('does nothing where the tier doesn’t trace', async () => {
    const logged: unknown[] = [];
    await stopTracing(undefined, (entry) => logged.push(entry));
    expect(logged).toEqual([]);
  });
});
