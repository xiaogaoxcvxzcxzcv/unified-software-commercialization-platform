export function StatusBadge({ status }: { status: string }) {
  const labels: Record<string, string> = {
    active: "正常",
    paused: "已停用",
    trial: "试用中",
    expired: "已到期",
    locked: "已锁定",
    revoked: "已撤销",
    suspended: "已暂停",
    pending: "装配中",
    ready: "已就绪",
    failed: "装配失败",
    official: "官方",
    agent: "代理",
    success: "成功",
    denied: "已拒绝",
  };
  return <span className={`status status-${status}`}>{labels[status] ?? status}</span>;
}
