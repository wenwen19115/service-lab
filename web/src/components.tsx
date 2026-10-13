import {useEffect,useRef,useState, type ReactNode} from 'react';
import type {Event} from './types';
import {failed,formatJSON,phases,progress,size,time} from './types';

const paths:Record<string,string[]>={
 grid:['M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z'],
 upload:['M12 16V3 m-5 5 5-5 5 5 M4 16v5h16v-5'],
 api:['m8 8-4 4 4 4 m8-8 4 4-4 4 m-3-11-2 14'],
 activity:['M3 12h4l3-8 4 16 3-8h4'],
 devices:['M3 4h18v12H3z M8 21h8 m-4-5v5'],
 settings:['M4 6h16 M4 12h16 M4 18h16 M8 3v6 M16 9v6 M10 15v6'],
 plus:['M12 5v14 M5 12h14'],search:['M21 21l-5-5','M17 10a7 7 0 1 1-14 0 7 7 0 0 1 14 0'],
 copy:['M8 8h12v13H8z M4 16H2V2h13v2'],trash:['M3 6h18 M8 6V3h8v3 M5 6l1 15h12l1-15 M10 10v7 M14 10v7'],
 check:['m5 12 4 4 10-10'],x:['m6 6 12 12 M6 18 18 6'],chevron:['m9 5 7 7-7 7'],
 play:['m8 4 12 8-12 8z'],pause:['M8 4v16 M16 4v16'],download:['M12 3v13 m-5-5 5 5 5-5 M4 17v4h16v-4'],
 refresh:['M20 8a8 8 0 0 0-14-3L3 8 m0-5v5h5 M4 16a8 8 0 0 0 14 3l3-3 m0 5v-5h-5'],
 edit:['m16 3 5 5-13 13H3v-5z M13 6l5 5'],file:['M14 2H4v20h16V8z M14 2v6h6 M8 13h8 M8 17h5'],
 restore:['M3 10h5 M3 10V5 m0 5 4-4a8 8 0 1 1-2 11 M12 7v5l3 2'],
 clock:['M12 7v5l3 2','M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0'],
 external:['M14 3h7v7 M21 3l-11 11 M10 3H3v18h18v-7'],
 folder:['M2 6V3h7l3 3h10v15H2z'],terminal:['m5 7 5 5-5 5 M13 17h6'],
 bell:['M5 17h14l-2-3V8a5 5 0 0 0-10 0v6z M10 21h4'],
 link:['m10 14 4-4 M8 16l-2 2a4 4 0 0 1-6-6l4-4a4 4 0 0 1 6 0 M16 8l2-2a4 4 0 0 1 6 6l-4 4a4 4 0 0 1-6 0'],
};
export function Icon({name,size=18}:{name:string;size?:number}){return <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{(paths[name]||paths.file).map((d,i)=><path d={d} key={i}/>)}</svg>;}
export function Badge({children,tone='neutral'}:{children:ReactNode;tone?:string}){return <span className={'badge '+tone}>{children}</span>;}
export function Modal({title,subtitle,children,onClose,wide=false}:{title:string;subtitle?:string;children:ReactNode;onClose:()=>void;wide?:boolean}){
 const ref=useRef<HTMLDivElement>(null);
 useEffect(()=>{const previous=document.activeElement as HTMLElement|null;const before=document.body.style.overflow;document.body.style.overflow='hidden';ref.current?.querySelector<HTMLElement>('button')?.focus();function key(e:KeyboardEvent){if(e.key==='Escape'){onClose();}if(e.key==='Tab'&&ref.current){const items=Array.from(ref.current.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),select,textarea,a[href]'));if(!items.length)return;const first=items[0],last=items[items.length-1];if(e.shiftKey&&document.activeElement===first){e.preventDefault();last.focus();}else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first.focus();}}}document.addEventListener('keydown',key);return()=>{document.body.style.overflow=before;document.removeEventListener('keydown',key);previous?.focus();};},[]);
 return <div className="modal-mask" onMouseDown={e=>{if(e.target===e.currentTarget)onClose();}}><div className={'modal '+(wide?'wide':'')} role="dialog" aria-modal="true" aria-label={title} ref={ref} tabIndex={-1}><div className="modal-header"><div><h2>{title}</h2>{subtitle&&<p>{subtitle}</p>}</div><button className="icon-button" aria-label="关闭弹窗" onClick={onClose}><Icon name="x"/></button></div>{children}</div></div>;
}
export function Empty({icon='folder',title,description,action}:{icon?:string;title:string;description?:string;action?:ReactNode}){return <div className="empty"><span className="empty-icon"><Icon name={icon} size={27}/></span><h3>{title}</h3>{description&&<p>{description}</p>}{action}</div>;}
export function Copy({text,onCopy,label='复制',compact=false}:{text:string;onCopy:(s:string)=>void;label?:string;compact?:boolean}){return <button className={compact?'icon-button':'button quiet'} title={label} aria-label={label} onClick={()=>onCopy(text)}><Icon name="copy" size={15}/>{!compact&&label}</button>;}
export function JSONPanel({value,onCopy,empty='无内容'}:{value:string;onCopy:(text:string)=>void;empty?:string}){
 const [raw,setRaw]=useState(false);const formatted=raw?value:formatJSON(value);
 const display=formatted.slice(0,65536);
 return <div className="json-panel"><div className="json-toolbar"><div className="segments"><button className={!raw?'selected':''} onClick={()=>setRaw(false)}>格式化</button><button className={raw?'selected':''} onClick={()=>setRaw(true)}>原始</button></div><Copy text={formatted} onCopy={onCopy} compact/></div>{!value?<div className="json-empty">{empty}</div>:<pre><code>{display.split(/("(?:\\.|[^"\\])*"\s*:|"(?:\\.|[^"\\])*"|\btrue\b|\bfalse\b|\bnull\b|-?\d+(?:\.\d+)?)/g).map((part,i)=>{const kind=part.endsWith(':')?'json-key':part.startsWith('"')?'json-string':/^(true|false|null)$/.test(part)?'json-literal':/^-?\d/.test(part)?'json-number':'';return <span className={kind} key={i}>{part}</span>;})}</code></pre>}{formatted.length>65536&&<div className="hint">显示前 64 KiB；复制可获取完整内容。</div>}</div>;
}
export function Transfer({event,compact=false}:{event:Event;compact?:boolean}){return <div className={'transfer '+(compact?'compact':'')}><div className="transfer-label"><span>{size(event.bytes)} <small>/ {size(event.totalBytes)}</small></span><strong>{progress(event).toFixed(1)}%</strong></div><div className="progress-track"><i style={{width:progress(event)+'%'}} className={failed(event)?'failed':''}/></div>{!compact&&<div className="transfer-meta"><span>{phases[event.phase]}</span><span>{size(event.speedBps)}/s</span></div>}</div>;}
export function EventTable({events,onSelect}:{events:Event[];onSelect:(e:Event)=>void}){
 if(!events.length)return <Empty icon="activity" title="暂无匹配的请求" description="设备接入或调试接口后，请求会显示在这里。"/>;
 return <div className="table-wrap"><table><thead><tr><th>时间</th><th>请求路径</th><th>设备 / 客户端</th><th>结果</th><th>数据传输</th><th>耗时</th><th/></tr></thead><tbody>{events.map(e=><tr key={e.id} onClick={()=>onSelect(e)} tabIndex={0} onKeyDown={k=>{if(k.key==='Enter')onSelect(e);}}><td className="mono">{time(e.time)}</td><td><div className="request-route"><Badge tone={'method '+e.method.toLowerCase()}>{e.method}</Badge><code title={e.path}>{e.path}</code></div><small>{e.note||'设备请求'}</small></td><td><code>{e.deviceId||e.remote.split(':')[0]}</code><small>{e.version?'v'+e.version:'未上报版本'}</small></td><td><Badge tone={failed(e)?'danger':e.phase==='downloading'?'info':'success'}>{e.status||'…'} {e.phase?phases[e.phase]:''}</Badge></td><td>{e.totalBytes?<Transfer event={e} compact/>:<span className="mono muted">{size(e.bytes)}</span>}</td><td className="mono">{e.durationMs<1000?e.durationMs+' ms':(e.durationMs/1000).toFixed(1)+' s'}</td><td><Icon name="chevron" size={14}/></td></tr>)}</tbody></table></div>;
}
export function Field({label,hint,children}:{label:string;hint?:string;children:ReactNode}){return <label className="field"><span>{label}</span>{children}{hint&&<small>{hint}</small>}</label>;}
