import {useEffect,useState} from 'react';
import type {Transfer} from './types';
import {api,jsonRequest} from './types';
import {Field} from './components';

type Props={value:Transfer;busy:boolean;run:(fn:()=>Promise<void>)=>void};
export function TransferEndpoints({value,busy,run}:Props){
 const [draft,setDraft]=useState(value);
 useEffect(()=>setDraft(value),[value.listPath,value.confirmPath,value.downloadPath]);
 const dirty=JSON.stringify(draft)!==JSON.stringify(value);
 return <section className="panel">
  <div className="panel-heading"><div><h2>资源传输接口</h2><p>配置资源列表、接收确认和文件下载地址，响应内容由资源传输模块生成。</p></div></div>
  <p className="muted">可填写路径或粘贴完整 URL，自动提取路径；服务主机不变，保存立即生效。</p>
  {([['listPath','资源列表（GET）'],['confirmPath','接收确认（POST）'],['downloadPath','文件下载前缀']] as const).map(([field,label])=><Field key={field} label={label}><input value={draft[field]} disabled={busy} onChange={e=>setDraft({...draft,[field]:e.target.value})}/></Field>)}
  <div className="button-group"><button className="button primary" disabled={busy||!dirty} onClick={()=>run(async()=>{await api('/api/transfer',jsonRequest('PUT',draft));})}>保存地址</button><button className="button quiet" disabled={busy} onClick={()=>setDraft({listPath:'/transfer/resources',confirmPath:'/transfer/confirm',downloadPath:'/resources'})}>填入默认值</button>{dirty&&<button className="button quiet" disabled={busy} onClick={()=>setDraft(value)}>放弃修改</button>}</div>
 </section>;
}
