import IconBriefcase2 from "@tabler/icons-react/dist/esm/icons/IconBriefcase2.mjs";
import IconPlus from "@tabler/icons-react/dist/esm/icons/IconPlus.mjs";
import IconTrash from "@tabler/icons-react/dist/esm/icons/IconTrash.mjs";
import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import type { CustomRoute } from "../../integration/routes";
import "../workbench.css";

function Workbench() {
  const [items, setItems] = useState([{ id: "item-1", text: "梳理今天的工作" }, { id: "item-2", text: "检查交付结果" }]);
  const [draft, setDraft] = useState("");
  const [focusAfterDelete, setFocusAfterDelete] = useState<string | null>(null);
  const nextItem = useRef(3);
  const inputRef = useRef<HTMLInputElement>(null);
  const deleteButtons = useRef(new Map<string, HTMLButtonElement>());
  const add = (event: FormEvent) => { event.preventDefault(); const value = draft.trim(); if (!value) return; setItems((current) => [...current, { id: `item-${nextItem.current++}`, text: value }]); setDraft(""); };
  useEffect(() => {
    if (!focusAfterDelete) return;
    if (focusAfterDelete === "input") inputRef.current?.focus();
    else deleteButtons.current.get(focusAfterDelete)?.focus();
    setFocusAfterDelete(null);
  }, [focusAfterDelete, items]);
  const remove = (index: number) => {
    const nextFocus = items[index + 1]?.id ?? items[index - 1]?.id ?? "input";
    setFocusAfterDelete(nextFocus);
    setItems((current) => current.filter((_, itemIndex) => itemIndex !== index));
  };
  return <section className="workbench-panel">
    <header><div><span>自定义业务</span><h2>工作台</h2></div><strong aria-live="polite" aria-atomic="true">{items.length} 项</strong></header>
    <form onSubmit={add}><label htmlFor="work-item">新事项</label><div><input ref={inputRef} id="work-item" type="text" maxLength={160} placeholder="输入事项名称" value={draft} onChange={(event) => setDraft(event.target.value)} /><button type="submit" disabled={!draft.trim()} aria-label="添加事项" title="添加事项"><IconPlus aria-hidden="true" /></button></div></form>
    {items.length === 0 ? <p className="workbench-empty" role="status">暂无事项，可以从上方添加</p> : <ul>{items.map((item, index) => <li key={item.id}><span id={`${item.id}-label`}>{item.text}</span><button ref={(node) => { if (node) deleteButtons.current.set(item.id, node); else deleteButtons.current.delete(item.id); }} type="button" aria-label={`删除第 ${index + 1} 项`} aria-describedby={`${item.id}-label`} title="删除事项" onClick={() => remove(index)}><IconTrash aria-hidden="true" /></button></li>)}</ul>}
  </section>;
}

const route: CustomRoute = { id: "custom.workbench", label: "工作台", icon: <IconBriefcase2 />, Component: Workbench };
export default route;
