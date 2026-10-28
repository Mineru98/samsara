import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath, pathToFileURL } from 'node:url';

/*
 * 이슈 #77. 일반 소스·문서 링크(.go/.md/.json/.txt)는 이미지 검증 대상이 아니다.
 * 이미지 확장자 링크, user-attachments, 인라인 이미지 문법은 기존 검증을 유지한다.
 * issue-media.mjs 사본 다섯 벌이 같은 동작을 하는지 사본마다 확인한다.
 */

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const SKILLS = ['create', 'end', 'merge', 'onboard', 'start'];
const copyPath = (skill) => path.join(repo, 'skills', `issue-${skill}`, 'scripts', 'issue-media.mjs');
const copies = await Promise.all(SKILLS.map(async (skill) => ({
  skill,
  media: await import(pathToFileURL(copyPath(skill)).href),
})));

const SOURCE_LINK = 'https://github.com/Mineru98/samsara/blob/fix/74-macos-symlink-cwd-scope/runtime/internal/app/alias_test.go';
const BLOB = 'https://github.com/o/r/blob/main';
const RAW = 'https://raw.githubusercontent.com/o/r/main';
const ATTACHMENT = 'https://github.com/user-attachments/assets/0f3c2a1e-1111-2222-3333-444455556666';

test('issue-media.mjs 사본 다섯 벌은 바이트가 같다', () => {
  const [reference, ...rest] = SKILLS.map((skill) => readFileSync(copyPath(skill)));
  rest.forEach((data, i) => {
    assert.ok(reference.equals(data), `issue-${SKILLS[i + 1]} 사본이 issue-${SKILLS[0]} 사본과 다르다`);
  });
});

const allowed = {
  '#74 실제 소스 blob 링크': `- [alias_test.go](${SOURCE_LINK})`,
  '문서·데이터 blob 링크': `[doc](${BLOB}/docs/a.md) [cfg](${BLOB}/a.json) [log](${BLOB}/out.txt)`,
  'raw 소스 링크': `[src](${RAW}/runtime/main.go)`,
  'bare 소스 blob URL': `출처: ${SOURCE_LINK}`,
  'release 비이미지 자산 링크': '[bin](https://github.com/o/r/releases/download/v1/samsara.tar.gz)',
  'alt 있는 user-attachments 인라인 이미지': `![after 화면](${ATTACHMENT})`,
  '이미지 확장자 직접 인라인 이미지': '![after](https://example.com/shot.webp)',
};

for (const [name, markdown] of Object.entries(allowed)) {
  test(`허용: ${name}`, () => {
    for (const { skill, media } of copies) {
      const r = media.validateEvidenceReport(markdown);
      assert.equal(r.ok, true, `issue-${skill}: ${r.errors.join(' | ')}`);
    }
  });
}

test('일반 소스 링크는 이미지 참조로 수집되지 않는다', () => {
  for (const { skill, media } of copies) {
    const refs = media.collectImageReferences(`[a](${SOURCE_LINK}) ${BLOB}/README.md`);
    assert.deepEqual(refs, [], `issue-${skill}`);
  }
});

const rejected = {
  '이미지 blob 일반 링크': [`[shot](${BLOB}/shot.png)`, /이미지가 일반 링크로 작성되었습니다[\s\S]*GitHub 페이지 URL/],
  '이미지 raw 일반 링크': [`[shot](${RAW}/shot.jpeg)`, /이미지가 일반 링크로 작성되었습니다/],
  '이미지 raw bare URL': [`${RAW}/shot.gif`, /bare URL로 작성되었습니다/],
  '이미지 blob 인라인': [`![shot](${BLOB}/shot.svg)`, /GitHub 페이지 URL은 직접 이미지 링크가 아닙니다/],
  '비이미지 blob 을 인라인 이미지로 씀': [`![src](${SOURCE_LINK})`, /GitHub 페이지 URL은 직접 이미지 링크가 아닙니다/],
  'user-attachments 일반 링크': [`[after](${ATTACHMENT})`, /이미지가 일반 링크로 작성되었습니다/],
  'user-attachments bare URL': [`after ${ATTACHMENT}`, /bare URL로 작성되었습니다/],
  'alt 없는 인라인 이미지': ['![](https://example.com/shot.png)', /alt\)이 비어 있습니다/],
  'HTML img': ['<img src="https://example.com/shot.png" alt="x">', /HTML <img>/],
};

for (const [name, [markdown, pattern]] of Object.entries(rejected)) {
  test(`거부: ${name}`, () => {
    for (const { skill, media } of copies) {
      const r = media.validateEvidenceReport(markdown);
      assert.equal(r.ok, false, `issue-${skill} 가 통과시켰다`);
      assert.match(r.errors.join('\n'), pattern, `issue-${skill}`);
    }
  });
}

test('private 저장소 보조 링크 규칙은 그대로다', () => {
  for (const { skill, media } of copies) {
    assert.equal(media.validateEvidenceReport(`[shot.webp](${RAW}/shot.webp)`, { isPrivate: true }).ok, true, `issue-${skill}`);
    const inline = media.validateEvidenceReport(`![shot](${RAW}/shot.webp)`, { isPrivate: true });
    assert.equal(inline.ok, false, `issue-${skill}`);
    assert.equal(inline.needsManualUpload, true, `issue-${skill}`);
  }
});
