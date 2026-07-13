import { IconCopy, IconDeviceFloppy } from "@tabler/icons-react";
import { useEffect, useState } from "react";
import { adminClient } from "../api/adminClient";
import { useAppContext } from "../app/AppContext";
import { Shell } from "../components/Shell";

const capabilities = ["统一账号", "权益", "设备", "激活码", "代理租户", "订单支付", "版本更新", "云存储"];

export function SettingsPage() {
  const { currentProduct, refreshProducts } = useAppContext();
  const [name, setName] = useState("");
  const [version, setVersion] = useState("");
  const [enabled, setEnabled] = useState<Set<string>>(new Set());
  const [saving, setSaving] = useState(false);
  const [capabilitySaving, setCapabilitySaving] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setName(currentProduct?.name ?? "");
    setVersion(currentProduct?.version ?? "");
    setEnabled(new Set(currentProduct?.enabledCapabilities ?? []));
    setMessage(null);
    setError(null);
  }, [currentProduct?.id]);

  const saveProduct = async () => {
    if (!currentProduct || saving) return;
    setSaving(true);
    setError(null);
    setMessage(null);
    try {
      await adminClient.updateProduct(currentProduct.id, { name: name.trim(), version: version.trim() });
      await refreshProducts();
      setMessage("产品信息已保存到演示 Client");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "保存失败，请重试");
    } finally {
      setSaving(false);
    }
  };

  const toggle = async (capability: string) => {
    if (!currentProduct || capabilitySaving) return;
    const previous = new Set(enabled);
    const next = new Set(enabled);
    next.has(capability) ? next.delete(capability) : next.add(capability);
    setEnabled(next);
    setCapabilitySaving(true);
    setError(null);
    setMessage(null);
    try {
      await adminClient.updateCapabilities(currentProduct.id, [...next]);
      await refreshProducts();
      setMessage(`${capability}能力配置已保存`);
    } catch (reason) {
      setEnabled(previous);
      setError(reason instanceof Error ? reason.message : "能力配置保存失败，请重试");
    } finally {
      setCapabilitySaving(false);
    }
  };

  return <Shell title="产品设置" subtitle={`${currentProduct?.name ?? "当前软件"} · 身份、接入与能力开关`}>
    {(message || error) && <div className={error ? "feedback feedback-error" : "feedback feedback-success"} role="status">{error ?? message}</div>}
    <section className="settings-grid">
      <article className="panel settings-panel"><div className="panel-title"><div><h2>基本信息</h2><p>产品代码创建后保持稳定</p></div></div><div className="form inline-form"><label>软件名称<input value={name} onChange={(event) => setName(event.target.value)} /></label><label>产品代码<div className="copy-field"><input readOnly value={currentProduct?.code ?? ""}/><button type="button" title="复制产品代码" onClick={() => void navigator.clipboard.writeText(currentProduct?.code ?? "")}><IconCopy size={17}/></button></div></label><label>当前版本<input value={version} onChange={(event) => setVersion(event.target.value)} /></label><button className="primary-button" type="button" disabled={saving || !name.trim() || !version.trim()} onClick={() => void saveProduct()}><IconDeviceFloppy size={18}/>{saving ? "保存中..." : "保存信息"}</button></div></article>
      <article className="panel settings-panel"><div className="panel-title"><div><h2>能力开关</h2><p>保存后目录同步更新，真实服务端仍需执行能力校验</p></div></div><div className="toggle-list">{capabilities.map((capability) => <label key={capability}><div><strong>{capability}</strong><span>统一能力 · 当前软件独立配置</span></div><input type="checkbox" disabled={capabilitySaving} checked={enabled.has(capability)} onChange={() => void toggle(capability)}/><i /></label>)}</div></article>
    </section>
  </Shell>;
}
