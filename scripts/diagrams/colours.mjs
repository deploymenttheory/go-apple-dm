// Copied into the isolated Archify renderer before validation and delivery.
// Authored purpose is independent of component type and relationship variant.
export const palette = {
  service: { label: 'Services and processing', light: '#2563eb', dark: '#60a5fa' },
  transport: { label: 'Communication and transport', light: '#087e98', dark: '#22d3ee' },
  storage: { label: 'Storage and persistence', light: '#0f766e', dark: '#2dd4bf' },
  authentication: { label: 'Authentication and identity', light: '#4338ca', dark: '#9397ff' },
  certificate: { label: 'Certificates and cryptography', light: '#7c3aed', dark: '#c084fc' },
  policy: { label: 'Authorization and policy', light: '#a21caf', dark: '#e879f9' },
  artifact: { label: 'Configuration and artifacts', light: '#80552f', dark: '#c99a6b' },
  neutral: { label: 'Actors, structure, neutral states', light: '#526174', dark: '#a8b4c5' },
  success: { label: 'Successful or accepted', light: '#15803d', dark: '#4ade80' },
  warning: { label: 'Warning, deferred, or retry', light: '#a65b00', dark: '#ffb347' },
  failure: { label: 'Failed, rejected, or invalid', light: '#c62828', dark: '#ff7373' },
};

const meanings = {
  service: 'Services and processing steps that perform the work shown in the diagram.',
  transport: 'Communication between components, including endpoints, protocol exchanges, and notifications.',
  storage: 'Records and persistence: where the system stores or retrieves information.',
  authentication: 'Checks that establish or verify an identity or credential.',
  certificate: 'Certificate issuance and cryptographic work, including signing, encryption, and keys.',
  policy: 'Authorization and policy decisions that determine whether an action is allowed.',
  artifact: 'Configuration and artifacts that the system consumes or produces.',
  neutral: 'Actors, structural boundaries, and states that do not imply success, warning, or failure.',
  success: 'An explicitly successful or accepted result.',
  warning: 'A warning, deferred action, or condition that requires a retry.',
  failure: 'An explicitly failed, rejected, or invalid result.',
};
const escapeHtml = value => String(value).replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));

const collections = {
  architecture: ['components', 'connections'], workflow: ['nodes', 'edges'],
  sequence: ['participants', 'messages'], dataflow: ['nodes', 'flows'],
  lifecycle: ['states', 'transitions'],
};
let current = null;

// Archify ships precompiled validators. Validate this small extension explicitly,
// then give those unchanged validators a copy containing every core field.
// Unknown properties elsewhere remain errors; the authored input is never edited.
export function schemaInput(type, diagram) {
  if (!Object.hasOwn(diagram.meta || {}, 'colour_profile')) return diagram;
  if (diagram.meta.colour_profile !== 'purpose-v1') throw new Error('Unknown diagram colour profile');
  const copy = structuredClone(diagram);
  delete copy.meta.colour_profile;
  for (const collection of collections[type]) {
    for (const item of copy[collection] || []) {
      if (!item.id || !Object.hasOwn(palette, item.purpose)) {
        throw new Error(`${collection}: ${item.id || item.label} needs a stable id and a valid purpose`);
      }
      delete item.purpose;
    }
  }
  for (const card of copy.cards || []) {
    if (!Object.hasOwn(palette, card.dot)) throw new Error(`Card ${card.title} needs a purpose colour`);
    card.dot = 'slate';
  }
  return copy;
}

