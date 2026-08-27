#!/bin/zsh
cd /tmp/samsara-issue-1-review-40c32a1 || exit 90
set +e
printf '%s\n' '=== S1 grok --version ==='
grok --version
printf 'EXIT=%s\n' "$?"
printf '%s\n' '=== S2 grok plugin validate . ==='
grok plugin validate .
printf 'EXIT=%s\n' "$?"
printf '%s\n' '=== S3 grok inspect --json ==='
PAGER=cat GROK_PAGER=cat TERM=dumb grok inspect --json | node -e 'let s=""; process.stdin.on("data", d => s += d).on("end", () => { const x=JSON.parse(s); console.log(JSON.stringify({grokVersion:x.grokVersion, channel:x.channel, projectRoot:x.projectRoot, projectTrusted:x.projectTrusted, localProjectPlugins:x.localProjectPlugins})); });'
printf 'EXIT=%s\n' "$?"
printf '%s\n' '=== S4 grok plugin install --help ==='
grok plugin install --help
printf 'EXIT=%s\n' "$?"
printf '%s\n' '=== S5 grok plugin marketplace update --help ==='
grok plugin marketplace update --help
printf 'EXIT=%s\n' "$?"
printf '%s\n' '=== S6 invalid path validate (expected nonzero) ==='
grok plugin validate ./does-not-exist
printf 'EXIT=%s\n' "$?"
printf '%s\n' '=== S7 JSON/path/README assertions ==='
node - <<'NODE'
const fs = require('node:fs');
const files = [
  '.grok-plugin/plugin.json',
  '.codex-plugin/plugin.json',
  '.claude-plugin/plugin.json',
  '.claude-plugin/marketplace.json',
  '.agents/plugins/marketplace.json'
];
for (const file of files) JSON.parse(fs.readFileSync(file, 'utf8'));
const manifest = JSON.parse(fs.readFileSync('.grok-plugin/plugin.json', 'utf8'));
const skills = fs.readdirSync('skills').filter(name => fs.existsSync(`skills/${name}/SKILL.md`));
const agents = fs.readdirSync('agents').filter(name => name.endsWith('.md'));
const readme = fs.readFileSync('README.md', 'utf8');
const pinnedSha = 'fac10ac385f41c217f94d9565e0cec416288d37e';
if (manifest.name !== 'samsara' || manifest.version !== '0.1.0' || manifest.logo !== 'assets/logo.png') throw new Error('manifest fields');
if (!fs.existsSync(manifest.logo)) throw new Error('missing logo');
if (skills.length !== 8 || agents.length !== 4) throw new Error('component count');
if (!readme.includes(`Mineru98/samsara@${pinnedSha}`) || !readme.includes('grok plugin validate .') || !readme.includes('grok plugin update samsara')) throw new Error('README assertions');
console.log(JSON.stringify({ jsonFiles: files.length, manifest: manifest.name, version: manifest.version, skillCount: skills.length, agentCount: agents.length, logo: manifest.logo, pinnedSha }));
NODE
printf 'EXIT=%s\n' "$?"
printf '%s\n' '=== S8 immutable baseline/path checks ==='
node - <<'NODE'
const { execFileSync } = require('node:child_process');
const sha = 'fac10ac385f41c217f94d9565e0cec416288d37e';
const tree = execFileSync('git', ['ls-tree', '-r', '--name-only', sha, '.grok-plugin/plugin.json'], { encoding: 'utf8' }).trim();
const manifest = execFileSync('git', ['show', `${sha}:.grok-plugin/plugin.json`], { encoding: 'utf8' });
if (tree !== '.grok-plugin/plugin.json' || !JSON.parse(manifest).name) throw new Error('baseline missing manifest');
console.log(JSON.stringify({ pinnedSha, manifestPathAtBaseline: tree, baselineManifestName: JSON.parse(manifest).name }));
NODE
printf 'EXIT=%s\n' "$?"
printf '%s\n' '=== S9 git status/diff check ==='
git status --short
git diff --check main...HEAD
printf 'EXIT=%s\n' "$?"
printf '%s\n' '=== DONE ==='
