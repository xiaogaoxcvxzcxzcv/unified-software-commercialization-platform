export function QueryState({ loading, error, onRetry }: { loading: boolean; error: string | null; onRetry: () => void }) {
  if (loading) return <div className="query-state" role="status">正在加载当前范围数据...</div>;
  if (error) return <div className="query-state query-error" role="alert"><span>{error}</span><button className="secondary-button" type="button" onClick={onRetry}>重试</button></div>;
  return null;
}
