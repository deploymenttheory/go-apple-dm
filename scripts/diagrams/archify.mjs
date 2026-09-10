#!/usr/bin/env node
// Project reading profile for Archify 2.17. Patches an isolated copy before
// validation/delivery; neither the installed skill nor delivered HTML is edited.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {colourStyles} from './colours.mjs';
const here=path.dirname(fileURLToPath(import.meta.url));
const skill=path.resolve(process.env.ARCHIFY_SKILL_DIR || path.join(os.homedir(),'.agents/skills/archify'));
const version=JSON.parse(fs.readFileSync(path.join(skill,'package.json'),'utf8')).version;
if (!version.startsWith('2.17.')) throw new Error(`Reading profile requires Archify 2.17; found ${version}. Review the patch anchors before upgrading.`);
const css=fs.readFileSync(path.join(here,'reading.css'),'utf8')+'\n'+colourStyles();
const colourProfile=fs.readFileSync(path.join(here,'colours.mjs'),'utf8');
const upstreamEntries = ['assets', 'bin', 'renderers', 'scripts', 'schemas', 'package.json'];
const upstreamHasher = createHash('sha256');
function fingerprint(relative) {
  const absolute = path.join(skill, relative);
  if (fs.statSync(absolute).isDirectory()) {
    for (const child of fs.readdirSync(absolute).sort()) fingerprint(path.join(relative, child));
  } else {
    const bytes = fs.readFileSync(absolute);
    upstreamHasher.update(relative).update('\0').update(String(bytes.length)).update('\0').update(bytes);
  }
}
upstreamEntries.forEach(fingerprint);
const upstreamSha256 = upstreamHasher.digest('hex');
const hash = createHash('sha256')
  .update(fs.readFileSync(fileURLToPath(import.meta.url)))
  .update(css).update(colourProfile).update(upstreamSha256).digest('hex');
