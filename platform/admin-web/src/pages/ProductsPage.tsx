import { IconCirclePlus, IconSearch } from "@tabler/icons-react";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useAppContext } from "../app/AppContext";
import { QueryState } from "../components/QueryState";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";

export function ProductsPage() {
  const { products, loading, error, refreshProducts } = useAppContext();
  const navigate = useNavigate();
  const [search, setSearch] = useState("");
  const filtered = products.filter((item) => `${item.name}${item.code}`.toLowerCase().includes(search.toLowerCase()));
  return <Shell title="软件管理" subtitle="创建软件并配置独立产品身份">
    <section className="toolbar"><div className="search-field"><IconSearch size={18}/><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索软件名称或代码" /></div><button className="primary-button" type="button" onClick={() => navigate("/create")}><IconCirclePlus size={18}/>创建软件</button></section>
    <QueryState loading={loading} error={error} onRetry={() => void refreshProducts()} />
    {!loading && !error && <section className="panel table-panel"><div className="table-heading"><div><h2>全部软件</h2><p>共 {filtered.length} 款软件</p></div></div><div className="table-scroll"><table><thead><tr><th>软件</th><th>产品代码</th><th>版本</th><th>用户</th><th>启用能力</th><th>状态</th></tr></thead><tbody>{filtered.map((product) => <tr key={product.id}><td><div className="product-cell"><span style={{ background: product.accent }}>{product.name.slice(0, 1)}</span><div><strong>{product.name}</strong><small>{product.id}</small></div></div></td><td><code>{product.code}</code></td><td>{product.version}</td><td>{product.users.toLocaleString()}</td><td>{product.enabledCapabilities.length} 项</td><td><StatusBadge status={product.status}/></td></tr>)}</tbody></table></div></section>}
  </Shell>;
}
