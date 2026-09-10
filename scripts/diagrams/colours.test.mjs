import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { test, after } from 'node:test';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'dm-purpose-check-'));
after(() => fs.rmSync(temporary, { recursive: true, force: true }));
const source = JSON.parse(fs.readFileSync(path.join(root, 'docs/diagrams/src/flow-scep-issuance.sequence.json'), 'utf8'));

function validate(change) {
  const candidate = structuredClone(source);
  change(candidate);
  const file = path.join(temporary, 'candidate.sequence.json');
  fs.writeFileSync(file, JSON.stringify(candidate));
  const result = spawnSync(process.execPath, [path.join(root, 'scripts/diagrams/archify.mjs'), 'validate', 'sequence', file, '--quality', 'showcase', '--json'], { encoding: 'utf8' });
  assert.equal(result.error, undefined);
  return { status: result.status, receipt: JSON.parse(result.stdout) };
}

test('purpose extension keeps all nine artifact checks enabled', () => {
  const result = validate(() => {});
  assert.equal(result.status, 0);
  assert.equal(result.receipt.checks.length, 9);
  assert.ok(result.receipt.checks.every(check => check.ok));
});

for (const [name, change] of [
  ['missing node purpose', d => { delete d.participants[0].purpose; }],
  ['invalid relationship purpose', d => { d.messages[0].purpose = 'green-means-security'; }],
  ['missing relationship identity', d => { delete d.messages[0].id; }],
  ['legacy card colour', d => { d.cards[0].dot = 'rose'; }],
  ['unknown profile', d => { d.meta.colour_profile = 'anything'; }],
  ['unknown core property', d => { d.participants[0].unexpected = true; }],
  ['invalid relationship target', d => { d.messages[0].to = 'nonexistent'; }],
  ['wrongly nested purpose', d => { d.meta.purpose = 'service'; }],
]) {
  test(`rejects ${name}`, () => {
    const result = validate(change);
    assert.notEqual(result.status, 0);
    assert.equal(result.receipt.ok, false);
  });
}
