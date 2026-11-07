import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { canonicalJsonBytes } from './issue-phase-contract.mjs';

const script = new URL('./issue-merge.mjs', import.meta.url).pathname;
function phase(data) {
  const dir = mkdtempSync(path.join(tmpdir(), 'merge-ci-'));
  try {
    const request = path.join(dir, 'request.json');
    writeFileSync(request, canonicalJsonBytes({ apiVersion: 'issue-phase/v1', contractId: 'issue-merge-phase-api-v1', phaseId: 'issue-merge.merge-verify', checkpoint: { id: 'ci-test', attempt: 1 }, data }));
    const result = spawnSync(process.execPath, [script, 'phase', '--request', request], { encoding: 'utf8' });
    assert.ok([0, 3].includes(result.status), result.stderr);
    return JSON.parse(result.stdout);
  } finally { rmSync(dir, { recursive: true, force: true }); }
}
for (const ci of ['success', 'none', 'failure', 'pending', 'unknown']) {
  test(`CI checkpoint preserves ${ci} and only accepts success or no checks`, () => {
    const result = phase({ subcheckpoint: 'ci', ci });
    assert.equal(result.data.ci, ci);
    assert.equal(result.handback.disposition, ['success', 'none'].includes(ci) ? 'complete' : 'held');
  });
}
const gates = { subcheckpoint: 'merge', pr: 157, onto: 'base', accumulatedOnto: 'base', baseHead: 'base', expectedBaseHead: 'base', headSha: 'a'.repeat(40), expectedHeadSha: 'a'.repeat(40), bodySha256: 'body', expectedBodySha256: 'body', checksSha256: 'checks', expectedChecksSha256: 'checks', preflightClean: true, criticVerdict: 'proceed', triggerCount: 0, mergeApproved: true };
test('no checks proposes the requested merge and records none honestly', () => {
  const result = phase({ ...gates, ci: 'none' });
  assert.equal(result.data.holdCode, 'EFFECT_PROPOSED');
  assert.deepEqual(result.proposedEffect.request.argv.slice(0, 3), ['merge', '--pr', '157']);
  assert.equal(result.observedFacts.find(f => f.kind === 'merge-gates').value.ci, 'none');
});
test('no checks does not bypass approval, preflight, or failed CI', () => {
  for (const [patch, code] of [[{ mergeApproved: false }, 'MERGE_APPROVAL_REQUIRED'], [{ preflightClean: false }, 'PREFLIGHT_NOT_CLEAN'], [{ ci: 'failure' }, 'CI_NOT_SUCCESSFUL']]) {
    const result = phase({ ...gates, ci: 'none', ...patch });
    assert.equal(result.data.holdCode, code);
    assert.equal(result.proposedEffect, null);
  }
});
