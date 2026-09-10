import fs from 'node:fs';
import {palette} from './colours.mjs';
import os from 'node:os';
import {fileURLToPath, pathToFileURL} from 'node:url';
import path from 'node:path';
import {createHash} from 'node:crypto';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const skill=process.env.ARCHIFY_SKILL_DIR || path.join(os.homedir(),'.agents/skills/archify');
const {ChromeVisualBrowser,findChrome}=await import(pathToFileURL(path.join(skill,'bin/visual-check.mjs')).href);
const output=path.resolve(process.argv.find(a=>a.startsWith('--output='))?.slice(9)||path.join(os.tmpdir(),'diagram-viewer-checks.json'));
const only=process.argv.find(a=>a.startsWith('--only='))?.slice(7).split(',');
export async function main(){
 const browser=new ChromeVisualBrowser(findChrome()),results=[];
 const exportNames=['acme-internals','checkin-dispatch','enrollment-paths','flow-enrollment-profile-replacement','request-decode','lifecycle-command','flow-scep-issuance'];
 try {
  for(const src of fs.readdirSync(path.join(root,'docs/diagrams/src')).filter(x=>x.endsWith('.json')&&(!only||only.includes(x.split('.')[0])))){
   const name=src.split('.')[0],artifactPath=path.join(root,'docs/diagrams',name+'.html');
   await browser.inspect({artifactPath,width:1440,height:900,theme:'light'});
   const session=await browser.sessionPromise;
   const spec=JSON.parse(fs.readFileSync(path.join(root,'docs/diagrams/src',src),'utf8'));
   const collection={architecture:['components','connections'],workflow:['nodes','edges'],sequence:['participants','messages'],dataflow:['nodes','flows'],lifecycle:['states','transitions']}[spec.diagram_type];
   const expected=Object.fromEntries(Object.keys(palette).map(key=>[key,{nodes:spec[collection[0]].filter(x=>x.purpose===key).length,connections:spec[collection[1]].filter(x=>x.purpose===key).length}]));
   const answer=await browser.cdp.send('Runtime.evaluate',{expression:`(async()=>{
    const browserErrors=[];window.addEventListener('error',e=>browserErrors.push(e.message));window.addEventListener('unhandledrejection',e=>browserErrors.push(String(e.reason)));
    const frame=()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));
    const views=[];
    for(const button of document.querySelectorAll('[data-guided-view-id]')){button.click();await frame();views.push({id:button.dataset.guidedViewId,focused:document.querySelector('svg').hasAttribute('data-focus-active')});}
    document.getElementById('btn-focus-clear')?.click();
    const initial=document.documentElement.getAttribute('data-theme');document.getElementById('btn-theme').click();await frame();const toggled=document.documentElement.getAttribute('data-theme');document.getElementById('btn-theme').click();await frame();
    const node=document.querySelector('[data-node-id]'),label=node.querySelector('text[data-node-label]').textContent;
    document.getElementById('btn-node-finder').click();
    const input=document.getElementById('node-finder-input');input.value=label;input.dispatchEvent(new Event('input',{bubbles:true}));await frame();
    const searchMatches=document.querySelectorAll('.node-finder-result').length;
    document.getElementById('node-finder-close').click();await frame();const searchClosed=document.getElementById('node-finder').hidden;
    node.dispatchEvent(new MouseEvent('click',{bubbles:true}));await frame();
    const focusOpened=document.getElementById('focus-chip').hidden===false;
    document.getElementById('btn-focus-clear').click();await frame();const focusClosed=document.getElementById('focus-chip').hidden===true;
    const legendErrors=[],filters=[];
    const svg=document.querySelector('.diagram-container svg'),key=document.getElementById('purpose-key');
    const expected=${JSON.stringify(expected)};
    const items=[...svg.querySelectorAll('[data-node-id][data-purpose],[data-edge-from][data-purpose]')];
    const buttons=[...key.querySelectorAll('[data-purpose-filter-button]')];
    const settle=()=>new Promise(r=>setTimeout(r,250));
    if(key.hidden||document.getElementById('purpose-key-title').textContent!=='Legend'||buttons[0].textContent!=='All Components')legendErrors.push('labels/visibility');
    const keys=Object.keys(expected).filter(k=>expected[k].nodes+expected[k].connections>0);
    if(JSON.stringify(keys.sort())!==JSON.stringify(buttons.slice(1).map(b=>b.dataset.purposeFilterButton).sort()))legendErrors.push('used colours');
    const choose=value=>key.querySelector('[data-purpose-filter-button="'+value+'"]').click();
    const clear=()=>!svg.hasAttribute('data-purpose-filter')&&!svg.querySelector('[data-purpose-muted],[data-purpose-highlight]')&&buttons[0].getAttribute('aria-pressed')==='true';
    for(const theme of ['light','dark']) {
     document.documentElement.setAttribute('data-theme',theme);
     for(const button of buttons.slice(1)) {
      button.click();await settle();
      const value=button.dataset.purposeFilterButton,errors=[];
      if(svg.getAttribute('data-purpose-filter')!==value)errors.push('filter not active');
      if(button.getAttribute('aria-pressed')!=='true'||buttons.filter(b=>b.getAttribute('aria-pressed')==='true').length!==1)errors.push('pressed state');
      const nodes=svg.querySelectorAll('[data-node-id][data-purpose-highlight]').length;
      const connections=new Set([...svg.querySelectorAll('[data-edge-from][data-purpose-highlight]')].map(e=>e.dataset.edgeId)).size;
      if(nodes!==expected[value].nodes||connections!==expected[value].connections)errors.push('highlighted counts');
      const counts=document.getElementById('purpose-key-counts').textContent.match(/\\d+/g).map(Number);
      if(counts[0]!==nodes||counts[1]!==connections)errors.push('displayed counts');
      for(const item of items) {
       const match=item.dataset.purpose===value,opacity=Number(getComputedStyle(item).opacity);
       if(item.hasAttribute('data-purpose-highlight')!==match||item.hasAttribute('data-purpose-muted')===match||(match?opacity<.99:opacity>.2))errors.push('emphasis '+(item.dataset.nodeId||item.dataset.edgeId));
      }
      if(!nodes&&!document.getElementById('purpose-key-description').textContent.includes('Highlighted connections:'))errors.push('connection-only description');
      for(const rail of svg.querySelectorAll(':scope > path[marker-end]:not([data-edge-from])'))if(Number(getComputedStyle(rail).opacity)>.2)errors.push('structural rail over-emphasized');
      filters.push({theme,purpose:value,nodes,connections,errors});
     }
     choose('all');await settle();if(!clear()||items.some(e=>Number(getComputedStyle(e).opacity)<.99))legendErrors.push('restore '+theme);
    }
    // Switching either way between the key and native exploration must leave
    // just one active form of emphasis, with truthful pressed states.
    buttons[1].click();node.dispatchEvent(new MouseEvent('click',{bubbles:true}));await frame();
    if(!clear()||!svg.hasAttribute('data-focus-active'))legendErrors.push('legend to node focus');
    buttons[1].click();await frame();if(svg.hasAttribute('data-focus-active')||!svg.hasAttribute('data-purpose-filter'))legendErrors.push('node focus to legend');
    const guided=document.querySelector('[data-guided-view-id]');
    if(guided){guided.click();await frame();if(!clear())legendErrors.push('legend to guided view');buttons[1].click();await frame();if(svg.hasAttribute('data-focus-active'))legendErrors.push('guided view to legend');}
    const kind=Archify.semanticLens.kinds()[0]?.id;
    if(kind){Archify.semanticLens.select(kind);await frame();if(!clear())legendErrors.push('legend to semantic lens');buttons[1].click();await frame();if(svg.hasAttribute('data-lens-active'))legendErrors.push('semantic lens to legend');}
    buttons[0].focus();
    buttons[0].dispatchEvent(new KeyboardEvent('keydown',{key:'End',bubbles:true}));
    if(document.activeElement!==buttons.at(-1))legendErrors.push('End key');
    buttons.at(-1).dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowRight',bubbles:true}));
    if(document.activeElement!==buttons[0])legendErrors.push('arrow wrap');
    buttons[1].focus();buttons[1].dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));await frame();
    if(!clear()||document.activeElement!==buttons[0])legendErrors.push('Escape key');
    buttons[1].click();const filtered=svg.getAttribute('data-purpose-filter');
    window.dispatchEvent(new Event('beforeprint'));
    const printReady=svg.getAttribute('viewBox')===svg.getAttribute('data-purpose-canonical-view-box')&&!svg.hasAttribute('data-purpose-interactive')&&!svg.hasAttribute('data-purpose-filter')&&getComputedStyle(svg.querySelector('[data-purpose-legend]')).display!=='none';
    window.dispatchEvent(new Event('afterprint'));
    if(!printReady||svg.getAttribute('data-purpose-filter')!==filtered||svg.getAttribute('viewBox')!==svg.getAttribute('data-purpose-diagram-view-box'))legendErrors.push('print/restoration');
    choose('all');await frame();
    const exports=[];
    if(${JSON.stringify(exportNames.includes(name))}) {
     const blobs=new Map(),downloads=[];const create=URL.createObjectURL.bind(URL);
     URL.createObjectURL=blob=>{const url=create(blob);blobs.set(url,blob);return url;};
     HTMLAnchorElement.prototype.click=function(){downloads.push({name:this.download,blob:blobs.get(this.href)});};
     for(const theme of ['light','dark']) for(const format of ['svg','png']){
      document.documentElement.setAttribute('data-theme',theme);await frame();
      buttons[1].click();await frame();
      await Archify.exportMenu.run(format);
      const html=document.documentElement,download=downloads.at(-1),blob=download?.blob;
      let clean=true;const colourErrors=[];
      const palette=${JSON.stringify(palette)};
      const keys=[...new Set([...document.querySelectorAll('.diagram-container svg [data-purpose]')].map(e=>e.dataset.purpose))];
      const rgb=hex=>[1,3,5].map(n=>parseInt(hex.slice(n,n+2),16));
      if(format==='svg'&&blob){
       const xml=new DOMParser().parseFromString(await blob.text(),'image/svg+xml');clean=!xml.querySelector('parsererror,script,[data-view-scale],.diagram-nav,#focus-chip,.node-finder,[data-focus-active],[data-purpose-filter],[data-purpose-interactive],[data-purpose-muted],[data-purpose-highlight]')&&xml.querySelectorAll('[data-purpose-legend-entry]').length===keys.length&&xml.documentElement.getAttribute('viewBox')===svg.getAttribute('data-purpose-canonical-view-box');
       const iframe=document.createElement('iframe');iframe.style.cssText='position:fixed;left:-10000px;width:1600px;height:1200px';
       const loaded=new Promise((resolve,reject)=>{iframe.onload=resolve;iframe.onerror=reject;});iframe.srcdoc='<!doctype html><html><body>'+await blob.text()+'</body></html>';document.body.append(iframe);await loaded;
       for(const exportedTheme of ['light','dark']){
        const doc=iframe.contentDocument;doc.querySelector('svg').setAttribute('data-theme',exportedTheme);
        if(iframe.contentWindow.getComputedStyle(doc.querySelector('[data-purpose-legend]')).display==='none')colourErrors.push('export key hidden');
        for(const e of doc.querySelectorAll('[data-node-id],[data-edge-from]'))if(Number(iframe.contentWindow.getComputedStyle(e).opacity)!==1)colourErrors.push('export retains dimming');
        for(const n of doc.querySelectorAll('[data-node-id][data-purpose]')){
         const actual=iframe.contentWindow.getComputedStyle(n.querySelector('rect:not(.c-mask)')).stroke;
         if(actual!=='rgb('+rgb(palette[n.dataset.purpose][exportedTheme]).join(', ')+')')colourErrors.push(n.dataset.nodeId+':'+exportedTheme+':'+actual);
        }
        for(const e of doc.querySelectorAll('path.purpose-arrow')){
         const key=e.closest('[data-edge-from]').dataset.purpose;
         const actual=iframe.contentWindow.getComputedStyle(e).stroke;
         if(actual!=='rgb('+rgb(palette[key][exportedTheme]).join(', ')+')')colourErrors.push('edge:'+exportedTheme+':'+actual);
        }
       }iframe.remove();
      }
      if(format==='png'&&blob){
       const bitmap=await createImageBitmap(blob),canvas=document.createElement('canvas');canvas.width=bitmap.width;canvas.height=bitmap.height;
       const ctx=canvas.getContext('2d');ctx.drawImage(bitmap,0,0);bitmap.close();
       const pixels=ctx.getImageData(0,0,canvas.width,canvas.height).data;
       for(const key of keys){const [r,g,b]=rgb(palette[key][theme]);let count=0;
        for(let i=0;i<pixels.length;i+=4)if(Math.abs(pixels[i]-r)<=3&&Math.abs(pixels[i+1]-g)<=3&&Math.abs(pixels[i+2]-b)<=3&&pixels[i+3]>240)count++;
        if(count<5)colourErrors.push('PNG missing '+key+':'+theme);
       }
      }
      exports.push({format,theme,bytes:blob?.size||0,canonical:html.getAttribute('data-last-export-canonical'),error:html.getAttribute('data-last-export-error'),clean,colourErrors});
     }
     document.documentElement.setAttribute('data-theme',initial);await frame();
    }
    choose('all');document.documentElement.setAttribute('data-theme',initial);await frame();
    return {browserErrors,legendErrors,filters,views,themeChanged:initial!==toggled,themeRestored:document.documentElement.getAttribute('data-theme')===initial,searchMatches,searchClosed,focusOpened,focusClosed,exports};
   })()`,awaitPromise:true,returnByValue:true},session);
   if(answer.exceptionDetails)throw new Error(JSON.stringify(answer.exceptionDetails));
   const r={name,artifactSha256:createHash('sha256').update(fs.readFileSync(artifactPath)).digest('hex'),...answer.result.value};
   r.ok=r.browserErrors.length===0&&r.legendErrors.length===0&&r.filters.every(f=>f.errors.length===0)&&r.views.every(v=>v.focused)&&r.themeChanged&&r.themeRestored&&r.searchMatches>0&&r.searchClosed&&r.focusOpened&&r.focusClosed&&r.exports.every(e=>e.bytes>1000&&e.canonical==='true'&&!e.error&&e.clean&&e.colourErrors.length===0);
   results.push(r);console.log('interactions',name,r.ok,r.exports.length,JSON.stringify([...new Set([...r.browserErrors,...r.legendErrors,...r.filters.flatMap(f=>f.errors),...r.exports.flatMap(e=>e.colourErrors)])].slice(0,12)));
  }
 } finally {await browser.close();fs.mkdirSync(path.dirname(output),{recursive:true});fs.writeFileSync(output,JSON.stringify(results,null,2)+'\n');}
 if(results.some(r=>!r.ok))process.exitCode=1;
}

if(process.argv[1]&&path.resolve(process.argv[1])===fileURLToPath(import.meta.url))await main();
