import {Icon} from './components';

export function DiagnosticsPanel(){
 return <section className="panel">
  <div className="panel-heading"><div><h2>请求诊断</h2><p>下载最近的服务运行日志，排查连接、接口响应和传输问题。</p></div><a className="button" href="/api/diagnostics/log"><Icon name="download"/>下载诊断日志（最近 1 MiB）</a></div>
 </section>;
}
