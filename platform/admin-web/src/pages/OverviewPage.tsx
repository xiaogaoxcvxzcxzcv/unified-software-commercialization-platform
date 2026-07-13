import {
  IconActivity,
  IconApps,
  IconBuildingStore,
  IconKey,
  IconRefresh,
  IconShieldCheck,
  IconUsers,
} from "@tabler/icons-react";
import { useMemo } from "react";
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { useAppContext } from "../app/AppContext";
import { Shell } from "../components/Shell";
import { StatCard } from "../components/StatCard";

const activity = [
  { day: "周一", users: 520, checks: 940 }, { day: "周二", users: 638, checks: 1080 },
  { day: "周三", users: 592, checks: 1020 }, { day: "周四", users: 760, checks: 1280 },
  { day: "周五", users: 842, checks: 1510 }, { day: "周六", users: 710, checks: 1240 },
  { day: "周日", users: 786, checks: 1380 },
];

export function OverviewPage() {
  const { products, currentProduct, refreshProducts } = useAppContext();
  const totals = useMemo(() => ({ users: products.reduce((sum, item) => sum + item.users, 0), active: products.reduce((sum, item) => sum + item.activeUsers, 0) }), [products]);
  const title = currentProduct ? currentProduct.name : "平台总览";
  const subtitle = currentProduct ? `${currentProduct.code} · ${currentProduct.version} · 产品级汇总（跨租户） · 演示数据` : "全部软件的演示接入概况";
  const userValue = currentProduct?.users ?? totals.users;
  const activeValue = currentProduct?.activeUsers ?? totals.active;

  return <Shell title={title} subtitle={subtitle}>
    <section className="toolbar"><div><span className="eyebrow">演示概况</span><strong>当前页面未连接真实监控服务</strong></div><button className="secondary-button" type="button" onClick={() => void refreshProducts()}><IconRefresh size={18} />刷新演示数据</button></section>
    <section className="stats-grid">
      <StatCard icon={IconApps} label="软件" value={currentProduct ? "1" : String(products.length)} detail={currentProduct ? "当前上下文" : `${products.filter((item) => item.status === "active").length} 款运行中`} tone="blue" />
      <StatCard icon={IconUsers} label={currentProduct ? "产品总用户（跨租户）" : "用户"} value={userValue.toLocaleString()} detail={`${activeValue.toLocaleString()} 位活跃用户`} tone="teal" />
      <StatCard icon={IconKey} label="权益检查（演示）" value="12,680" detail="示例通过率 98.7%" tone="purple" />
      <StatCard icon={IconBuildingStore} label="代理租户（演示）" value={currentProduct ? "3" : "7"} detail="示例隔离状态" tone="green" />
    </section>
    <section className="dashboard-layout">
      <article className="panel chart-panel">
        <div className="panel-title"><div><h2>用户与权益趋势</h2><p>最近 7 天活跃用户和权益检查次数</p></div><span className="status status-active">按天</span></div>
        <div className="chart-box"><ResponsiveContainer width="100%" height="100%"><AreaChart data={activity} margin={{ top: 8, right: 8, left: -22, bottom: 0 }}><defs><linearGradient id="users" x1="0" y1="0" x2="0" y2="1"><stop offset="5%" stopColor="#0f9f8f" stopOpacity={0.22}/><stop offset="95%" stopColor="#0f9f8f" stopOpacity={0}/></linearGradient></defs><CartesianGrid stroke="#e7eeed" vertical={false}/><XAxis dataKey="day" axisLine={false} tickLine={false} tick={{ fill: "#718096", fontSize: 12 }}/><YAxis axisLine={false} tickLine={false} tick={{ fill: "#718096", fontSize: 12 }}/><Tooltip contentStyle={{ borderRadius: 8, border: "1px solid #dfe9e7", boxShadow: "0 8px 20px rgba(30, 60, 55, .08)" }}/><Area type="monotone" dataKey="checks" stroke="#5b62e8" strokeWidth={2} fill="transparent" name="权益检查"/><Area type="monotone" dataKey="users" stroke="#0f9f8f" strokeWidth={2.5} fill="url(#users)" name="活跃用户"/></AreaChart></ResponsiveContainer></div>
      </article>
      <article className="panel operations-panel"><div className="panel-title"><div><h2>运行检查示例</h2><p>演示状态，不代表真实服务检测结果</p></div><IconShieldCheck size={22} color="#0f9f8f" /></div><ul className="health-list"><li><IconActivity size={19}/><div><strong>产品数据隔离</strong><span>演示检查结果</span></div><b>示例正常</b></li><li><IconActivity size={19}/><div><strong>租户范围校验</strong><span>演示检查结果</span></div><b>示例正常</b></li><li><IconActivity size={19}/><div><strong>Client API</strong><span>演示响应时间 86ms</span></div><b>示例正常</b></li><li><IconActivity size={19}/><div><strong>审计写入</strong><span>演示积压任务 0</span></div><b>示例正常</b></li></ul></article>
    </section>
    <section className="panel capabilities-panel"><div className="panel-title"><div><h2>{currentProduct ? "已启用统一能力" : "软件接入概况"}</h2><p>{currentProduct ? "公共能力按当前软件独立配置" : "每款软件使用独立产品身份和能力配置"}</p></div></div><div className="capability-grid">{(currentProduct?.enabledCapabilities ?? ["统一账号", "权益", "代理租户", "设备", "激活码", "云存储"]).map((item, index) => <div className="capability-item" key={item}><span className={`capability-index color-${index % 4}`}>{index + 1}</span><div><strong>{item}</strong><small>服务端授权 · 独立配置</small></div><span className="status status-active">已启用</span></div>)}</div></section>
  </Shell>;
}
