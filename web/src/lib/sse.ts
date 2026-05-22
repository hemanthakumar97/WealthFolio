import { useEffect, useRef, useCallback } from 'react';

export interface SSEEvent {
  type: string;
  data: unknown;
}

type Handler = (data: unknown) => void;

/**
 * useSSE opens a persistent EventSource to /api/events and lets callers
 * subscribe to named event types. Re-connects automatically on error.
 *
 * Usage:
 *   const { on, off } = useSSE();
 *   useEffect(() => {
 *     const unsub = on('backfill', (data) => console.log(data));
 *     return unsub;
 *   }, [on]);
 */
export function useSSE() {
  const esRef = useRef<EventSource | null>(null);
  const handlersRef = useRef<Map<string, Set<Handler>>>(new Map());

  const connect = useCallback(() => {
    if (esRef.current) return;
    const es = new EventSource('/api/events', { withCredentials: true });
    esRef.current = es;

    es.onerror = () => {
      es.close();
      esRef.current = null;
      // Reconnect after 3 s.
      setTimeout(connect, 3000);
    };

    // Generic message dispatcher — each SSE event has an event: <type> line.
    const types = ['backfill', 'prices', 'snapshot', 'import'];
    types.forEach((t) => {
      es.addEventListener(t, (ev: MessageEvent) => {
        const handlers = handlersRef.current.get(t);
        if (!handlers) return;
        try {
          const data = JSON.parse(ev.data);
          handlers.forEach((h) => h(data));
        } catch {
          handlers.forEach((h) => h(ev.data));
        }
      });
    });
  }, []);

  useEffect(() => {
    connect();
    return () => {
      esRef.current?.close();
      esRef.current = null;
    };
  }, [connect]);

  const on = useCallback((type: string, handler: Handler) => {
    if (!handlersRef.current.has(type)) {
      handlersRef.current.set(type, new Set());
    }
    handlersRef.current.get(type)!.add(handler);
    return () => handlersRef.current.get(type)?.delete(handler);
  }, []);

  return { on };
}
