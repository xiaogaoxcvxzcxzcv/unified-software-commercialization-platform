import { IconArrowRight, IconCirclePlus, IconSearch } from "@tabler/icons-react";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useAppContext } from "../app/AppContext";
import { QueryState } from "../components/QueryState";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";

const dateTime = (value: string) => new Date(value).toLocaleString("zh-CN");

export function ProductsPage() {
  const { products, loading, error, refreshProducts } = useAppContext();
  const navigate = useNavigate();
  const [search, setSearch] = useState("");
  const filtered = products.filter((item) => `${item.name}${item.code}${item.id}`.toLowerCase().includes(search.toLowerCase()));
  const open = (productId: string) => navigate(`/products/${encodeURIComponent(productId)}/overview`);
  return <Shell title="软件管理" subtitle="进入已有软件的独立管理工作区">
    <section className="toolbar"><div className="search-field"><IconSearch size={18}/><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索软件名称、代码或 ID" /></div><button className="primary-button" type="button" onClick={() => navigate("/create")}><IconCirclePlus size={18}/>创建软件</button></section>
    <QueryState loading={loading} error={error} onRetry={() => void refreshProducts()} />
    {!loading && !error && <section className="panel table-panel"><div className="table-heading"><div><h2>全部软件</h2><p>共 {filtered.length} 款可访问软件</p></div></div><div className="table-scroll"><table><thead><tr><th>软件</th><th>产品代码</th><th>运行状态</th><th>装配状态</th><th>上下文版本</th><th>更新时间</th><th aria-label="操作" /></tr></thead><tbody>{filtered.map((product) => <tr key={product.id}><td><div className="product-cell"><span>{product.name.slice(0, 1)}</span><div><strong>{product.name}</strong><small>{product.id}</small></div></div></td><td><code>{product.code}</code></td><td><StatusBadge status={product.status}/></td><td><StatusBadge status={product.provisioningState}/></td><td>{product.contextVersion}</td><td>{dateTime(product.updatedAt)}</td><td><button className="table-action" type="button" onClick={() => open(product.id)} aria-label={`进入 ${product.name} 工作区`}><IconArrowRight size={18}/></button></td></tr>)}</tbody></table></div>{filtered.length === 0 && <div className="empty-state"><strong>没有匹配的软件</strong></div>}</section>}
  </Shell>;
}
