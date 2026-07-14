import IconBriefcase2 from "@tabler/icons-react/dist/esm/icons/IconBriefcase2.mjs";
import IconPlus from "@tabler/icons-react/dist/esm/icons/IconPlus.mjs";
import IconTrash from "@tabler/icons-react/dist/esm/icons/IconTrash.mjs";
import { useRef, useState } from "react";
import type { FormEvent } from "react";
import type { CustomRoute } from "../../integration/routes";
import "../workbench.css";

function Workbench() {
  const [items, setItems] = useState([{ id: "item-1", text: "梳理今天的工作" }, { id: "item-2", text: "检查交付结果" }]);
  const [draft, setDraft] = useState("");
  const nextItem = useRef(3);
  const add = (event: FormEvent) => { event.preventDefault(); const value = draft.trim(); if (!value) return; setItems((current) => [...current, { id: `item-${nextItem.current++}`, text: value }]); setDraft(""); };
  return <section className="workbench-panel">
    <header><div><span>自定义业务</span><h2>工作台</h2></div><strong>{items.length} 项</strong></header>
    <form onSubmit={add}><label htmlFor="work-item">新事项</label><div><input id="work-item" placeholder="输入事项名称" value={draft} onChange={(event) => setDraft(event.target.value)} /><button type="submit" disabled={!draft.trim()} aria-label="添加事项" title="添加事项"><IconPlus /></button></div></form>
    <ul>{items.map((item) => <li key={item.id}><span>{item.text}</span><button type="button" aria-label={`删除 ${item.text}`} title="删除事项" onClick={() => setItems((current) => current.filter((value) => value.id !== item.id))}><IconTrash /></button></li>)}</ul>
  </section>;
}

const route: CustomRoute = { id: "custom.workbench", label: "工作台", icon: <IconBriefcase2 />, Component: Workbench };
export default route;
