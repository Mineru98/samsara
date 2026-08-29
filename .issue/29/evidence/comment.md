## 작업 요약

저장소 루트에 ZCode용 `marketplace.json`을 추가하고 `samsara` 플러그인을
`Mineru98/samsara`의 `main` 루트 GitHub source로 등록했습니다. marketplace 항목의
버전·설명은 선행 이슈 #28의 `.zcode-plugin/plugin.json`과 일치시키고 컴포넌트 선언은
중복하지 않았습니다.

## 변경 전후

| 전 | 후 |
| --- | --- |
| 루트 `marketplace.json` 없음 | `samsara@0.3.1` 1개 항목이 `Mineru98/samsara` `main` 루트를 가리키는 ZCode catalog 추가 |

문서·설정 파일만 변경되어 실행 화면 캡처는 생략했습니다. ZCode CLI가 이 환경에 설치되어
있지 않고, #28의 PR이 아직 `main`에 통합되지 않았으므로 Personal marketplace의 실제
표시·Install·Enable·Refresh 동작 대신 marketplace JSON, GitHub source, 선행 매니페스트와
기존 호환성 파일을 정적으로 검증했습니다. #28이 먼저 `main`에 통합되면 이 source가
`.zcode-plugin/plugin.json`을 읽습니다.

## 변경 파일

- `marketplace.json` — SAMSARA ZCode marketplace와 GitHub source 등록

## 검증

- 커스텀 정적 검증 통과: 필수 필드, name 규칙, GitHub repo/path/ref, `0.3.1` metadata 일치,
  컴포넌트 중복 없음, 선행 `.zcode-plugin/plugin.json` 경로
- `node --test tools/issue-ontology/ontology.test.mjs` 통과: 13 passed, 0 failed
- `git diff --check` 통과
- 구현 커밋: `b850681b180cdea77aabc8ec9e89130de61bd525`

## 남은 이슈

- #28 PR이 먼저 통합되어야 `marketplace.json`의 `main` source에서 ZCode 플러그인 매니페스트를
  실제로 설치할 수 있습니다.
