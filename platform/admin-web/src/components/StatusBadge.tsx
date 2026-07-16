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
    planned: "等待执行",
    provisioning: "准备资源",
    generating: "生成中",
    validating: "验证中",
    completed: "已完成",
    rolling_back: "回滚中",
    rolled_back: "已回滚",
    official: "官方",
    agent: "代理",
    success: "成功",
    denied: "已拒绝",
  };
  return <span className={`status status-${status}`}>{labels[status] ?? status}</span>;
}