export function configureColours(type, diagram) {
  current = null;
  if (!diagram.meta?.colour_profile) return;
  if (diagram.meta.colour_profile !== 'purpose-v1') throw new Error('Unknown diagram colour profile');
  if (diagram.meta.legend?.mode !== 'hidden') throw new Error('Purpose colours require the shared purpose legend');
  const [nodes, edges] = collections[type];
  current = { nodes: new Map(), edges: new Map(), used: new Set(), groups: {} };
  for (const [collection, target] of [[nodes, current.nodes], [edges, current.edges]]) {
    for (const item of diagram[collection] || []) {
      if (!item.id || !Object.hasOwn(palette, item.purpose)) {
        throw new Error(`${collection}: ${item.id || item.label} needs a stable id and a valid purpose`);
      }
      target.set(item.id, item.purpose);
      current.used.add(item.purpose);
      const group = current.groups[item.purpose] ||= { nodes: [], connections: [] };
      group[collection === nodes ? 'nodes' : 'connections'].push(item.label || item.id);
    }
  }
  for (const card of diagram.cards || []) {
    if (!Object.hasOwn(palette, card.dot)) throw new Error(`Card ${card.title} needs a purpose colour`);
  }
}

export function colourAttrs(kind, id) {
  if (!current) return '';
  const purpose = (kind === 'node' ? current.nodes : current.edges).get(id);
  if (!purpose) throw new Error(`Missing colour purpose for ${kind} ${id}`);
  return `data-purpose="${purpose}"`;
}

export function colourArrow(item, variants) {
  const original = variants[item.variant || 'default'] || variants.default;
  if (!current) return original;
  if (!Object.hasOwn(palette, item.purpose)) throw new Error(`Missing arrow purpose: ${item.id}`);
  // Security work is not inherently dashed. Only explicit dashed/return
  // variants describe line style; emphasis affects thickness, never outcome.
  const cls = item.variant === 'dashed' ? 'a-dashed' : item.variant === 'emphasis' ? 'a-emphasis' : 'a-default';
  return [`${cls} purpose-arrow purpose-${item.purpose}`, `purpose-arrowhead-${item.purpose}`];
}

export function colourDefinitions() {
  if (!current) return '';
  return Object.keys(palette).map(key => `<marker id="purpose-arrowhead-${key}" markerWidth="10" markerHeight="7" refX="9" refY="3.5" orient="auto"><polygon points="0 0, 10 3.5, 0 7" class="purpose-marker purpose-${key}"/></marker>`).join('\n');
}

export function withColourLegend(svg, meta) {
  if (!meta.colour_profile) return svg;
  const match = svg.match(/viewBox="0 0 ([\d.]+) ([\d.]+)"/);
  if (!match || !current) throw new Error('Cannot place purpose legend without diagram geometry');
  const width = Number(match[1]), height = Number(match[2]);
  const columns = Math.max(1, Math.floor((width - 80) / 310));
  const cellWidth = (width - 80) / columns;
  const entries = Object.entries(palette).filter(([key]) => current.used.has(key));
  const rows = Math.ceil(entries.length / columns);
  const base = height + 28, footer = base + 36 + rows * 30;
  const caption = 'Colour identifies purpose or outcome; line style does not imply success or failure.';
  const legend = `<g data-graph-role="legend" data-purpose-legend="" aria-label="Legend">
    <path d="M 40 ${height + 8} H ${width - 40}" class="purpose-legend-rule"/>
    <text x="40" y="${base}" class="t-primary" font-size="14" font-weight="600">Legend</text>
    ${entries.map(([key, entry], index) => {
      const x = 40 + index % columns * cellWidth, y = base + 30 + Math.floor(index / columns) * 30;
      return `<g data-purpose-legend-entry="${key}"><rect x="${x}" y="${y - 11}" width="13" height="13" rx="3" class="purpose-swatch purpose-${key}"/><text x="${x + 22}" y="${y}" class="t-primary" font-size="13">${entry.label}</text></g>`;
    }).join('\n')}
    <text x="40" y="${footer}" class="t-muted" font-size="12">${caption}</text>
  </g>`;
  return svg.replace(match[0], `viewBox="0 0 ${width} ${footer + 24}" data-purpose-diagram-view-box="0 0 ${width} ${height}"`).replace(/\s*<\/svg>\s*$/, `\n${legend}\n      </svg>`);
}

