## 작업 요약

Grok Build 공식 플러그인 원본 형식에 맞춰 `.grok-plugin/plugin.json`을 추가했습니다.
기존 `skills/`와 `agents/` 경로는 유지하고, README에 Grok Build의 설치·업데이트·검증 절차와
marketplace 원격 소스의 SHA 고정 주의사항을 문서화했습니다.
소스 구현 검토 기준 커밋은 `c33d03fae7efe04130d4f21e22f494d0d1e8517f`이며, 직전
evidence-only 동기화 부모는 `649c2f319be0266c31876ecfcb8603c9420dc9dc`입니다.

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
- `README.md:287-345` 수동 검토: marketplace 등록 범위, 고정 commit ref 저장소 직접 설치,
  marketplace refresh, trust 선택 기준, Grok 설치·업데이트·검증·전체 소문자 40자 SHA 안내와
  xAI 공식 링크 확인
- `grok plugin install/update` 및 `grok plugin marketplace update/add --help`에서 source/ref와
  refresh 문법 확인 (전역 상태를 바꾸는 실제 install/update는 실행하지 않음)
- 직접 설치 기준 `Mineru98/samsara@fac10ac385f41c217f94d9565e0cec416288d37e`가 GitHub commit API와
  raw manifest에서 HTTP 200으로 확인됨. README는 자기 참조를 피하기 위해 이 이전 manifest 기준
  커밋을 의도적으로 고정하고, 새 버전에서는 새 전체 SHA로 교체하도록 설명함

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
