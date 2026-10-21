import {useEffect,useState} from 'react';
import {api,jsonRequest} from './types';
import {Field} from './components';
type Props={value:string;busy:boolean;run:(fn:()=>Promise<void>)=>void};
export function OTAEndpoint({value,busy,run}:Props){
 const [draft,setDraft]=useState(value);
 useEffect(()=>setDraft(value),[value]);
 return <section className="panel"><div className="panel-heading"><div><h2>OTA服务接口</h2><p>预置版本检测接口，响应由OTA服务的固件与场景设置生成。</p></div></div><Field label="版本检测路径" hint="可填路径或粘贴完整URL，保存立即生效。"><input value={draft} disabled={busy} onChange={e=>setDraft(e.target.value)}/></Field><div className="button-group"><button className="button primary" disabled={busy||draft===value} onClick={()=>run(async()=>{await api('/api/ota/endpoint',jsonRequest('PUT',{path:draft}));})}>保存地址</button><button className="button quiet" disabled={busy} onClick={()=>setDraft('/ota')}>填入通用路径</button>{draft!==value&&<button className="button quiet" disabled={busy} onClick={()=>setDraft(value)}>放弃修改</button>}</div></section>;
}
