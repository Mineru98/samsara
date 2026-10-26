## 작업 요약

samsara 에이전트 4개의 Claude 모델을 `opus` 로 올리고 `effort: high` 를 지정했습니다.
스킬 문서(SKILL.md)의 Claude 모델 표기도 frontmatter 와 맞췄습니다. 이 표기는 원래부터 frontmatter 와 어긋나 있었습니다.
Codex 정의(`agents/*.toml`, `codex-model`)는 바꾸지 않았습니다.

## 변경 전후

정의 파일과 문서만 바뀌어 화면 캡처나 성능 측정이 의미가 없으므로 생략했습니다. 대신 grep 결과로 변경 근거를 남깁니다.

| 에이전트 | 전 | 후 |
| --- | --- | --- |
| neji-verifier | haiku, effort 없음 | opus, effort high |
| shikamaru-merge-analyst | sonnet, effort 없음 | opus, effort high |
| itachi-merge-critic | opus, effort 없음 | opus, effort high |
| yamato-merge-resolver | opus, effort 없음 | opus, effort high |

SKILL.md 표기 12곳: 전에는 `haiku`/`sonnet` 이 섞여 있었고(issue-merge 는 frontmatter 와도 불일치), 이제 모두 `opus` 입니다.
전후 grep 전문은 `.issue/61/evidence/before.txt`, `after.txt` 에 있습니다.

## 변경 파일

- `agents/{itachi-merge-critic,neji-verifier,shikamaru-merge-analyst,yamato-merge-resolver}.md` — `model: opus`, `effort: high`
- `skills/issue-{create,start,end,merge}/SKILL.md` — `claude-model="opus"`, `(model: opus)`

## 검증

- `grep -rnE 'claude-model="(haiku|sonnet)"|\(model: (haiku|sonnet)\)' skills` → 0건
- `sh scripts/check-shared.sh` → exit 0
- `node --test scripts/check-shared.test.mjs` → 10 pass / 0 fail
- `git diff -- 'agents/*.toml'` → 변경 없음

## 남은 이슈

- 서술 문구는 그대로 두었습니다: "판정성 작업은 값싼 모델에 맡긴다"(issue-start·issue-create SKILL.md), "analyst 와 critic 은 판정만 하므로 작은 모델로 충분하다"(issue-merge SKILL.md), neji-verifier 의 "값싸게 수행한다"·"값싼 모델로 분리했다". Codex 쪽은 여전히 작은 모델이라 유효하지만 Claude 기준으로는 어긋납니다. 별도 이슈로 다룰 수 있습니다.
