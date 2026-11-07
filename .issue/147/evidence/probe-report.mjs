// 사용: node probe-report.mjs <worktree>
import path from 'node:path';
import { pathToFileURL } from 'node:url';
const wt = process.argv[2];
const { validateEvidenceReport } = await import(pathToFileURL(path.join(wt, 'skills/issue-start/scripts/issue-media.mjs')).href);
const base = 'https://github.com/Mineru98/samsara';
const cases = {
  'a. 증거 텍스트 파일 링크만': `원본: [guard.txt](${base}/blob/main/.issue/147/evidence/after/guard.txt)`,
  'a2. 증거 텍스트 raw bare URL': `원본: https://raw.githubusercontent.com/Mineru98/samsara/main/.issue/147/evidence/after/guard.txt`,
  'b. blob 이미지 (깨진 이미지 링크)': `![after](${base}/blob/main/.issue/147/evidence/after/a.webp)`,
  'b2. 이미지 파일을 일반 링크로': `[after](https://raw.githubusercontent.com/Mineru98/samsara/main/.issue/147/evidence/after/a.webp)`,
  'c. 정상 raw 이미지': `![after](https://raw.githubusercontent.com/Mineru98/samsara/main/.issue/147/evidence/after/a.webp)`,
};
for (const [name, md] of Object.entries(cases)) {
  const r = validateEvidenceReport(md, { isPrivate: false });
  console.log(`## ${name}\n${md}\nok=${r.ok}${r.errors.length ? '\n- ' + r.errors.join('\n- ') : ''}\n`);
}
