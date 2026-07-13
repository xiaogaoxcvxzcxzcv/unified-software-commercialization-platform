import { useCallback, useEffect, useRef, useState } from "react";

export function useScopedQuery<T>(scopeKey: string | null, fetcher: () => Promise<T>, initialValue: T) {
  const fetcherRef = useRef(fetcher);
  const requestRef = useRef(0);
  const [data, setData] = useState<T>(initialValue);
  const [loading, setLoading] = useState(Boolean(scopeKey));
  const [error, setError] = useState<string | null>(null);
  const [revision, setRevision] = useState(0);
  fetcherRef.current = fetcher;

  const retry = useCallback(() => setRevision((value) => value + 1), []);

  useEffect(() => {
    const requestId = ++requestRef.current;
    if (!scopeKey) {
      setData(initialValue);
      setLoading(false);
      setError(null);
      return;
    }
    setData(initialValue);
    setLoading(true);
    setError(null);
    void fetcherRef.current().then((result) => {
      if (requestRef.current !== requestId) return;
      setData(result);
      setLoading(false);
    }).catch((reason: unknown) => {
      if (requestRef.current !== requestId) return;
      setError(reason instanceof Error ? reason.message : "加载失败，请重试");
      setLoading(false);
    });
    return () => { requestRef.current += 1; };
  }, [scopeKey, revision]);

  return { data, loading, error, retry };
}
