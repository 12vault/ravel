package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/12ya/reporavel/internal/graph"
)

func Write(path string, g graph.Graph) error {
	data, err := json.Marshal(g)
	if err != nil {
		return err
	}
	safe := strings.ReplaceAll(string(data), "</script", `<\/script`)
	html := strings.Replace(document, "__GRAPH_DATA__", safe, 1)
	if err := os.WriteFile(path, []byte(html), 0644); err != nil {
		return fmt.Errorf("write dashboard: %w", err)
	}
	return nil
}

const document = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Ravel Graph</title>
<style>
:root{color-scheme:dark;--bg:#0b1020;--panel:#121a2e;--line:#263451;--text:#e7ecf5;--muted:#93a4c3;--accent:#6ee7b7}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:14px system-ui,sans-serif}header{height:58px;display:flex;align-items:center;gap:12px;padding:10px 16px;border-bottom:1px solid var(--line)}h1{font-size:17px;margin:0}input,select{background:var(--panel);color:var(--text);border:1px solid var(--line);border-radius:7px;padding:9px}input{flex:1}.layout{display:grid;grid-template-columns:minmax(0,1fr) 340px;height:calc(100vh - 58px)}canvas{width:100%;height:100%}aside{border-left:1px solid var(--line);background:var(--panel);padding:16px;overflow:auto}.muted{color:var(--muted)}.tag{display:inline-block;padding:3px 7px;margin:2px;border:1px solid var(--line);border-radius:999px}.relation{padding:7px 0;border-bottom:1px solid var(--line);cursor:pointer}.relation:hover{color:var(--accent)}code{word-break:break-all} @media(max-width:760px){.layout{grid-template-columns:1fr;grid-template-rows:60vh auto}aside{border-left:0;border-top:1px solid var(--line)}}
</style>
</head>
<body>
<header><h1>Ravel</h1><select id="view"><option value="">Everything</option><option value="architecture">Architecture</option><option value="learning">Tours</option><option value="documents">Documents</option><option value="schemas">Schemas</option></select><input id="search" placeholder="Search nodes, paths, metadata"><select id="kind"><option value="">All kinds</option></select><span id="count" class="muted"></span></header>
<main class="layout"><canvas id="graph"></canvas><aside id="detail"><h2>Knowledge graph</h2><p class="muted">Search or click a node to inspect its relationships.</p></aside></main>
<script>const G=__GRAPH_DATA__;
const canvas=document.querySelector('#graph'),ctx=canvas.getContext('2d'),search=document.querySelector('#search'),kind=document.querySelector('#kind'),view=document.querySelector('#view'),detail=document.querySelector('#detail'),count=document.querySelector('#count');
const palette={file:'#60a5fa',function:'#34d399',method:'#22d3ee',class:'#c084fc',domain:'#fb7185',flow:'#fbbf24',document:'#a3e635',table:'#f97316',concept:'#e879f9'};let visible=[],selected=null,points=[];
const viewKinds={architecture:new Set(['package','module','class','domain','flow','step','concept']),learning:new Set(['tour','concept','domain','flow','file']),documents:new Set(['document','section','concept','file']),schemas:new Set(['schema','table','column'])};
const byId=new Map(G.nodes.map(n=>[n.id,n]));const edgesBy=new Map;for(const e of G.edges){for(const id of[e.from,e.to]){if(!edgesBy.has(id))edgesBy.set(id,[]);edgesBy.get(id).push(e)}}
for(const k of [...new Set(G.nodes.map(n=>n.kind))].sort()){const o=document.createElement('option');o.value=k;o.textContent=k;kind.append(o)}
function resize(){const r=canvas.getBoundingClientRect(),d=devicePixelRatio||1;canvas.width=r.width*d;canvas.height=r.height*d;ctx.setTransform(d,0,0,d,0,0);layout()}
function filter(){const q=search.value.toLowerCase(),k=kind.value,v=viewKinds[view.value];visible=G.nodes.filter(n=>(!v||v.has(n.kind))&&(!k||n.kind===k)&&(!q||JSON.stringify(n).toLowerCase().includes(q))).slice(0,1200);count.textContent=visible.length+'/'+G.nodes.length;layout()}
function layout(){const w=canvas.clientWidth,h=canvas.clientHeight,cols=Math.max(1,Math.ceil(Math.sqrt(visible.length*w/Math.max(h,1))));points=visible.map((n,i)=>({n,x:35+(i%cols)*(w-70)/Math.max(cols-1,1),y:35+Math.floor(i/cols)*(h-70)/Math.max(Math.ceil(visible.length/cols)-1,1)}));draw()}
function draw(){const w=canvas.clientWidth,h=canvas.clientHeight;ctx.clearRect(0,0,w,h);const pmap=new Map(points.map(p=>[p.n.id,p]));ctx.strokeStyle='#263451';ctx.globalAlpha=.45;for(const e of G.edges){const a=pmap.get(e.from),b=pmap.get(e.to);if(a&&b){ctx.beginPath();ctx.moveTo(a.x,a.y);ctx.lineTo(b.x,b.y);ctx.stroke()}}ctx.globalAlpha=1;for(const p of points){ctx.beginPath();ctx.fillStyle=palette[p.n.kind]||'#94a3b8';ctx.arc(p.x,p.y,p.n.id===selected?7:4,0,Math.PI*2);ctx.fill()}}
function show(id){selected=id;const n=byId.get(id),relations=edgesBy.get(id)||[];detail.innerHTML='<h2>'+esc(n.name)+'</h2><p><span class="tag">'+esc(n.kind)+'</span></p><p><code>'+esc(n.id)+'</code></p>'+(n.path?'<p>'+esc(n.path)+(n.startLine?':'+n.startLine:'')+'</p>':'')+(n.meta?'<pre>'+esc(JSON.stringify(n.meta,null,2))+'</pre>':'')+'<h3>Relationships ('+relations.length+')</h3>'+relations.map(e=>{const other=byId.get(e.from===id?e.to:e.from);return other?'<div class="relation" data-id="'+esc(other.id)+'"><b>'+esc(e.kind)+'</b> '+esc(other.name)+'</div>':''}).join('');detail.querySelectorAll('[data-id]').forEach(el=>el.onclick=()=>show(el.dataset.id));draw()}
function esc(v){return String(v).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))}
canvas.onclick=e=>{const r=canvas.getBoundingClientRect(),x=e.clientX-r.left,y=e.clientY-r.top;let best=null,d=14;for(const p of points){const n=Math.hypot(p.x-x,p.y-y);if(n<d){d=n;best=p}}if(best)show(best.n.id)};search.oninput=filter;kind.onchange=filter;view.onchange=filter;addEventListener('resize',resize);filter();resize();
</script></body></html>`
