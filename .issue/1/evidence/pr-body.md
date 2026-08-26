관련 이슈: [#1 Grok Build 호환 플러그인 지원 및 README 설치 안내 추가](https://github.com/Mineru98/samsara/issues/1)

## 변경 내용

- `.grok-plugin/plugin.json`을 추가해 Grok Build 공식 플러그인 형식과 기존 root `skills/`·`agents/` 경로를 연결했습니다.
- Claude Code/Codex 플러그인 메타데이터와 기존 컴포넌트는 유지했습니다.
- `README.md`에 marketplace 카탈로그 설치 조건, 직접 설치용 immutable GitHub SHA, marketplace refresh/add와 plugin update, 선택적 `--trust`, `validate`/`inspect`, 호환 범위와 xAI 공식 링크를 문서화했습니다.
- Grok marketplace 카탈로그 등록 PR은 이 변경 범위에 포함하지 않았습니다.

## 설치 기준

- Source implementation review: `c33d03fae7efe04130d4f21e22f494d0d1e8517f`
- Final evidence/handoff tip: `fba26c089e85ba6645360fb97344e7b5060957e2`
- Direct source baseline: `Mineru98/samsara@fac10ac385f41c217f94d9565e0cec416288d37e`

README는 자기 참조를 피하기 위해 manifest가 포함된 공개 ancestor를 직접 설치 기준으로 의도적으로 고정합니다.

## 검증

- `grok plugin validate .` 통과
- `grok 1.0.5 (5115b46bc909) [stable]`에서 `grok inspect --json` 진단 통과
- JSON/path/README assertions 통과: manifest, logo, 8 skills, 4 agents, 기존 metadata 보존
- `node --test tools/issue-ontology/ontology.test.mjs`: 10 passed, 0 failed
- `git diff --check` 및 `ontology-guard --issue 1` 통과
- feature branch와 pinned baseline의 GitHub API/raw manifest 도달성 확인
- 목표·QA·코드·보안·맥락 5개 review-work lane이 최종 SHA 기준 PASS

전역 상태를 변경하는 실제 Grok install/update/marketplace-add는 실행하지 않았고, read-only help/listing/validation만 수행했습니다.

## 증거

[Issue #1 전후 검증 리포트](https://github.com/Mineru98/samsara/issues/1#issuecomment-5422735911)
