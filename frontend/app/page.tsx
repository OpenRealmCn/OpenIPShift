'use client';

import { FormEvent, useEffect, useState } from 'react';

type Node = { id: string; name: string; region: string; instance: string; staticIP: string; dnsName: string; proxyTarget: string };
type Task = { id: string; nodeId: string; phase: string; mode: string; releaseAfter: string; events: { at: string; phase: string; message: string }[] };
type State = { nodes: Node[]; tasks: Record<string, Task> };

// Browser requests stay same-origin; only the server knows the backend address.
const API = '';
const emptyNode: Node = { id: '', name: '', region: '', instance: '', staticIP: '', dnsName: '', proxyTarget: '' };

export default function Page() {
  const [state, setState] = useState<State>({ nodes: [], tasks: {} });
  const [token, setToken] = useState('');
  const [draftToken, setDraftToken] = useState('');
  const [authenticated, setAuthenticated] = useState(false);
  const [node, setNode] = useState<Node>(emptyNode);
  const [editing, setEditing] = useState<string | null>(null);
  const [error, setError] = useState('');

  async function request(path: string, init: RequestInit = {}) {
    const headers = new Headers(init.headers);
    headers.set('Authorization', `Bearer ${token}`);
    if (init.body) headers.set('Content-Type', 'application/json');
    const response = await fetch(API + path, { ...init, headers });
    if (!response.ok) throw new Error(await response.text());
    return response.status === 204 ? null : response.json();
  }

  async function refresh() {
    if (!authenticated || !token) return;
    try {
      setState(await request('/api/state'));
      setError('');
    } catch (e) {
      setAuthenticated(false);
      setState({ nodes: [], tasks: {} });
      setError('Token 无效或后端不可用');
    }
  }

  useEffect(() => {
    if (!authenticated) return;
    refresh();
    const id = setInterval(refresh, 2000);
    return () => clearInterval(id);
  }, [authenticated, token]);

  async function login(e: FormEvent) {
    e.preventDefault();
    if (!draftToken) return;
    const previous = token;
    setToken(draftToken);
    try {
      const headers = { Authorization: `Bearer ${draftToken}` };
      const response = await fetch(API + '/api/state', { headers });
      if (!response.ok) throw new Error('unauthorized');
      setState(await response.json());
      setAuthenticated(true);
      setError('');
    } catch {
      setToken(previous);
      setError('Token 无效或后端不可用');
    }
  }

  function logout() {
    setToken('');
    setDraftToken('');
    setAuthenticated(false);
    setState({ nodes: [], tasks: {} });
    setNode(emptyNode);
    setEditing(null);
  }

  async function saveNode(e: FormEvent) {
    e.preventDefault();
    try {
      if (editing) await request(`/api/nodes/${encodeURIComponent(editing)}`, { method: 'PUT', body: JSON.stringify(node) });
      else await request('/api/nodes', { method: 'POST', body: JSON.stringify(node) });
      setNode(emptyNode);
      setEditing(null);
      await refresh();
    } catch (e) { setError(String(e)); }
  }

  async function deleteNode(id: string) {
    if (!window.confirm('删除这个节点配置？历史任务会保留。')) return;
    try { await request(`/api/nodes/${encodeURIComponent(id)}`, { method: 'DELETE' }); await refresh(); }
    catch (e) { setError(String(e)); }
  }

  async function rotate(nodeId: string) {
    try {
      await request('/api/rotations', { method: 'POST', headers: { 'Idempotency-Key': crypto.randomUUID() }, body: JSON.stringify({ nodeId }) });
      await refresh();
    } catch (e) { setError(String(e)); }
  }

  if (!authenticated) return <main><h1>OpenIPShift · IPv4 轮换控制台</h1><aside><strong>访问 Token</strong>：Token 仅保存在当前页面内存，退出或刷新页面后清除。</aside><form onSubmit={login} className="login"><label>访问 Token<input type="password" value={draftToken} onChange={e => setDraftToken(e.target.value)} autoComplete="off" required /></label><button type="submit">登录</button></form>{error && <p role="alert">{error}</p>}</main>;

  return <main>
    <header><h1>OpenIPShift · IPv4 轮换控制台</h1><button onClick={logout}>退出</button></header>
    <aside><strong>MOCK / 模拟模式</strong>：不会修改 AWS / Cloudflare，出口及代理协议验证均为模拟。节点数量由你配置。</aside>
    {error && <p role="alert">{error}</p>}
    <section><h2>{editing ? '编辑节点' : '新增节点'}</h2><form className="node-form" onSubmit={saveNode}>
      {([['id', '节点 ID'], ['name', '名称'], ['region', 'AWS 区域'], ['instance', 'Lightsail 实例名'], ['staticIP', '当前静态 IPv4'], ['dnsName', '节点 DNS'], ['proxyTarget', '代理目标 host:port']] as const).map(([key, label]) => <label key={key}>{label}<input value={node[key]} disabled={editing !== null && key === 'id'} onChange={e => setNode({ ...node, [key]: e.target.value })} required /></label>)}
      <span><button type="submit">{editing ? '保存修改' : '添加节点'}</button>{editing && <button type="button" onClick={() => { setEditing(null); setNode(emptyNode); }}>取消</button>}</span>
    </form></section>
    <h2>节点（{state.nodes.length}）</h2><table><thead><tr><th>名称</th><th>区域</th><th>静态 IPv4</th><th>DNS A</th><th>操作</th></tr></thead><tbody>{state.nodes.map(n => { const busy = Object.values(state.tasks).some(t => t.nodeId === n.id && !['completed', 'rolled_back'].includes(t.phase)); return <tr key={n.id}><td>{n.name}<small>{n.id}</small></td><td>{n.region}</td><td>{n.staticIP}</td><td>{n.dnsName}</td><td><button onClick={() => rotate(n.id)} disabled={busy}>模拟轮换</button><button onClick={() => { setEditing(n.id); setNode(n); }} disabled={busy}>编辑</button><button onClick={() => deleteNode(n.id)} disabled={busy}>删除</button></td></tr>; })}</tbody></table>
    <h2>任务与进度日志</h2>{Object.values(state.tasks).sort((a, b) => b.id.localeCompare(a.id)).map(t => <section key={t.id}><h3>{t.nodeId} · {t.phase}</h3><small>{t.id} · {t.mode}</small>{t.phase === 'cooling' && <p>旧 IP 保留至 {new Date(t.releaseAfter).toLocaleString()} 后再复验。等待 TTL 不保证旧连接恢复。</p>}<ol>{t.events.map((event, i) => <li key={i}><time>{new Date(event.at).toLocaleString()}</time> [{event.phase}] {event.message}</li>)}</ol></section>)}
  </main>;
}