// Authored labels supply the examples and counts for every diagram type. The
// HTML key is outside the SVG; the SVG key remains the no-JS/export fallback.
export function colourLegendHtml() {
  if (!current) return '';
  const entries = Object.entries(palette).filter(([key]) => current.used.has(key)).map(([key, value]) => ({
    key, label: value.label, meaning: meanings[key], ...current.groups[key],
  }));
  const data = JSON.stringify({ entries, nodes: current.nodes.size, connections: current.edges.size }).replace(/</g, '\\u003c');
  return `<section class="purpose-key" id="purpose-key" aria-labelledby="purpose-key-title" hidden>
    <div class="purpose-key-heading"><h2 id="purpose-key-title">Legend</h2><p>Choose a colour to highlight its components and connections.</p></div>
    <div class="purpose-key-chips" role="group" aria-label="Highlight by colour">
      <button type="button" data-purpose-filter-button="all" aria-pressed="true">All Components</button>
      ${entries.map(entry => `<button type="button" data-purpose-filter-button="${entry.key}" aria-pressed="false" style="--entry:var(--purpose-${entry.key})"><span class="purpose-key-dot" aria-hidden="true"></span>${escapeHtml(entry.label)}</button>`).join('\n')}
    </div>
    <div class="purpose-key-detail" aria-live="polite" aria-atomic="true"><div><strong id="purpose-key-selection"></strong><span id="purpose-key-counts"></span></div><p id="purpose-key-description"></p></div>
    <script id="purpose-key-data" type="application/json">${data}</script>
  </section>`;
}

// CSS is generated from the same palette as the SVG legend and markers.
export function colourStyles() {
  const themes = ['light', 'dark'].map(theme => `[data-theme="${theme}"] {\n` +
    Object.entries(palette).map(([key, entry]) => {
      const hex = entry[theme].slice(1), rgb = [0, 2, 4].map(offset => parseInt(hex.slice(offset, offset + 2), 16));
      return `  --purpose-${key}: ${entry[theme]}; --purpose-${key}-fill: rgba(${rgb.join(',')},${theme === 'light' ? '.08' : '.10'});`;
    }).join('\n') + '\n}').join('\n');
  const tones = Object.keys(palette).map(key => `
svg [data-purpose="${key}"], svg .purpose-${key} { --purpose-stroke: var(--purpose-${key}); --purpose-fill: var(--purpose-${key}-fill); }
.card-dot.${key} { background: var(--purpose-${key}); }`).join('\n');
  return themes + tones + `
/* Literal colours are resolved by Archify's canonical export in both themes. */
svg [data-node-id][data-purpose] > rect:not(.c-mask) { fill: var(--purpose-fill); stroke: var(--purpose-stroke); }
svg [data-node-id][data-purpose] > .semantic-sigil { color: var(--purpose-stroke); }
svg [data-node-id][data-purpose] > text[data-detail="fine"] { fill: var(--purpose-stroke); }
svg .purpose-arrow { stroke: var(--purpose-stroke); fill: none; }
svg .purpose-marker, svg .purpose-swatch { fill: var(--purpose-stroke); }
svg [data-edge-from][data-purpose] text:not(.t-dim) { fill: var(--purpose-stroke); }
svg .purpose-legend-rule { stroke: var(--lane-stroke); stroke-width: 1; }
/* Boundaries describe structure, not a warning or an authorization result. */
svg .c-region, svg .c-security-group { stroke: var(--purpose-neutral); fill: none; }
svg [data-boundary-label] { fill: var(--purpose-neutral); }
/* Unclassified viewer accents and structural rails must not imply outcomes. */
[data-theme], html[data-reading-layout="scroll"] {
  --frontend-stroke: var(--purpose-transport); --backend-stroke: var(--purpose-service);
  --database-stroke: var(--purpose-storage); --cloud-stroke: var(--purpose-neutral);
  --security-stroke: var(--purpose-authentication); --messagebus-stroke: var(--purpose-transport);
  --external-stroke: var(--purpose-neutral); --arrow-emphasis: var(--purpose-service);
}
`;
}