const cache=path.join(os.tmpdir(),'go-apple-dm-archify-'+hash.slice(0,16));
let root=cache;
if (!fs.existsSync(path.join(root,'.ready'))) {
  root=fs.mkdtempSync(path.join(os.tmpdir(),'go-apple-dm-archify-build-'));
  for(const name of upstreamEntries) fs.cpSync(path.join(skill,name),path.join(root,name),{recursive:true});
  function edit(file,from,to){const p=path.join(root,file),old=fs.readFileSync(p,'utf8');if(!(typeof from === 'string' ? old.includes(from) : old.match(from)))throw new Error(`Missing Archify patch anchor in ${file}: ${from}`);fs.writeFileSync(p,old.replace(from,to));}
  edit('renderers/shared/utils.mjs', /<html lang="\$\{esc\(resolvedLocale\)\}"/, '<html data-reading-layout="scroll" lang="${esc(resolvedLocale)}"');
  edit('assets/template.html',/    <\/style>|  <\/style>/,`\n${css}\n  </style>`);
  edit('assets/template.html',/shell && diagram && svg && ratio >= WIDE_RATIO &&/,'html.getAttribute("data-reading-layout") !== "scroll" && shell && diagram && svg && ratio >= WIDE_RATIO &&');
  // Match the full-width desktop reader when calculating projected text sizes.
  edit('renderers/shared/desktop-readability.mjs',/DESKTOP_READER_MIN_WIDTH = 960/,'DESKTOP_READER_MIN_WIDTH = 1376');
  for (const file of ['architecture/render-architecture.mjs','sequence/render-sequence.mjs','lifecycle/render-lifecycle.mjs','dataflow/render-dataflow.mjs','workflow/workflow-compiler.mjs']) {
    const p='renderers/'+file;
    edit(p,/sublabelPreferred: [789],/,'sublabelPreferred: 11,');
    edit(p,/sublabelMinimum: 6,/,'sublabelMinimum: 8.5,');
    if(file.startsWith('workflow')) {edit(p,/labelPreferred: 11,/,'labelPreferred: 14,');}
    else edit(p,/(brandLabelFitWidth\([^\n]+\), )1[01], 8\)/,'$114, 10)');
    // The masks and routing measurements grow together with relationship text.
    if(file.startsWith('architecture') || file.startsWith('workflow')) {
      edit(p,/\* 4\.8 \+ 10/g,'* 6 + 12');
      edit(p,/font-size="8" text-anchor="middle">\$\{esc\((conn|edge)\.label\)\}/g,'font-size="10" text-anchor="middle">${esc($1.label)}');
    } else if(file.startsWith('sequence')) {
      edit(p,/textUnits\(message.label\) \* 5\.2 \+ 12/g,'textUnits(message.label) * 6.6 + 12');
      edit(p,/font-size="9" text-anchor="middle">\$\{esc\(message.label\)\}/,'font-size="11" text-anchor="middle">${esc(message.label)}');
    } else {
      edit(p,/\* 4\.9 \+ 12/g,'* 6 + 12');
      edit(p,/font-size="8" text-anchor="middle">\$\{esc\((flow|transition)\.label\)\}/g,'font-size="10" text-anchor="middle">${esc($1.label)}');
    }
  }
  // More room for legible state/data-flow nodes; source files retain semantic ranks.
  edit('renderers/lifecycle/render-lifecycle.mjs',/phaseXs: \[94, 248, 402, 556, 710\]/,'phaseXs: [150, 385, 620, 855, 1090]');
  edit('renderers/lifecycle/render-lifecycle.mjs',/eventXs: \[402, 556, 710\]/,'eventXs: [620, 855, 1090]');
  edit('renderers/lifecycle/render-lifecycle.mjs',/outcomeXs: \[402, 556, 710\]/,'outcomeXs: [620, 855, 1090]');
  edit('renderers/dataflow/render-dataflow.mjs',/leftX: 100/,'leftX: 150');
  edit('renderers/dataflow/render-dataflow.mjs',/colGap: 215/,'colGap: 310');
  edit('renderers/dataflow/render-dataflow.mjs',/stageW: 168/,'stageW: 255');
  // Public references are authored as plain title + URL strings. Escape both
  // text and href before adding links; this is not arbitrary HTML in JSON.
  edit('renderers/shared/utils.mjs',/export function renderCards\(cards\) \{/,
`function linkedCardText(value) {
  const text = String(value), match = text.match(/^(.*?) — (https:\\/\\/[^\\s]+)$/);
  return match ? '<a href="' + esc(match[2]) + '" target="_blank" rel="noopener noreferrer">' + esc(match[1]) + '</a>' : esc(text);
}
export function renderCards(cards) {`);
  edit('renderers/shared/utils.mjs',/\$\{esc\(item\)\}/,'${linkedCardText(item)}');
  // Explicit purpose colours are a project schema extension. Component types
  // still describe architecture; line variants still describe interaction style.
  fs.writeFileSync(path.join(root,'renderers/shared/project-colours.mjs'),colourProfile);
  const purposes = ['service','transport','storage','authentication','certificate','policy','artifact','neutral','success','warning','failure'];
  for (const name of fs.readdirSync(path.join(root,'schemas')).filter(name => name.endsWith('.schema.json'))) {
    const file=path.join(root,'schemas',name), schema=JSON.parse(fs.readFileSync(file,'utf8'));
    function extend(value) {
      if (!value || typeof value !== 'object') return;
      const props=value.properties;
      if (props?.meta?.properties) props.meta.properties.colour_profile={const:'purpose-v1'};
      if (props?.id && props?.type && props?.label || props?.from && props?.to) {
        props.purpose={enum:purposes};
      }
      if (props?.dot?.enum && props?.items && props?.title) props.dot.enum=[...new Set([...props.dot.enum,...purposes])];
      Object.values(value).forEach(extend);
    }
    extend(schema);
    fs.writeFileSync(file,JSON.stringify(schema,null,2)+'\n');
  }
  edit('renderers/shared/cli.mjs', /^import fs/, "import { configureColours, colourAttrs, withColourLegend } from './project-colours.mjs';\nimport fs");
  edit('renderers/shared/validator.mjs', /^import /, "import { schemaInput } from './project-colours.mjs';\nimport ");
  edit('renderers/shared/validator.mjs', /if \(!validate\(data\)\)/, 'if (!validate(schemaInput(diagramType, data)))');
  edit('renderers/shared/cli.mjs', /  validateSchema\(diagramType, diagram\);/, '  validateSchema(diagramType, diagram);\n  configureColours(diagramType, diagram);');
  edit('renderers/shared/cli.mjs', /    svg,\n    cards: renderCards/, '    svg: withColourLegend(svg, meta),\n    cards: renderCards');
  edit('renderers/shared/cli.mjs', 'aria-pressed="false"${optional}', 'aria-pressed="false"${optional} ${colourAttrs(\'node\', id)}');
  edit('renderers/shared/cli.mjs', '${named}${keyed}${identified}', '${named}${keyed}${identified} ${colourAttrs(\'edge\', id)}');
  edit('renderers/shared/utils.mjs', /^import \{/, "import { colourDefinitions } from './project-colours.mjs';\nimport {");
  edit('renderers/shared/utils.mjs', /        <defs>/, '        <defs>\n${colourDefinitions()}');
  for (const [file, item, map] of [
    ['architecture/render-architecture.mjs','conn','arrowClassMap'],
    ['workflow/workflow-compiler.mjs','edge','arrowClassMap'],
    ['sequence/render-sequence.mjs','message','arrowClass'],
    ['dataflow/render-dataflow.mjs','flow','arrowClassMap'],
    ['lifecycle/render-lifecycle.mjs','transition','arrowClassMap'],
  ]) {
    edit('renderers/'+file, /^import /, "import { colourArrow } from '../shared/project-colours.mjs';\nimport ");
    edit('renderers/'+file,
      `const [cls, marker] = ${map}[${item}.variant || 'default'] || ${map}.default;`,
      `const [cls, marker] = colourArrow(${item}, ${map});`);
  }
  fs.writeFileSync(path.join(root,'.ready'),JSON.stringify({version,upstreamSha256,profileSha256:hash}));
  try { fs.renameSync(root,cache); } catch (error) {
    if (!fs.existsSync(path.join(cache,'.ready'))) throw error;
    fs.rmSync(root,{recursive:true,force:true});
  }
  root=cache;
}
if(process.argv[2]==='--prepare') console.log(root);
else {
  const result=spawnSync(process.execPath,[path.join(root,'bin/archify.mjs'),...process.argv.slice(2)],{stdio:'inherit',env:process.env});
  if(result.error)throw result.error;
  process.exitCode=result.status ?? 1;
}
