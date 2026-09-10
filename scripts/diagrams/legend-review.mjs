#!/usr/bin/env node
// Build a separate review artifact from the accepted ACME diagram. This never
// changes the diagram source, delivered diagram, or shared legend renderer.
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
import {palette} from './colours.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const source=fs.readFileSync(path.join(root,'docs/diagrams/acme-internals.html'),'utf8');
const spec=JSON.parse(fs.readFileSync(path.join(root,'docs/diagrams/src/acme-internals.architecture.json'),'utf8'));
const start=source.indexOf('<svg viewBox="');
if(start<0)throw new Error('ACME SVG missing');
const svg=source.slice(start,source.indexOf('</svg>',start)+6);
const css=source.match(/<style>([\s\S]*?)<\/style>/)?.[1];
if(!css||!svg.includes('data-purpose-legend'))throw new Error('Expected purpose-colour ACME artifact');
const used=new Set([...spec.components,...spec.connections].map(x=>x.purpose));
const examples={
 service:'ACME coordinator; orders and authorizations',
 transport:'ACME discovery and signed request delivery',
 storage:'Replay nonces, orders, and ACME persistence',
 authentication:'JWS verification and device attestation',
 certificate:'Certificate authority: verify the CSR and issue',
 policy:'Admission policy and enrollment identifier claims',
 neutral:'The Apple device making the request',
 failure:'A bad attestation chain or a refused request',
};
const entries=Object.entries(palette).filter(([key])=>used.has(key)).map(([key,p])=>({key,...p,example:examples[key],nodes:spec.components.filter(n=>n.purpose===key).map(n=>n.label),edges:spec.connections.filter(e=>e.purpose===key).map(e=>e.label)}));
const data={svg,entries,cards:spec.cards,viewBox:spec.meta.viewBox,sourceSha256:createHash('sha256').update(source).digest('hex')};
const serialize=value=>JSON.stringify(value).replaceAll('<','\\u003c').replaceAll('>','\\u003e').replaceAll('&','\\u0026');
const html=`<!doctype html>
<html lang="en" data-theme="light" data-reading-layout="scroll">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>ACME legend options · go-apple-dm</title>
<style>${css}
/* Review-only interface. The diagram retains the delivered geometry and colours. */
:root { color-scheme:light; --review-bg:#f4f6fa; --review-panel:#fff; --review-ink:#172335; --review-muted:#526174; --review-line:#dce3ec; --review-soft:#eef2f7; --review-accent:#2563eb; }
html[data-theme="dark"] { color-scheme:dark; --review-bg:#09111f; --review-panel:#101c2e; --review-ink:#edf3fc; --review-muted:#a8b4c5; --review-line:#2a3a52; --review-soft:#17263c; --review-accent:#60a5fa; }
html { scroll-behavior:smooth; scroll-padding-top:120px; }
body { margin:0; padding:0; background:var(--review-bg); color:var(--review-ink); font-family:system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif; font-size:16px; line-height:1.55; transition:none; }
button,a { -webkit-tap-highlight-color:transparent; }
button { font:inherit; }
button:focus-visible,a:focus-visible { outline:3px solid var(--review-accent); outline-offset:4px; }
a { color:var(--review-accent); text-underline-offset:3px; }
button { cursor:pointer; }
.review-top { max-width:1840px; margin:auto; padding:28px 32px 20px; display:flex; align-items:center; justify-content:space-between; gap:24px; }
.review-brand { font-size:12px; letter-spacing:.16em; font-weight:750; text-transform:uppercase; color:var(--review-muted); }
.review-top-actions { display:flex; align-items:center; gap:20px; font-size:14px; }
.review-button { border:1px solid var(--review-line); background:var(--review-panel); color:var(--review-ink); border-radius:9px; padding:9px 15px; font-weight:650; }
.review-button:hover { border-color:var(--review-accent); }
.review-heading { max-width:1840px; margin:auto; padding:0 32px 26px; }
.review-heading h1 { font-size:clamp(28px,3vw,42px); letter-spacing:-.035em; line-height:1.15; margin:6px 0 12px; }
.review-heading p { max-width:900px; color:var(--review-muted); margin:0; font-size:17px; }
.review-tabs-shell { position:sticky; top:0; z-index:20; background:var(--review-bg); border-block:1px solid var(--review-line); }
.review-tabs { max-width:1840px; margin:auto; padding:12px 32px; display:grid; grid-template-columns:repeat(5,minmax(0,1fr)); gap:10px; }
.review-tab { display:flex; align-items:center; gap:12px; text-align:left; padding:13px 15px; border:1px solid var(--review-line); border-radius:10px; color:var(--review-muted); background:var(--review-panel); font-weight:650; line-height:1.3; }
.review-number { flex-shrink:0; width:30px; height:30px; display:grid; place-items:center; font-size:13px; border:1px solid var(--review-line); border-radius:50%; }
.review-tab[aria-selected="true"] { color:var(--review-ink); border-color:var(--review-accent); box-shadow:0 0 0 1px var(--review-accent); }
.review-tab[aria-selected="true"] .review-number { color:var(--review-panel); background:var(--review-accent); border-color:var(--review-accent); }
.review-main { max-width:1840px; margin:0 auto; padding:28px 32px 60px; }
.option-heading { display:flex; justify-content:space-between; align-items:flex-start; gap:28px; margin-bottom:20px; }
.option-heading h2 { margin:0 0 5px; font-size:24px; letter-spacing:-.025em; }
.option-heading p { margin:0; max-width:900px; color:var(--review-muted); }
.option-heading .review-button { white-space:nowrap; margin-top:4px; }
.option-note { margin:18px 0 28px; padding:16px 20px; border-left:3px solid var(--review-accent); background:var(--review-soft); border-radius:0 8px 8px 0; color:var(--review-muted); }
.option-note strong { color:var(--review-ink); }
.review-stage { border:1px solid var(--review-line); border-radius:16px; background:var(--review-panel); padding:20px; min-width:0; }
.stage-caption { max-width:1440px; margin:0 auto 8px; padding:4px 0 12px; color:var(--review-muted); font-size:13px; display:flex; justify-content:space-between; gap:18px; border-bottom:1px solid var(--review-line); }
.stage-caption strong { color:var(--review-ink); font-size:16px; font-weight:650; }
.diagram-with-key { min-width:0; }
.review-diagram { min-width:0; width:100%; max-width:1440px; margin:0 auto; }
.review-diagram>svg { display:block; width:100%; height:auto; overflow:visible; font-family:'JetBrains Mono',ui-monospace,SFMono-Regular,Menlo,Consolas,monospace; }
.review-diagram svg [data-node-id] { cursor:default; }
.review-diagram svg [data-node-id]:hover { filter:none; }
.review-diagram svg [data-muted] { opacity:.13; }
.review-diagram svg [data-highlight] > rect:not(.c-mask), .review-diagram svg path[data-highlight], .review-diagram svg [data-highlight] > path { stroke-width:2.4; }
.review-diagram svg [data-node-id], .review-diagram svg [data-edge-from] { transition:opacity .15s; }
.review-legend { max-width:1440px; margin:4px auto 0; padding:22px 0 8px; border-top:1px solid var(--review-line); scroll-margin-top:125px; }
.legend-eyebrow { margin:0 0 12px; font-size:12px; font-weight:750; letter-spacing:.12em; text-transform:uppercase; color:var(--review-muted); }
.legend-footnote { margin:16px 0 0; font-size:14px; color:var(--review-muted); }
.legend-dot { width:12px; height:12px; border-radius:3px; background:var(--entry); flex:0 0 12px; }
.grouped-key { display:grid; grid-template-columns:minmax(0,3fr) minmax(220px,1fr); gap:24px; }
.purpose-chips { display:flex; flex-wrap:wrap; gap:10px; }
.purpose-chip { display:flex; align-items:center; gap:9px; border:1px solid var(--review-line); background:var(--review-panel); border-radius:7px; padding:8px 11px; font-size:14px; font-weight:550; }
.outcome-band { border-left:1px solid var(--review-line); padding-left:24px; }
.outcome-band p { font-size:14px; color:var(--review-muted); margin:12px 0 0; }
.example-key { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); gap:12px; }
.example-card { position:relative; border:1px solid var(--review-line); border-radius:10px; background:var(--review-panel); padding:17px 17px 18px 21px; overflow:hidden; }
.example-card::before { content:''; position:absolute; left:0; top:0; bottom:0; width:4px; background:var(--entry); }
.example-card small { display:block; font-size:10px; letter-spacing:.1em; text-transform:uppercase; color:var(--review-muted); font-weight:700; margin-bottom:6px; }
.example-card h3 { font-size:15px; margin:0 0 8px; line-height:1.4; }
.example-card p { font-size:14px; line-height:1.5; margin:0; color:var(--review-muted); }
.side-key { padding:20px; background:var(--review-soft); border-radius:12px; margin-top:10px; }
.side-key h3 { margin:0 0 3px; font-size:18px; }
.side-key-intro { color:var(--review-muted); font-size:13px; margin:0 0 16px; }
.side-key-list { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:0 24px; }
.side-key-item { display:flex; gap:11px; padding:12px 0; border-top:1px solid var(--review-line); }
.side-key-item .legend-dot { margin-top:5px; }
.side-key-item strong { display:block; font-size:14px; line-height:1.4; }
.side-key-item p { font-size:12px; color:var(--review-muted); line-height:1.5; margin:4px 0 0; }
.side-key [data-kind="outcome"] { border-top:2px solid var(--entry); }
.interactive-key { margin:0 auto 18px; border-top:0; padding:16px 18px; border:1px solid var(--review-line); background:var(--review-soft); border-radius:12px; }
.filter-heading { display:flex; align-items:baseline; justify-content:space-between; gap:18px; margin-bottom:12px; }
.filter-heading h3 { margin:0; font-size:17px; }
.filter-heading span { color:var(--review-muted); font-size:13px; }
.filter-chips { display:flex; gap:9px; flex-wrap:wrap; }
.filter-chip { display:flex; align-items:center; gap:8px; border-radius:7px; border:1px solid var(--review-line); background:var(--review-panel); color:var(--review-ink); padding:8px 11px; font-size:13px; font-weight:600; }
.filter-chip[aria-pressed="true"] { border-color:var(--entry,var(--review-accent)); box-shadow:0 0 0 1px var(--entry,var(--review-accent)); }
.filter-detail { min-height:66px; display:grid; grid-template-columns:minmax(200px,.8fr) minmax(0,2fr); gap:20px; align-items:start; margin:16px 0 0; padding:14px 0 0; border-top:1px solid var(--review-line); }
.filter-detail strong { font-size:15px; }
.filter-detail span { display:block; color:var(--review-muted); font-size:13px; margin-top:3px; }
.filter-detail p { color:var(--review-muted); margin:0; font-size:14px; }
.context-label { font-size:13px; color:var(--review-muted); margin:0 0 12px; }
.context-cards { display:grid; grid-template-columns:repeat(3,minmax(0,1fr)); gap:16px; }
.context-card { border:1px solid var(--review-line); border-radius:12px; padding:21px; background:var(--review-panel); }
.context-card h3 { margin:0 0 14px; font-size:16px; }
.context-card ul { margin:0; padding-left:19px; }
.context-card li { color:var(--review-muted); font-size:15px; line-height:1.65; margin:8px 0; }
.review-footer { margin-top:32px; padding-top:22px; border-top:1px solid var(--review-line); display:flex; justify-content:space-between; gap:20px; color:var(--review-muted); font-size:14px; }
.review-footer p { margin:0; }
@media(min-width:1850px) {
 .diagram-with-key.has-side-key { display:grid; grid-template-columns:minmax(0,1fr) 290px; gap:24px; align-items:start; }
 .has-side-key .review-diagram { max-width:1440px; }
 .has-side-key #side-key-slot { position:sticky; top:108px; align-self:start; }
 .has-side-key .side-key { margin:0; padding:18px; }
 .has-side-key .side-key-list { display:block; }
}
@media(max-width:1100px) { .example-key { grid-template-columns:repeat(2,minmax(0,1fr)); } .grouped-key { grid-template-columns:1fr; } .outcome-band { border-left:0; border-top:1px solid var(--review-line); padding:18px 0 0; } .review-tabs { gap:6px; } .review-tab { font-size:13px; padding:10px; gap:7px; } }
@media(max-width:760px) {
 html { scroll-padding-top:180px; } .review-top,.review-heading,.review-main { padding-left:16px; padding-right:16px; }
 .review-top { align-items:flex-start; } .review-top-actions { gap:10px; } .review-top-actions a { display:none; }
 .review-tabs { padding:10px 16px; grid-template-columns:repeat(3,minmax(0,1fr)); } .review-tab { min-height:50px; } .review-number { width:22px; height:22px; font-size:11px; }
 .option-heading { display:block; } .option-heading .review-button { margin-top:16px; }
 .review-stage { padding:12px; } .stage-caption { display:block; } .stage-caption span { display:block; margin-top:4px; }
 .example-key,.side-key-list,.context-cards,.filter-detail { grid-template-columns:1fr; } .filter-heading { display:block; } .filter-heading span { display:block; margin-top:4px; }
 .review-footer { display:block; } .review-footer p+p { margin-top:12px; }
}
@media(prefers-reduced-motion:reduce) { html { scroll-behavior:auto; } * { transition:none!important; } }
</style></head>
<body>
<header>
 <div class="review-top"><div class="review-brand">go-apple-dm / legend review</div><div class="review-top-actions"><a href="acme-internals.html" target="_blank" rel="noopener">Open current diagram ↗</a><button class="review-button" id="theme-toggle" type="button">Switch to dark</button></div></div>
 <div class="review-heading"><h1>Compare five legend layouts</h1><p>Use the ACME diagram to compare placement, explanation, and interaction. The diagram and agreed colour meanings stay the same. Select an option, then review its legend in context.</p></div>
</header>
<div class="review-tabs-shell"><nav class="review-tabs" role="tablist" aria-label="Legend options">
${['Original footer','Grouped bands','Example cards','Side reference','Interactive key'].map((name,index)=>`<button class="review-tab" type="button" role="tab" id="option-${index+1}" aria-selected="${index===0}" aria-controls="review-panel" tabindex="${index===0?0:-1}" data-option="${index+1}"><span class="review-number">${index+1}</span><span>${name}</span></button>`).join('\n')}
</nav></div>
<main class="review-main">
 <section id="review-panel" role="tabpanel" aria-labelledby="option-1">
  <div class="option-heading"><div><h2 id="option-title"></h2><p id="option-description"></p></div><button type="button" class="review-button" id="jump-legend">Jump to legend ↓</button></div>
  <div class="review-stage"><div class="stage-caption"><strong>ACME Server Internals</strong><span>Same 13 components · Same 13 connections</span></div><div id="interactive-key-slot"></div><div class="diagram-with-key" id="diagram-with-key"><div class="review-diagram" id="diagram"></div><div id="side-key-slot"></div></div><div id="footer-key-slot"></div></div>
  <div class="option-note" id="option-note"></div>
 </section>
 <section aria-label="ACME context"><p class="context-label">The diagram’s explanations and public references are retained below for context.</p><div class="context-cards" id="context-cards"></div></section>
 <footer class="review-footer"><p>Option 5 was selected and applied across all 31 diagrams.</p><p><a href="colours.md">Shared colour meanings</a> · The five alternatives are retained here as a record of the review.</p></footer>
</main>
<script type="application/json" id="review-data">${serialize(data)}</script>
<script>
'use strict';
const review=JSON.parse(document.getElementById('review-data').textContent);
const definitions=[
 {title:'1 · Original footer',description:'The existing compact colour key sits below the complete diagram.',note:'Smallest change.',tradeoff:'Easy to scan and straightforward to export, but it asks readers to connect abstract category names to the diagram themselves.'},
 {title:'2 · Grouped purpose and outcome bands',description:'Separate component responsibilities from the meaning of a failed outcome.',note:'Clearest semantic grouping.',tradeoff:'Makes the role-versus-result distinction explicit without adding much height. Categories still rely mainly on their names.'},
 {title:'3 · Example cards',description:'Every colour has a short explanation drawn directly from this ACME flow.',note:'Most help for newcomers.',tradeoff:'Readers can connect each category to a named component or operation. The extra explanation adds a few rows below the diagram.'},
 {title:'4 · Side reference panel',description:'Keep the key beside the diagram on a wide display, with an example for every colour.',note:'Reference stays nearby.',tradeoff:'The panel remains visible while scrolling on wide screens. At narrower widths it moves below the diagram to preserve readable diagram text.'},
 {title:'5 · Interactive key',description:'Select a purpose to highlight the components and connections that use it.',note:'Explore the meaning in place.',tradeoff:'Directly shows what each colour refers to and keeps the rest of the flow visible. It needs interaction; a static export would include the complete key.'}
];
let option=1,filter='all';
const diagram=document.getElementById('diagram');
const esc=value=>String(value).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;').replaceAll('"','&quot;');
const outcome=e=>['success','warning','failure'].includes(e.key);
const entryStyle=e=>'--entry:var(--purpose-'+e.key+')';
const dot=e=>'<span class="legend-dot" aria-hidden="true" style="'+entryStyle(e)+'"></span>';
const chip=e=>'<span class="purpose-chip" data-key-entry="'+e.key+'">'+dot(e)+esc(e.label)+'</span>';
const caption='<p class="legend-footnote">Colour identifies purpose or outcome; line style does not imply success or failure.</p>';
function groupedKey(){return '<section class="review-legend" id="active-legend" aria-label="Grouped colour legend"><div class="grouped-key"><div><h3 class="legend-eyebrow">Purpose · what a component or operation does</h3><div class="purpose-chips">'+review.entries.filter(e=>!outcome(e)).map(chip).join('')+'</div></div><div class="outcome-band"><h3 class="legend-eyebrow">Outcome · what happened</h3><div class="purpose-chips">'+review.entries.filter(outcome).map(chip).join('')+'</div><p>Here, red marks a bad attestation chain or a refused request. Verification and policy evaluation keep their own purpose colours.</p></div></div>'+caption+'</section>';}
function exampleKey(){return '<section class="review-legend" id="active-legend" aria-label="Colour legend with ACME examples"><h3 class="legend-eyebrow">Colour and meaning in this diagram</h3><div class="example-key">'+review.entries.map(e=>'<article class="example-card" data-key-entry="'+e.key+'" style="'+entryStyle(e)+'"><small>'+ (outcome(e)?'Outcome':'Purpose')+'</small><h3>'+esc(e.label)+'</h3><p>'+esc(e.example)+'</p></article>').join('')+'</div>'+caption+'</section>';}
function sideKey(){return '<aside class="side-key" id="active-legend" aria-label="Colour reference"><h3>Colour reference</h3><p class="side-key-intro">Purpose colours describe responsibilities. Red identifies failure.</p><div class="side-key-list">'+review.entries.map(e=>'<div class="side-key-item" data-key-entry="'+e.key+'" data-kind="'+(outcome(e)?'outcome':'purpose')+'" style="'+entryStyle(e)+'">'+dot(e)+'<div><strong>'+esc(e.label)+'</strong><p>'+esc(e.example)+'</p></div></div>').join('')+'</div></aside>';}
function interactiveKey(){return '<section class="review-legend interactive-key" id="active-legend" aria-label="Interactive colour legend"><div class="filter-heading"><h3>Legend</h3><span>Choose a category; select All Components to restore the full diagram.</span></div><div class="filter-chips"><button class="filter-chip" type="button" data-filter="all" aria-pressed="true">All Components</button>'+review.entries.map(e=>'<button class="filter-chip" type="button" data-key-entry="'+e.key+'" data-filter="'+e.key+'" aria-pressed="false" style="'+entryStyle(e)+'">'+dot(e)+esc(e.label)+'</button>').join('')+'</div><div class="filter-detail" aria-live="polite" id="filter-detail"></div></section>';}
function selectFilter(key){
 filter=key;
 const entry=review.entries.find(e=>e.key===key);
 document.querySelectorAll('[data-filter]').forEach(button=>button.setAttribute('aria-pressed',String(button.dataset.filter===key)));
 diagram.querySelectorAll('[data-node-id],[data-edge-from]').forEach(el=>{
  const match=key==='all'||el.dataset.purpose===key;
  el.toggleAttribute('data-muted',!match);el.toggleAttribute('data-highlight',key!=='all'&&match);
 });
 const detail=document.getElementById('filter-detail');
 if(detail)detail.innerHTML=entry?'<div><strong>'+esc(entry.label)+'</strong><span>'+entry.nodes.length+' component'+(entry.nodes.length===1?'':'s')+' · '+entry.edges.length+' connection'+(entry.edges.length===1?'':'s')+'</span></div><p>'+esc(entry.example)+'. Highlighted components: '+esc(entry.nodes.join('; '))+'.</p>':'<div><strong>Complete ACME flow</strong><span>13 components · 13 connections</span></div><p>Choose a category to connect its colour with the actual diagram. For example, authentication highlights JWS verification and device attestation; failure highlights rejected requests.</p>';
}
function render(next){
 option=next;filter='all';const d=definitions[option-1];
 document.getElementById('option-title').textContent=d.title;
 document.getElementById('option-description').textContent=d.description;
 document.getElementById('option-note').innerHTML='<strong>'+esc(d.note)+'</strong> '+esc(d.tradeoff);
 document.getElementById('review-panel').setAttribute('aria-labelledby','option-'+option);
 document.querySelectorAll('[data-option]').forEach(button=>{const selected=Number(button.dataset.option)===option;button.setAttribute('aria-selected',String(selected));button.tabIndex=selected?0:-1;});
 diagram.innerHTML=review.svg;
 const svg=diagram.querySelector('svg');
 // The prototype compares legends; the full interactive Archify viewer remains
 // available through the reference link. Avoid inactive button semantics here.
 svg.querySelectorAll('[data-node-id]').forEach(n=>{n.removeAttribute('tabindex');n.removeAttribute('role');n.removeAttribute('aria-pressed');});
 if(option!==1){svg.querySelector('[data-purpose-legend]').remove();svg.setAttribute('viewBox','0 0 '+review.viewBox.join(' '));}
 else svg.querySelector('[data-purpose-legend]').id='active-legend';
 document.getElementById('footer-key-slot').innerHTML=option===2?groupedKey():option===3?exampleKey():'';
 document.getElementById('side-key-slot').innerHTML=option===4?sideKey():'';
 document.getElementById('interactive-key-slot').innerHTML=option===5?interactiveKey():'';
 document.getElementById('diagram-with-key').classList.toggle('has-side-key',option===4);
 document.querySelectorAll('[data-filter]').forEach(button=>button.addEventListener('click',()=>selectFilter(button.dataset.filter)));
 if(option===5)selectFilter('all');
 document.documentElement.dataset.option=String(option);
 try{history.replaceState(null,'','#option-'+option);}catch{}
}
document.querySelectorAll('[data-option]').forEach(button=>{
 button.addEventListener('click',()=>render(Number(button.dataset.option)));
 button.addEventListener('keydown',event=>{
  let next=option;
  if(event.key==='ArrowRight')next=option%5+1;
  else if(event.key==='ArrowLeft')next=(option+3)%5+1;
  else if(event.key==='Home')next=1;
  else if(event.key==='End')next=5;
  else return;
  event.preventDefault();render(next);document.getElementById('option-'+next).focus();
 });
});
document.getElementById('theme-toggle').addEventListener('click',()=>{
 const dark=document.documentElement.dataset.theme!=='dark';document.documentElement.dataset.theme=dark?'dark':'light';document.getElementById('theme-toggle').textContent=dark?'Switch to light':'Switch to dark';
});
document.getElementById('jump-legend').addEventListener('click',()=>document.getElementById('active-legend').scrollIntoView({block:'center',behavior:matchMedia('(prefers-reduced-motion:reduce)').matches?'instant':'smooth'}));
document.getElementById('context-cards').innerHTML=review.cards.map(card=>'<article class="context-card"><h3>'+esc(card.title)+'</h3><ul>'+card.items.map(item=>{const match=item.match(/^(.*?) — (https:\\/\\/[^\\s]+)$/);return '<li>'+(match?'<a href="'+esc(match[2])+'" target="_blank" rel="noopener noreferrer">'+esc(match[1])+'</a>':esc(item))+'</li>';}).join('')+'</ul></article>').join('');
const initial=Number(location.hash.match(/^#option-([1-5])$/)?.[1]||5);render(initial);
</script></body></html>`;
const out=path.join(root,'docs/diagrams/acme-legend-options.html');fs.writeFileSync(out,html.replace(/[ \t]+$/gm,''));
console.log(out);console.log('ACME source SHA-256: '+data.sourceSha256);
