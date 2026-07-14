import { IconCirclePlus, IconSearch } from "@tabler/icons-react";
import { FormEvent, useState } from "react";
import { adminClient } from "../api/adminClient";
import { useAppContext } from "../app/AppContext";
import { Modal } from "../components/Modal";
import { QueryState } from "../components/QueryState";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";

export function ProductsPage() {
  const { products, loading, error, refreshProducts } = useAppContext();
  const [search, setSearch] = useState("");
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [code, setCode] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const filtered = products.filter((item) => `${item.name}${item.code}`.toLowerCase().includes(search.toLowerCase()));
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (submitting) return;
    setSubmitting(true);
    setSubmitError(null);
    try {
      await adminClient.createProduct({ name, code: code.toLowerCase() });
      await refreshProducts();
      setOpen(false);
      setName("");
      setCode("");
    } catch (reason) {
      setSubmitError(reason instanceof Error ? reason.message : "创建软件失败，请重试");
    } finally {
      setSubmitting(false);
    }
  };
  return <Shell title="软件管理" subtitle="创建软件并配置独立产品身份">
    <section className="toolbar"><div className="search-field"><IconSearch size={18}/><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索软件名称或代码" /></div><button className="primary-button" type="button" onClick={() => setOpen(true)}><IconCirclePlus size={18}/>创建软件</button></section>
    <QueryState loading={loading} error={error} onRetry={() => void refreshProducts()} />
    {!loading && !error && <section className="panel table-panel"><div className="table-heading"><div><h2>全部软件</h2><p>共 {filtered.length} 款软件</p></div></div><div className="table-scroll"><table><thead><tr><th>软件</th><th>产品代码</th><th>版本</th><th>用户</th><th>启用能力</th><th>状态</th></tr></thead><tbody>{filtered.map((product) => <tr key={product.id}><td><div className="product-cell"><span style={{ background: product.accent }}>{product.name.slice(0, 1)}</span><div><strong>{product.name}</strong><small>{product.id}</small></div></div></td><td><code>{product.code}</code></td><td>{product.version}</td><td>{product.users.toLocaleString()}</td><td>{product.enabledCapabilities.length} 项</td><td><StatusBadge status={product.status}/></td></tr>)}</tbody></table></div></section>}
    <Modal open={open} onClose={() => !submitting && setOpen(false)} title="创建软件"><form className="form" onSubmit={submit}><label>软件名称<input required value={name} onChange={(event) => setName(event.target.value)} placeholder="例如：视频生产大脑" /></label><label>产品代码<input required pattern="[A-Za-z0-9_]+" value={code} onChange={(event) => setCode(event.target.value)} placeholder="例如：VIDEO_BRAIN" /></label><p className="form-note">创建后产品代码不可随意修改，系统会自动建立官方租户。</p>{submitError && <p className="form-error" role="alert">{submitError}</p>}<footer><button className="secondary-button" type="button" disabled={submitting} onClick={() => setOpen(false)}>取消</button><button className="primary-button" type="submit" disabled={submitting}>{submitting ? "创建中..." : "确认创建"}</button></footer></form></Modal>
  </Shell>;
}
