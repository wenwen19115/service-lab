export type Firmware={id:string;name:string;version:string;imageVersion:string;size:number;sha256:string;uploadedAt:string};
export type OTA={mode:string;firmwareId:string;publicBase:string;upstream:string;forward:boolean;delayMs:number};
export type Mock={id:string;name:string;method:string;path:string;status:number;contentType:string;body:string;delayMs:number;enabled:boolean;headers?:Record<string,string>};
export type Step={time:string;bytes:number;phase:string;message:string};
export type Event={id:number;phase:string;totalBytes:number;speedBps:number;firmwareVersion:string;responseBody:string;requestHeaders?:Record<string,string>;responseHeaders?:Record<string,string>;steps?:Step[];time:string;method:string;path:string;remote:string;deviceId:string;version:string;status:number;bytes:number;durationMs:number;body:string;note:string};
export type State={config:{ota:OTA;firmware:Firmware[];trash:Firmware[];mocks:Mock[]};events:Event[];addresses:string[];now:string;server:{version:string;buildTime:string;startedAt:string;uptimeSeconds:number;dataDir:string;listenAddr:string;eventLimit:number;historyError:string;historySavedAt:string}};
export type Tab='overview'|'ota'|'mocks'|'events'|'devices'|'settings'|'about';
export type TestResult={status:number;headers:Record<string,string[]>;body:string;durationMs:number;truncated:boolean};
export const modes:Record<string,string>={'no-update':'无更新',update:'正常升级','check-error':'检查失败','download-error':'下载失败',truncate:'下载中断'};
export const phases:Record<string,string>={started:'连接中',downloading:'传输中',completed:'文件已发送',interrupted:'传输中断',failed:'请求失败'};
export const size=(n=0)=>n>=1048576?(n/1048576).toFixed(2)+' MiB':n>=1024?(n/1024).toFixed(1)+' KiB':n+' B';
export const time=(s:string)=>new Date(s).toLocaleTimeString('zh-CN',{hour12:false});
export const date=(s:string)=>new Date(s).toLocaleString('zh-CN',{hour12:false});
export const relative=(s:string)=>{const seconds=Math.max(0,Math.floor((Date.now()-new Date(s).getTime())/1000));return seconds<60?seconds+' 秒前':seconds<3600?Math.floor(seconds/60)+' 分钟前':seconds<86400?Math.floor(seconds/3600)+' 小时前':Math.floor(seconds/86400)+' 天前';};
export const formatJSON=(body:string)=>{try{return JSON.stringify(JSON.parse(body),null,2);}catch{return body;}};
export const failed=(e:Event)=>e.status>=400||e.phase==='interrupted'||e.phase==='failed';
export const progress=(e:Event)=>e.totalBytes?Math.min(100,e.bytes/e.totalBytes*100):0;
export const isPublished=(state:State)=>['update','download-error','truncate'].includes(state.config.ota.mode)&&!!state.config.firmware.find(f=>f.id===state.config.ota.firmwareId);
export const baseURL=(state:State)=>state.config.ota.publicBase||state.addresses[0]||window.location.origin;
export function exportJSON(name:string,data:unknown){const uri=URL.createObjectURL(new Blob([JSON.stringify(data,null,2)],{type:'application/json'}));const link=document.createElement('a');link.href=uri;link.download=name;link.click();setTimeout(()=>URL.revokeObjectURL(uri),1000);}
export async function api<T>(path:string,init?:RequestInit):Promise<T>{const res=await fetch(path,init);const data=await res.json();if(!res.ok)throw new Error(data.error||'请求失败（'+res.status+'）');return data as T;}
export const jsonRequest=(method:string,body:unknown):RequestInit=>({method,headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
