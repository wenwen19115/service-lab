import {useState} from 'react';
import type {Tab} from './types';
import {Icon} from './components';
const services:[Tab,string,string][]=[['ota','upload','OTA服务'],['resources','file','资源传输']];
type Props={tab:Tab;select:(tab:Tab)=>void;dirty:boolean};
export function ServiceNavigation({tab,select,dirty}:Props){
 const [open,setOpen]=useState(true);
 const active=services.some(([id])=>id===tab);
 return <div className="service-navigation"><button className={!open&&active?'active':''} aria-expanded={open} onClick={()=>setOpen(!open)}><Icon name="grid"/><span>服务</span><span className={'service-chevron '+(open?'open':'')}><Icon name="chevron" size={14}/></span></button>{open&&<div className="service-children">{services.map(([id,icon,label])=><button key={id} className={tab===id?'active':''} onClick={()=>select(id)}><Icon name={icon}/><span>{label}</span>{id==='ota'&&dirty&&<i className="pending-dot"/>}</button>)}</div>}</div>;
}
