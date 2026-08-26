## 작업 요약

Grok Build 공식 플러그인 원본 형식에 맞춰 `.grok-plugin/plugin.json`을 추가했습니다.
기존 `skills/`와 `agents/` 경로는 유지하고, README에 Grok Build의 설치·업데이트·검증 절차와
marketplace 원격 소스의 SHA 고정 주의사항을 문서화했습니다.

## 변경 파일

- `.grok-plugin/plugin.json` — SAMSARA Grok Build manifest
- `README.md` — Grok Build 설치, 업데이트, 검증, 호환 범위와 공식 참고 문서

## 검증

- `grok plugin validate .` 통과 (Grok Build 1.0.5)
- `grok --version` 확인 (Grok 1.0.5 stable)
- `grok inspect --json` 진단 통과: 작업 폴더 trusted, marketplace 미설치 로컬 플러그인 0개
- 5개 plugin/marketplace JSON 파싱 및 manifest 필수 필드·logo·컴포넌트 경로 확인 통과
- 8개 `skills/*/SKILL.md`, 4개 `agents/*.md` 존재 확인
- `node --test tools/issue-ontology/ontology.test.mjs` 통과 (10/10)
- `git diff origin/main...HEAD --check` 통과
- `README.md:287-334` 수동 검토: marketplace 등록 범위, 저장소 직접 설치, trust 선택 기준,
  Grok 설치·업데이트·검증·전체 소문자 40자 SHA 안내와 xAI 공식 링크 확인

## 증거

이번 변경은 브라우저 화면이나 실행 중인 서비스가 없는 문서·설정 작업이므로 webp 캡처를 생략했습니다.
before/after 텍스트 검증 기록은 다음에 보존되어 있습니다.

- `.issue/1/evidence/before/validation.md`
- `.issue/1/evidence/after/validation.md`

## 남은 이슈

- 외부 `xai-org/plugin-marketplace` 저장소에 marketplace 등록 PR을 제출하는 작업은 별도 범위입니다.
- ontology 테스트 의존성 설치 시 기존 dependency tree에서 moderate audit warning 1건이 보고되었지만,
  이번 변경에서는 의존성 파일을 수정하지 않았습니다.
- validator의 `agent dir(s)` 요약은 기존 Claude 호환 메타데이터 사용 전후에 달라졌지만, 정확한
  파일 수는 path 검사에서 4개로 동일하며 agent 파일은 변경하지 않았습니다.
