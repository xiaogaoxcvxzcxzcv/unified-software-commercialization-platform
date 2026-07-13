import { IconActivityHeartbeat, IconDatabase, IconServer, IconStack2 } from "@tabler/icons-react";
import { Shell } from "../components/Shell";

const services = [
  { icon: IconServer, name: "Client API", detail: "示例版本 v1 · 示例响应 86ms" },
  { icon: IconDatabase, name: "PostgreSQL", detail: "等待接入真实连接与迁移检查" },
  { icon: IconStack2, name: "任务队列", detail: "等待接入真实积压与失败统计" },
  { icon: IconActivityHeartbeat, name: "审计服务", detail: "等待接入真实写入检测" },
];

export function HealthPage() {
  return <Shell title="系统状态（演示）" subtitle="尚未连接健康检查 API，以下均为界面示例"><div className="feedback" role="status">演示环境：这些卡片不代表 PostgreSQL、任务队列或审计服务的真实状态。</div><section className="health-grid">{services.map(({ icon: Icon, name, detail }) => <article className="panel service-card" key={name}><span><Icon size={22}/></span><div><strong>{name}</strong><small>演示数据 · {detail}</small></div><b>示例正常</b></article>)}</section></Shell>;
}
