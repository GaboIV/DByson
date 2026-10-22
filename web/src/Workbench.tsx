import { useCallback, useEffect, useRef, useState } from "react";
import { api, type Connection, type Session } from "./api";
import { Connections } from "./Connections";
import { Workspace, type WorkspaceView } from "./Workspace";
import { Brand } from "./Brand";
import { usePreferences, PreferencesDialog, QueryHistoryDialog } from "./Preferences";
import { Dialog } from "./Dialog";
import { TabStrip, type TabItem } from "./TabStrip";
const viewLabels:Partial<Record<WorkspaceView,string>>={data:"Contenido",structure:"Estructura",modify:"Modificar",insert:"Nuevo registro",sql:"Consulta SQL",export:"Exportar"};
interface WorkTab {id:string;connectionId:string;database:string;table:string;sql:boolean;view:WorkspaceView;title:string;dirty?:boolean;groupId:string;groupTable:string;groupDatabase:string}
function scope(c:string,db:string,table:string){return `table:${c}:${encodeURIComponent(db)}:${encodeURIComponent(table)}`;}
function restore(key:string):WorkTab[]{
 try{const values=JSON.parse(localStorage.getItem(key)||"[]");if(!Array.isArray(values))return [];
 return values.filter(t=>t&&typeof t.id==="string"&&typeof t.connectionId==="string"&&typeof t.database==="string"&&typeof t.table==="string"&&typeof t.sql==="boolean").slice(0,48).map(t=>{
 const view=["browse","data","structure","modify","insert","sql","export"].includes(t.view)?t.view:t.sql?"sql":t.table?"data":"browse";
 const groupTable=typeof t.groupTable==="string"?t.groupTable:t.table,groupDatabase=typeof t.groupDatabase==="string"?t.groupDatabase:t.database;
 return {...t,view,dirty:false,groupTable,groupDatabase,groupId:groupTable?scope(t.connectionId,groupDatabase,groupTable):t.id};});
 }catch{return [];}
}
export function Workbench({session,onLogout}:{session:Session;onLogout:()=>void}){
 const {preferences}=usePreferences();const storageKey=`dbyson:${session.username}`;
 const [tabs,setTabs]=useState<WorkTab[]>(()=>restore(storageKey+":tabs"));
 const [active,setActive]=useState("home"),[visited,setVisited]=useState<string[]>([]),[connections,setConnections]=useState<Connection[]>([]),[palette,setPalette]=useState(false),[search,setSearch]=useState(""),[error,setError]=useState(""),[newConnection,setNewConnection]=useState(0),[settings,setSettings]=useState(false),[history,setHistory]=useState(false);
 const seeds=useRef<Record<string,string>>({}),lastPane=useRef<Record<string,string>>({});
 const refresh=useCallback(async(signal?:AbortSignal)=>{try{setConnections(await api<Connection[]>("GET","/api/connections",undefined,{signal}));}catch(e){if(!signal?.aborted)setError(e instanceof Error?e.message:"No se pudieron cargar las conexiones");}},[]);
 useEffect(()=>{const c=new AbortController();void refresh(c.signal);return()=>c.abort();},[refresh]);
 useEffect(()=>{try{localStorage.setItem(storageKey+":tabs",JSON.stringify(tabs.map(({dirty,...t})=>t)));}catch{}},[tabs,storageKey]);
 useEffect(()=>{const c=new AbortController();if(active==="home"||palette)void refresh(c.signal);return()=>c.abort();},[active,palette,refresh]);
 const groups=[...new Map(tabs.map(t=>[t.groupId,tabs.find(p=>p.groupId===t.groupId)!])).values()];
 const current=tabs.find(t=>t.id===active),currentConnection=connections.find(c=>c.id===current?.connectionId);
 function activate(id:string){const pane=tabs.find(t=>t.id===id);if(pane)lastPane.current[pane.groupId]=id;setActive(id);if(id!=="home")setVisited(old=>old.includes(id)?old:[...old,id]);setError("");}
 function open(c:Connection,database=c.database,table="",view:WorkspaceView="browse",query?:string,parent?:WorkTab){
  setConnections(old=>[...old.filter(x=>x.id!==c.id),c]);
  const groupId=parent?.groupId||(table?scope(c.id,database,table):"");
  const match=view!=="sql"&&tabs.find(t=>groupId?t.groupId===groupId&&t.view===view:t.connectionId===c.id&&t.database===database&&!t.table&&t.view===view);
  if(match){activate(match.id);return;}
  if(tabs.length>=48||(!tabs.some(t=>t.groupId===groupId)&&groups.length>=16)){setError("Cierra una pestaña para continuar (máximo 16 principales y 48 vistas).");return;}
  const id=crypto.randomUUID();if(query)seeds.current[id]=query;
  const pane:WorkTab={id,connectionId:c.id,database,table,view,sql:view==="sql",title:view==="sql"?"Consulta SQL":table||database||c.name,groupId:groupId||id,groupTable:parent?.groupTable??table,groupDatabase:parent?.groupDatabase??database};
  setTabs(old=>[...old,pane]);lastPane.current[pane.groupId]=id;activate(id);
 }
 function newSQL(){if(current&&currentConnection)open(currentConnection,current.database,current.groupTable,"sql",undefined,current.groupTable?current:undefined);}
 function remove(ids:string[]){
  if(tabs.some(t=>ids.includes(t.id)&&t.dirty)&&!window.confirm("Esta pestaña tiene cambios sin guardar. ¿Cerrarla y descartar los borradores?"))return;
  const kept=tabs.filter(t=>!ids.includes(t.id));setTabs(kept);setVisited(old=>old.filter(id=>!ids.includes(id)));for(const id of ids)delete seeds.current[id];
  if(ids.includes(active)){const i=tabs.findIndex(t=>t.id===active),neighbors=[...tabs.slice(0,i).reverse(),...tabs.slice(i+1)].filter(t=>!ids.includes(t.id));activate((neighbors.find(t=>t.groupId===current?.groupId)||neighbors[0])?.id||"home");}
 }
 function closeGroup(groupId:string){const children=tabs.filter(t=>t.groupId===groupId);const index=groups.findIndex(g=>g.groupId===groupId);const neighbor=groups[index-1]||groups[index+1];
  if(children.some(t=>t.dirty)&&!window.confirm("Esta pestaña tiene cambios sin guardar. ¿Cerrarla y descartar los borradores de sus vistas?"))return;
  const ids=children.map(t=>t.id);setTabs(old=>old.filter(t=>t.groupId!==groupId));setVisited(old=>old.filter(id=>!ids.includes(id)));for(const id of ids)delete seeds.current[id];delete lastPane.current[groupId];
  if(current?.groupId===groupId)activate(neighbor?(tabs.some(t=>t.id===lastPane.current[neighbor.groupId])?lastPane.current[neighbor.groupId]:neighbor.id):"home");
 }
 useEffect(()=>{const handler=(e:KeyboardEvent)=>{if(!palette&&[...document.querySelectorAll<HTMLElement>('[role="dialog"]')].some(el=>el.getClientRects().length))return;if((e.ctrlKey||e.metaKey)&&e.key.toLowerCase()==="k"){e.preventDefault();setPalette(p=>!p);setSearch("");}if((e.ctrlKey||e.metaKey)&&e.altKey&&e.key.toLowerCase()==="n"){e.preventDefault();newSQL();}};const unload=(e:BeforeUnloadEvent)=>{if(tabs.some(t=>t.dirty))e.preventDefault();};window.addEventListener("keydown",handler);window.addEventListener("beforeunload",unload);return()=>{window.removeEventListener("keydown",handler);window.removeEventListener("beforeunload",unload);};},[tabs,active,connections,palette]);
 function navigation(pane:WorkTab,c:Connection){
  if(!pane.groupTable)return undefined;
  const siblings=tabs.filter(t=>t.groupId===pane.groupId);
  const fixed:WorkspaceView[]=["data","structure",...(!c.readOnly&&!preferences.viewOnly?["modify","insert"] as WorkspaceView[]:[])];
  if((pane.view==="modify"||pane.view==="insert")&&!fixed.includes(pane.view))fixed.push(pane.view);
  const items:TabItem[]=fixed.map(view=>{const target=siblings.find(t=>t.view===view);return {id:view,label:viewLabels[view]!,control:target?`panel-${target.id}`:undefined,dirty:target?.dirty,onSelect:()=>target?activate(target.id):open(c,pane.groupDatabase,pane.groupTable,view,undefined,pane)};});
  let queryNumber=0;for(const child of siblings.filter(t=>!fixed.includes(t.view))){const label=child.view==="sql"?`Consulta ${++queryNumber}`:viewLabels[child.view]||child.title;items.push({id:child.id,label,control:`panel-${child.id}`,dirty:child.dirty,onSelect:()=>activate(child.id),onClose:()=>remove([child.id])});}
  return <TabStrip label="Vistas de la tabla" className="table-tab-strip" idPrefix={`view-${pane.id}`} active={fixed.includes(pane.view)?pane.view:pane.id} items={items}/>;
 }
 const mainItems:TabItem[]=[{id:"home",label:"Conexiones",leading:"⌂",control:"panel-home",onSelect:()=>activate("home")},...groups.map(g=>{const c=connections.find(c=>c.id===g.connectionId),child=tabs.find(t=>t.id===lastPane.current[g.groupId])||g;return {id:g.groupId,label:g.groupTable||g.title,title:`${c?.name||"Conexión"} · ${g.groupDatabase}${g.groupTable?" · "+g.groupTable:""}`,leading:<span className={`tab-env env-${c?.env||"local"}`}/>,control:`panel-${child.id}`,dirty:tabs.some(t=>t.groupId===g.groupId&&t.dirty),onSelect:()=>activate(child.id),onClose:()=>closeGroup(g.groupId)};})];
 const query=search.toLowerCase();
 return <div className="workbench"><TabStrip label="Espacios de trabajo" className="main-tab-strip" active={current?.groupId||"home"} items={mainItems} actions={<><button onClick={newSQL} disabled={!currentConnection} title="Nueva consulta SQL · Ctrl+Alt+N" aria-label="Nueva pestaña SQL">＋</button><button className={preferences.viewOnly?"settings-button protected":"settings-button"} aria-label="Preferencias del espacio" onClick={()=>setSettings(true)}>⚙{preferences.viewOnly&&<span className="settings-lock">●</span>}</button><button onClick={()=>{setPalette(true);setSearch("");}} title="Buscar y abrir · Ctrl+K">⌕ <span className="shortcut-hint">Ctrl K</span></button></>}/>
 {error&&<div className="workbench-error" role="alert">{error}<button onClick={()=>setError("")}>Cerrar</button></div>}
 <div id="panel-home" role="tabpanel" aria-labelledby="tab-home" className="workbench-panel" hidden={active!=="home"}><header className="topbar"><strong className="brand"><Brand/></strong><span className="spacer"/><span className="muted">{session.username}</span><button onClick={onLogout}>Salir</button></header><main className="content home-content"><Connections key={newConnection} storageKey={storageKey} startNew={newConnection>0} onSelect={c=>open(c)}/></main></div>
 {tabs.filter(t=>visited.includes(t.id)).map(pane=>{const c=connections.find(c=>c.id===pane.connectionId);return <div id={`panel-${pane.id}`} role="tabpanel" aria-labelledby={`tab-${pane.groupId}`} key={pane.id} className="workbench-panel" hidden={active!==pane.id}>{c?<Workspace connection={c} username={session.username} onLogout={onLogout} onBack={()=>activate("home")} initialDatabase={pane.database} initialTable={pane.table} initialView={pane.view} initialSQL={seeds.current[pane.id]} isActive={active===pane.id} viewNavigation={navigation(pane,c)} onOpenTable={(db,table)=>open(c,db,table,"data")} onOpenView={(db,table,view)=>open(c,db,table,view,undefined,pane.groupTable&&table===pane.groupTable&&db===pane.groupDatabase?pane:undefined)} onCloseTab={()=>remove([pane.id])} onNewSQL={(db,seed)=>open(c,db,pane.groupTable,"sql",seed,pane.groupTable?pane:undefined)} onContextChange={ctx=>setTabs(old=>old.map(t=>t.id===pane.id?{...t,database:ctx.database,table:ctx.table,view:ctx.view,sql:ctx.sql,dirty:ctx.dirty,title:ctx.sql?"Consulta SQL":ctx.table||ctx.database||c.name}:t))}/>:<div className="content"><p>Este perfil ya no está disponible.</p><button onClick={()=>activate("home")}>Ir a conexiones</button><button onClick={()=>remove([pane.id])}>Cerrar pestaña</button></div>}</div>;})}
 {settings&&<PreferencesDialog onClose={()=>setSettings(false)} onHistory={()=>{setSettings(false);setHistory(true);}}/>}
 {history&&<QueryHistoryDialog onClose={()=>setHistory(false)} onOpen={entry=>{const c=connections.find(c=>c.id===entry.path.split("/")[3]);if(c){const db=entry.database??c.database,parent=current?.groupTable&&current.connectionId===c.id&&current.groupDatabase===db?current:undefined;open(c,db,parent?.groupTable||"","sql",entry.sql,parent);setHistory(false);}else setError("El perfil de esta consulta ya no está disponible.");}}/>}
 {palette&&<Dialog title="Buscar y abrir" onClose={()=>setPalette(false)}><label>Conexión, pestaña o acción<input autoFocus type="search" value={search} onChange={e=>setSearch(e.target.value)} placeholder="Escribe un nombre…"/></label><div className="command-results"><button onClick={()=>{activate("home");setPalette(false);}}>⌂ Ir a conexiones</button><button onClick={()=>{activate("home");setNewConnection(n=>n+1);setPalette(false);}}>＋ Nueva conexión</button>{currentConnection&&<button onClick={()=>{newSQL();setPalette(false);}}>⌘ Nueva consulta SQL <small>Ctrl+Alt+N</small></button>}{tabs.filter(t=>`${t.title} ${t.database} ${viewLabels[t.view]||""}`.toLowerCase().includes(query)).map(t=><button key={t.id} onClick={()=>{activate(t.id);setPalette(false);}}>Pestaña · {t.groupTable||t.title}<small>{viewLabels[t.view]||connections.find(c=>c.id===t.connectionId)?.name}</small></button>)}{connections.filter(c=>`${c.name} ${c.host}`.toLowerCase().includes(query)).map(c=><button key={c.id} onClick={()=>{open(c);setPalette(false);}}><span className={`badge env-${c.env}`}>{c.env}</span>{c.name}<small>{c.host}{c.ssh?.enabled?" · SSH":""}</small></button>)}</div></Dialog>}
 </div>;
}
