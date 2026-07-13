import type { TablerIcon } from "@tabler/icons-react";

export function StatCard({ icon: Icon, label, value, detail, tone }: { icon: TablerIcon; label: string; value: string; detail: string; tone: string }) {
  return <article className="stat-card"><span className={`stat-icon tone-${tone}`}><Icon size={23} /></span><div><span>{label}</span><strong>{value}</strong><small>{detail}</small></div></article>;
}

