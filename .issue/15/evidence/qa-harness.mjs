import { appendFileSync, chmodSync, cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

import {
  graphDocumentDigest,
  issueSnapshotDigest,
} from './../../../skills/issue-onboard/scripts/issue-onboard.mjs';

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const sourceOnboard = path.join(repo, 'skills', 'issue-onboard');
const sourceSync = path.join(repo, 'skills', 'issue-sync');
const sourceOntology = path.join(repo, 'tools', 'issue-ontology');
const fields = ['problem', 'outcome', 'scope', 'acceptance', 'result', 'components', 'decisions', 'evidence'];
const now = '2026-08-28T00:00:00.000Z';

function issue(number, title = `Issue ${number}`, extra = {}) {
  return {
    number, title, labels: [], url: `https://github.com/o/r/issues/${number}`,
    state: 'OPEN', updatedAt: 'r', body: '', comments: [], ...extra,
  };
}

function graphFor(issues) {
  const nodes = {};
  for (const it of issues) {
    const source = { url: it.url, revision: it.updatedAt ?? 'unknown', observedAt: now };
    nodes[String(it.number)] = {
      id: `github:o/r#${it.number}`, number: it.number, title: it.title,
      status: it.state === 'CLOSED' || it.state === 'MERGED' ? 'close' : 'open',
      labels: (it.labels ?? []).map((l) => typeof l === 'string' ? l : l.name).filter((l) => !String(l).startsWith('status:')),
      url: it.url,
      context: Object.fromEntries(fields.map((f) => [f, { value: 'unknown', reason: 'fixture', source }])),
      provenance: source,
    };
  }
  const graph = {
    version: 2, provider: 'github', repository: 'o/r', updatedAt: now,
    snapshot: { status: 'complete', fetchedAt: now, digest: issueSnapshotDigest(issues), reason: null },
    nodes, edges: [],
  };
  graph.snapshot.graphDigest = graphDocumentDigest(graph);
  return graph;
}

function executable(file, text) {
  writeFileSync(file, text, 'utf8');
  chmodSync(file, 0o755);
}

function copyTrustedInstall(root, { actualSync = false, stubOnboard = null } = {}) {
  const install = path.join(root, 'trusted-install');
  cpSync(sourceOnboard, path.join(install, 'skills', 'issue-onboard'), { recursive: true, dereference: true });
  mkdirSync(path.join(install, 'tools', 'issue-ontology'), { recursive: true });
  cpSync(path.join(sourceOntology, 'validate.mjs'), path.join(install, 'tools', 'issue-ontology', 'validate.mjs'));
  cpSync(path.join(sourceOntology, 'schemas'), path.join(install, 'tools', 'issue-ontology', 'schemas'), { recursive: true });
  cpSync(path.realpathSync(path.join(sourceOntology, 'node_modules')), path.join(install, 'tools', 'issue-ontology', 'node_modules'), { recursive: true, dereference: true });
  if (stubOnboard !== null) {
    writeFileSync(path.join(install, 'skills', 'issue-onboard', 'scripts', 'issue-onboard.mjs'), stubOnboard, 'utf8');
  }
  if (actualSync) {
    cpSync(sourceSync, path.join(install, 'skills', 'issue-sync'), { recursive: true, dereference: true });
  }
  return install;
}

function makeFixture({ issues, initialGraph = null, syncGraph = null, syncMode = 'complete', paginate = false, trusted = false, actualSync = false, stubOnboard = null, externalOntology = null, symlinkIssue = false, syncProbe = null } = {}) {
  const root = mkdtempSync(path.join(os.tmpdir(), 'issue-15-qa-'));
  mkdirSync(path.join(root, 'bin'), { recursive: true });
  spawnSync('git', ['init', '--quiet', root], { encoding: 'utf8' });
  const log = path.join(root, 'gh.log');
  const issueJson = JSON.stringify(issues);
  executable(path.join(root, 'bin', 'gh'), `#!/usr/bin/env node
import { appendFileSync } from 'node:fs';
const a = process.argv.slice(2).join(' ');
appendFileSync(process.env.GH_LOG, a + '\\n');
if (a.includes('issue list')) {
  const all = JSON.parse(process.env.GH_ISSUES_JSON);
  const m = a.match(/--limit (\\d+)/);
  const limit = Number(m?.[1] ?? all.length);
  const out = process.env.GH_PAGINATE === '1' && limit === 200 ? all.slice(0, 200) : all.slice(0, limit);
  console.log(JSON.stringify(out));
} else if (a.includes('repo view')) console.log(JSON.stringify({ nameWithOwner: 'o/r' }));
else if (a.includes('api graphql')) console.log(JSON.stringify({ data: { repository: { issue: { blockedBy: { nodes: [] } } } } }));
else console.log('{}');
`);

  let entryRoot = root;
  if (trusted) entryRoot = copyTrustedInstall(root, { actualSync, stubOnboard });
  else cpSync(sourceOnboard, path.join(root, 'skills', 'issue-onboard'), { recursive: true, dereference: true });
  const entry = path.join(entryRoot, 'skills', 'issue-onboard', 'scripts', 'issue-onboard.mjs');
  const syncFile = path.join(entryRoot, 'skills', 'issue-sync', 'scripts', 'issue-sync.mjs');
  if (!actualSync && syncProbe !== null) {
    mkdirSync(path.dirname(syncFile), { recursive: true });
    writeFileSync(syncFile, syncProbe, 'utf8');
  } else if (!actualSync && trusted) {
    mkdirSync(path.dirname(syncFile), { recursive: true });
    executable(syncFile, `#!/usr/bin/env node
import { mkdirSync, writeFileSync } from 'node:fs';
const mode = ${JSON.stringify(syncMode)};
if (mode === 'partial') { console.log('SNAPSHOT_STATUS=partial'); console.log('GRAPH_SYNC=failed'); process.exit(0); }
if (mode === 'failed') { console.log('SYNC_FAILED=1'); process.exit(7); }
${syncProbe ?? `const graph = ${JSON.stringify(syncGraph ?? graphFor(issues))};
mkdirSync('.issue', { recursive: true });
writeFileSync('.issue/graph.json', JSON.stringify(graph) + '\\n', 'utf8');`}
console.log('SNAPSHOT_STATUS=complete');
console.log('GRAPH_SYNC=ok');
`);
  }
  if (initialGraph !== null || symlinkIssue) {
    if (symlinkIssue) {
      const outside = mkdtempSync(path.join(os.tmpdir(), 'issue-15-outside-'));
      symlinkSync(outside, path.join(root, '.issue'), 'dir');
    } else {
      mkdirSync(path.join(root, '.issue'), { recursive: true });
      writeFileSync(path.join(root, '.issue', 'graph.json'), typeof initialGraph === 'string' ? initialGraph : JSON.stringify(initialGraph) + '\n', 'utf8');
    }
  }
  const env = {
    ...process.env, PATH: `${path.join(root, 'bin')}${path.delimiter}${process.env.PATH ?? ''}`,
    GH_LOG: log, GH_ISSUES_JSON: issueJson, GH_PAGINATE: paginate ? '1' : '0',
  };
  if (externalOntology) env.ISSUE_ONTOLOGY_ROOT = externalOntology;
  if (syncProbe?.includes('BOOTSTRAP_SENTINEL')) env.BOOTSTRAP_SENTINEL = 'secret-sentinel';
  return { root, entry, syncEntry: path.join(entryRoot, 'skills', 'issue-sync', 'scripts', 'issue-sync.mjs'), entryRoot, log, env };
}

function run(fixture, args = ['onboard', '--no-llm']) {
  const result = spawnSync(process.execPath, [fixture.entry, ...args], { cwd: fixture.root, env: fixture.env, encoding: 'utf8' });
  return { status: result.status, stdout: result.stdout ?? '', stderr: result.stderr ?? '' };
}

function report(id, fixture, result, check, extra = '') {
  const ghLog = existsSync(fixture.log) ? readFileSync(fixture.log, 'utf8').trim().replaceAll('\n', ' | ') : '';
  const compact = (value) => value.length > 1000 ? `${JSON.stringify(value.slice(0, 400))}...[length=${value.length}]...${JSON.stringify(value.slice(-400))}` : JSON.stringify(value);
  console.log(`SCENARIO=${id}`);
  console.log(`EXIT=${result.status}`);
  console.log(`STDOUT=${compact(result.stdout)}`);
  console.log(`STDERR=${compact(result.stderr)}`);
  console.log(`GH_CALLS=${JSON.stringify(ghLog)}`);
  console.log(`CHECK=${check}`);
  if (extra) console.log(extra);
  console.log('---');
}

const one = [issue(1)];

{
  const f = makeFixture({ issues: one, syncGraph: graphFor(one) });
  const r = run(f);
  report('missing-cache-bootstrap', f, r, r.status === 0 && /GRAPH_BOOTSTRAP_REASON=missing/.test(r.stdout) && /ONBOARD_COUNT=1/.test(r.stdout) && existsSync(path.join(f.root, '.issue', 'graph.json')) ? 'PASS' : 'FAIL');
  rmSync(f.root, { recursive: true, force: true });
}
{
  const old = [issue(1, 'Old title', { updatedAt: 'old' })];
  const f = makeFixture({ issues: one, initialGraph: graphFor(old), syncGraph: graphFor(one) });
  const r = run(f);
  report('stale-cache-recovery', f, r, r.status === 0 && /GRAPH_BOOTSTRAP_REASON=snapshot-changed/.test(r.stdout) && /ONBOARD_COUNT=1/.test(r.stdout) ? 'PASS' : 'FAIL');
  rmSync(f.root, { recursive: true, force: true });
}
{
  const f = makeFixture({ issues: one, initialGraph: '{ malformed', syncGraph: graphFor(one) });
  const r = run(f);
  report('invalid-cache-recovery', f, r, r.status === 0 && /GRAPH_BOOTSTRAP_REASON=invalid/.test(r.stdout) && /ONBOARD_COUNT=1/.test(r.stdout) ? 'PASS' : 'FAIL');
  rmSync(f.root, { recursive: true, force: true });
}
{
  const many = Array.from({ length: 201 }, (_, i) => issue(i + 1));
  const f = makeFixture({ issues: many, paginate: true, syncGraph: graphFor(many) });
  const r = run(f);
  const calls = readFileSync(f.log, 'utf8');
  report('pagination-beyond-200', f, r, r.status === 0 && calls.includes('--limit 200') && calls.includes('--limit 400') && /ONBOARD_COUNT=6/.test(r.stdout) && /MORE_AVAILABLE=1/.test(r.stdout) ? 'PASS' : 'FAIL');
  rmSync(f.root, { recursive: true, force: true });
}
for (const mode of ['partial', 'failed']) {
  const f = makeFixture({ issues: one, syncMode: mode });
  const r = run(f);
  report(`sync-${mode}-fail-closed`, f, r, r.status !== 0 && !/ONBOARD_COUNT=/.test(r.stdout) && !/PRIORITY=/.test(r.stderr) ? 'PASS' : 'FAIL');
  rmSync(f.root, { recursive: true, force: true });
}
{
  const f = makeFixture({ issues: one, initialGraph: graphFor(one), externalOntology: '/private/tmp/issue-15-ontology-does-not-exist' });
  const r = run(f);
  report('ontology-unavailable-fail-closed', f, r, r.status !== 0 && !/ONBOARD_COUNT=/.test(r.stdout + r.stderr) ? 'PASS' : 'FAIL');
  rmSync(f.root, { recursive: true, force: true });
}
{
  const marker = path.join(os.tmpdir(), `issue-15-repository-sync-${process.pid}`);
  const f = makeFixture({ issues: one, trusted: true });
  const probe = `import { writeFileSync } from 'node:fs'; writeFileSync(${JSON.stringify(marker)}, process.env.BOOTSTRAP_SENTINEL ?? 'absent'); console.log('SNAPSHOT_STATUS=complete'); console.log('GRAPH_SYNC=ok');`;
  mkdirSync(path.join(f.root, 'skills', 'issue-sync', 'scripts'), { recursive: true });
  writeFileSync(path.join(f.root, 'skills', 'issue-sync', 'scripts', 'issue-sync.mjs'), probe, 'utf8');
  const r = run(f);
  report('trusted-install-no-repository-fallback', f, r, r.status !== 0 && !existsSync(marker) ? 'PASS' : 'FAIL');
  rmSync(f.root, { recursive: true, force: true });
  rmSync(marker, { force: true });
}
{
  const observed = path.join(os.tmpdir(), `issue-15-env-${process.pid}`);
  const probe = `import { mkdirSync, writeFileSync } from 'node:fs'; const p=${JSON.stringify(observed)}; writeFileSync(p, [process.env.BOOTSTRAP_SENTINEL ? 'leaked' : 'stripped', process.env.NODE_OPTIONS ? 'node-options-leaked' : 'node-options-stripped'].join('|')); mkdirSync('.issue', { recursive: true }); writeFileSync('.issue/graph.json', ${JSON.stringify(JSON.stringify(graphFor(one)) + '\n')}); console.log('SNAPSHOT_STATUS=complete'); console.log('GRAPH_SYNC=ok');`;
  const f = makeFixture({ issues: one, trusted: true, syncProbe: probe });
  f.env.NODE_OPTIONS = '--require=not-real';
  f.env.BOOTSTRAP_SENTINEL = 'secret-sentinel';
  const r = run(f);
  const observedText = existsSync(observed) ? readFileSync(observed, 'utf8') : '';
  report('bootstrap-environment-stripping', f, r, r.status === 0 && observedText === 'stripped|node-options-stripped' ? 'PASS' : 'FAIL', `ENV_OBSERVED=${observedText}`);
  rmSync(f.root, { recursive: true, force: true });
  rmSync(observed, { force: true });
}
{
  const f = makeFixture({ issues: one, trusted: true, actualSync: true, symlinkIssue: true });
  const r = run(f);
  const outside = readFileSync(path.join(f.root, '.issue'), 'utf8');
  report('symlink-issue-rejection', f, r, r.status !== 0 && !/GRAPH_BOOTSTRAP=/.test(r.stdout) && !outside.includes('graph.json') ? 'PASS' : 'FAIL', 'SYMLINK_READ=' + JSON.stringify(outside));
  rmSync(f.root, { recursive: true, force: true });
}
{
  const stub = `console.log('SNAPSHOT_STATUS=complete-but-not-a-marker'); console.log('GRAPH_SYNC=okay');`;
  const f = makeFixture({ issues: one, trusted: true, actualSync: true, stubOnboard: stub });
  const r = spawnSync(process.execPath, [f.syncEntry], { cwd: f.root, env: f.env, encoding: 'utf8' });
  report('exact-marker-parsing', f, r, r.status !== 0 && /GRAPH_SYNC=failed/.test(r.stdout) ? 'PASS' : 'FAIL');
  rmSync(f.root, { recursive: true, force: true });
}
{
  const f = makeFixture({ issues: one, trusted: true, syncProbe: `process.stdout.write('x'.repeat(17 * 1024 * 1024));` });
  const r = run(f);
  report('bootstrap-output-bound', f, r, r.status !== 0 && !/ONBOARD_COUNT=/.test(r.stdout), `STDOUT_LENGTH=${r.stdout.length}`);
  rmSync(f.root, { recursive: true, force: true });
}
{
  const target = path.join(repo, 'skills', 'issue-onboard', 'scripts', 'issue-onboard.mjs');
  const script = `import { runSyncBootstrap } from ${JSON.stringify(pathToFileURL(target).href)}; const r=runSyncBootstrap('/qa', { resolve:()=>'/trusted/issue-sync.mjs', spawn:(_c,_a,o)=>({status:0,stdout:'SNAPSHOT_STATUS=complete\\nGRAPH_SYNC=ok\\n',stderr:'', options:o}), env:{PATH:'/bin', BOOTSTRAP_SENTINEL:'secret'} }); console.log(JSON.stringify(r.options));`;
  const r = spawnSync(process.execPath, ['--input-type=module', '-e', script], { encoding: 'utf8' });
  report('bootstrap-timeout-bound', { log: path.join(os.tmpdir(), 'none') }, { status: r.status, stdout: r.stdout, stderr: r.stderr }, r.status === 0 && /"timeout":120000/.test(r.stdout) && /"maxBuffer":16777216/.test(r.stdout) && !/BOOTSTRAP_SENTINEL/.test(r.stdout) ? 'PASS' : 'FAIL');
}
