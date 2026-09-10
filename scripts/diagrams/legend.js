// Runs before Archify initializes its camera, so its viewport is the authored
// diagram without the static export key. No node or route geometry is changed.
(function () {
  const key = document.getElementById('purpose-key');
  const svg = document.querySelector('.diagram-container svg');
  if (!key || !svg) return;
  // Embed mode is a diagram-only surface; keep its complete static SVG key.
  if (document.documentElement.getAttribute('data-embed') === 'true') return;
  const data = JSON.parse(document.getElementById('purpose-key-data').textContent);
  const diagramViewBox = svg.getAttribute('data-purpose-diagram-view-box');
  if (!diagramViewBox) return;
  const canonicalViewBox = svg.getAttribute('viewBox');
  svg.setAttribute('data-purpose-canonical-view-box', canonicalViewBox);
  svg.setAttribute('data-purpose-interactive', '');
  svg.setAttribute('viewBox', diagramViewBox);
  key.hidden = false;
  const buttons = [...key.querySelectorAll('[data-purpose-filter-button]')];
  const items = [...svg.querySelectorAll('[data-node-id][data-purpose], [data-edge-from][data-purpose]')];
  let selected = 'all';
  const count = (n, label) => n + ' ' + label + (n === 1 ? '' : 's');

  function clearExploration() {
    const viewer = window.Archify;
    viewer?.semanticLens?.clear?.({ updateUrl: false, preserveView: true, closePanel: true });
    viewer?.routeProbe?.clear?.({ updateUrl: false, preserveView: true, restoreFocus: false });
    viewer?.guidedViews?.showAll?.({ clearFocus: false, updateUrl: false, resetView: false });
    viewer?.focus?.clear?.({ preserveView: true });
  }
  function select(value, userAction = false) {
    const entry = data.entries.find(entry => entry.key === value);
    if (value !== 'all' && !entry) return;
    if (userAction) clearExploration();
    selected = value;
    buttons.forEach(button => button.setAttribute('aria-pressed', String(button.dataset.purposeFilterButton === value)));
    if (entry) svg.setAttribute('data-purpose-filter', value);
    else svg.removeAttribute('data-purpose-filter');
    // Native story/lens overlays may clone a currently highlighted route.
    // Remove inherited filter state from those temporary shapes as well.
    svg.querySelectorAll('[data-purpose-muted], [data-purpose-highlight]').forEach(item => {
      item.removeAttribute('data-purpose-muted');
      item.removeAttribute('data-purpose-highlight');
    });
    items.forEach(item => {
      item.toggleAttribute('data-purpose-muted', !!entry && item.dataset.purpose !== value);
      item.toggleAttribute('data-purpose-highlight', !!entry && item.dataset.purpose === value);
    });
    document.getElementById('purpose-key-selection').textContent = entry ? entry.label : 'All Components';
    document.getElementById('purpose-key-counts').textContent = count(entry ? entry.nodes.length : data.nodes, 'component') + ' · ' + count(entry ? entry.connections.length : data.connections, 'connection');
    const examples = entry ? (entry.nodes.length
      ? ' Highlighted components: ' + entry.nodes.join('; ') + '.'
      : ' Highlighted connections: ' + entry.connections.join('; ') + '.') : '';
    document.getElementById('purpose-key-description').textContent = entry
      ? entry.meaning + examples
      : 'The complete diagram is shown. Select a colour to explore its role, or choose All Components to restore every component and connection. Colour identifies purpose or outcome; line style is independent.';
  }
  key.addEventListener('click', event => {
    const button = event.target.closest('[data-purpose-filter-button]');
    if (button) select(button.dataset.purposeFilterButton, true);
  });
  key.addEventListener('keydown', event => {
    const index = buttons.indexOf(document.activeElement);
    if (index < 0) return;
    let next;
    if (event.key === 'ArrowRight' || event.key === 'ArrowDown') next = (index + 1) % buttons.length;
    if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') next = (index + buttons.length - 1) % buttons.length;
    if (event.key === 'Home') next = 0;
    if (event.key === 'End') next = buttons.length - 1;
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); select('all', true); buttons[0].focus(); }
    if (next !== undefined) { event.preventDefault(); buttons[next].focus(); }
  });
  // Persistent native exploration takes over cleanly, including direct links,
  // node search, guided views, semantic lenses, and route selection.
  const nativeStates = ['data-focus-active', 'data-lens-active', 'data-route-active', 'data-route-picking', 'data-story-active', 'data-relationship-pin-active', 'data-intent-trace-active'];
  new MutationObserver(() => {
    if (selected !== 'all' && nativeStates.some(name => svg.hasAttribute(name))) select('all');
  }).observe(svg, { attributes: true, attributeFilter: nativeStates });
  window.addEventListener('beforeprint', () => {
    svg.removeAttribute('data-purpose-interactive');
    svg.removeAttribute('data-purpose-filter');
    svg.setAttribute('viewBox', canonicalViewBox);
  });
  window.addEventListener('afterprint', () => {
    svg.setAttribute('data-purpose-interactive', '');
    svg.setAttribute('viewBox', diagramViewBox);
    select(selected);
  });
  select('all');
})();
