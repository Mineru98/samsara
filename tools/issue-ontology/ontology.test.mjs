import assert from 'node:assert/strict';
import { readFileSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  assertAction,
  validateActionDocument,
} from './validate.mjs';
import { readyFact } from '../../skills/issue-start/scripts/issue-start.mjs';
import { predecessorNumbers } from '../../skills/issue-start/scripts/issue-common.mjs';
import { resolveSkillScript } from '../../skills/issue-create/scripts/issue-common.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
test('start requires an open tracker issue and a ready result when checked', () => {
  const base = {
    gitRepo: true,
    trackerAuth: true,
    issueExists: true,
    trackerStateOpen: true,
  };
  assert.doesNotThrow(() => assertAction('start', { ...base, readyChecked: false }));
  assert.doesNotThrow(() => assertAction('start', {
    ...base,
    readyChecked: true,
    ready: true,
  }));
  assert.throws(() => assertAction('start', { ...base, readyChecked: true }), /schema validation failed/);
  assert.throws(() => assertAction('start', {
    ...base,
    trackerStateOpen: false,
    readyChecked: false,
  }), /schema validation failed/);
});

test('create has only git and tracker preconditions', () => {
  const schema = JSON.parse(readFileSync(
    path.join(root, 'tools/issue-ontology/schemas/actions/create.schema.json'),
    'utf8',
  ));
  assert.equal(Object.hasOwn(schema.properties, 'issue'), false);
  assert.equal(validateActionDocument({
    action: 'create',
    observed: { gitRepo: true, trackerAuth: true },
  }).valid, true);
  assert.throws(() => assertAction('create', {
    gitRepo: true,
    trackerAuth: false,
  }), /schema validation failed/);
});

test('end and merge require complete before and after evidence', () => {
  const end = { gitRepo: true, trackerAuth: true, issueExists: true, evidenceComplete: false };
  assert.throws(() => assertAction('end', end), /schema validation failed/);
  assert.doesNotThrow(() => assertAction('end', { ...end, evidenceComplete: true }));

  const merge = {
    action: 'merge',
    observed: { gitRepo: true, trackerAuth: true, issueExists: true, evidenceComplete: false },
  };
  assert.throws(() => assertAction(merge), /schema validation failed/);
  assert.doesNotThrow(() => assertAction({
    ...merge,
    observed: { ...merge.observed, evidenceComplete: true },
  }));
});

test('missing island is an explicit skip for the human end guard', () => {
  const empty = mkdtempSync(path.join(tmpdir(), 'issue-ontology-'));
  const script = path.join(root, 'skills/issue-end/scripts/issue-end.mjs');
  const result = spawnSync(process.execPath, [script, 'ontology-guard', '--skip-ok'], {
    cwd: root,
    encoding: 'utf8',
    env: { ...process.env, ISSUE_ONTOLOGY_ROOT: empty },
  });
  assert.equal(result.status, 0);
  assert.match(result.stdout, /ONTOLOGY_SKIPPED=1/);
});

test('sibling skills resolve from the plugin root without hidden install directories', () => {
  const script = resolveSkillScript('', 'issue-end', 'issue-end.mjs', { root });
  assert.equal(script, path.join(root, 'skills/issue-end/scripts/issue-end.mjs'));
});

test('start ready is unchecked when the body has no predecessors', () => {
  const tracker = { issueView: () => { throw new Error('조회하면 안 된다'); } };
  assert.deepEqual(readyFact({ body: '## 선행 이슈\n\n없음\n' }, tracker), { readyChecked: false });
  assert.deepEqual(readyFact({ body: '' }, tracker), { readyChecked: false });
});

test('start ready follows predecessor issues read from the tracker', () => {
  const body = '## 배경\n\n- #9 참고\n\n## 선행 이슈\n\n- [x] [#12](https://github.com/o/r/issues/12) 앞 작업\n- [ ] #15\n- #12\n- [ ] [#<번호>](<이슈 URL>) <제목>\n\n## 후행 이슈\n\n- #20\n';
  assert.deepEqual(predecessorNumbers(body), [12, 15]);
  const states = {
    12: { state: 'CLOSED', labels: [] },
    15: { state: 'OPEN', labels: [{ name: 'status:close' }] },
  };
  const done = { issueView: (n) => states[n] ?? null };
  assert.deepEqual(readyFact({ body }, done), { readyChecked: true, ready: true });

  const open = { issueView: (n) => (n === 15 ? { state: 'OPEN', labels: [{ name: 'status:review' }] } : states[n]) };
  assert.deepEqual(readyFact({ body }, open), { readyChecked: true, ready: false });

  const broken = { issueView: (n) => (n === 15 ? null : states[n]) };
  assert.deepEqual(readyFact({ body }, broken), { readyChecked: false });
});
