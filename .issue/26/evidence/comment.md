## 작업 요약

issue-merge merge guard 가 문서(neither) 변경 이슈를 막던 것을 고쳤다. `issue-merge.mjs` 의 `evidenceComplete` 판정을 "before/after webp 둘 다" 에서 "before/after 둘 다 **또는** (스크린샷 전무 + 리포트 존재)" 로 완화했다. 공용 island 스키마와 vendored `issue-common.mjs` 는 건드리지 않았다.

## 변경 성격 — 코드 로직(neither)

CLI 스크립트 검증 로직 수정이라 스크린샷이 없다. 재현 로그로 증거를 남긴다.

## 원인

- `skills/issue-merge/scripts/issue-merge.mjs:508-513` — `evidenceComplete = before webp && after webp`. 문서 변경(캡처 없음)을 배제.
- `tools/issue-ontology/schemas/actions/merge.schema.json:38-40` — `evidenceComplete: {const: true}` 가 이 값을 요구.
- 비결정성: guard 는 island(`tools/issue-ontology`)의 Ajv 가 있을 때만 작동한다. 워크트리엔 `node_modules/ajv` 가 없어 skip(그래서 issue-end 는 통과), 메인엔 있어 작동(그래서 merge 는 실패). 같은 증거인데 환경 따라 갈렸다.
- `end` 액션(`end.schema.json` + `issue-end.mjs`)도 동일 구조지만 이 이슈 범위 밖(회귀 노트).

## 변경 내용

`skills/issue-merge/scripts/issue-merge.mjs` 한 곳:

```diff
+  const hasBefore = evidence.some((file) => file.includes('/before/'));
+  const hasAfter = evidence.some((file) => file.includes('/after/'));
+  const hasReport = evidence.some((file) => file.endsWith('comment.md'));
   const observed = {
     gitRepo: git(['rev-parse', '--show-toplevel'], { cwd: root }).code === 0,
     trackerAuth: createTracker(root).auth().ok,
     issueExists: Boolean(issue),
-    evidenceComplete: evidence.some((file) => file.includes('/before/'))
-      && evidence.some((file) => file.includes('/after/')),
+    // 문서·설정(neither) 변경은 before/after 캡처가 없는 게 정상이다.
+    // 스크린샷이 하나도 없고 리포트(comment.md)만 있으면 문서 증거로 인정한다.
+    // before/after 중 하나라도 있으면 UI/백엔드로 보고 둘 다 요구한다(회귀 방지).
+    evidenceComplete: (hasBefore && hasAfter) || (!hasBefore && !hasAfter && hasReport),
   };
```

## 검증 — gateAction 4케이스 (메인 island, Ajv available)

수정 전에는 문서 케이스에서 `merge 온톨로지 guard 실패: ... evidenceComplete must be equal to constant` 로 막혔다(#24 에서 확보). 수정 후:

| 케이스 | before/after/report | evidenceComplete | guard | 판정 |
| --- | --- | --- | --- | --- |
| 문서(neither) | 0 / 0 / 1 | true | ok | 통과 (신규 허용) |
| UI 정상 | 2 / 2 / 1 | true | ok | 통과 (기존 유지) |
| UI 부분(after 누락) | 2 / 0 / 1 | false | blocked | 막힘 (회귀 방지) |
| 증거 없음 | 0 / 0 / 0 | false | blocked | 막힘 (기존 유지) |

`RESULT: ALL PASS`. 기존 온톨로지 테스트 `node tools/issue-ontology/ontology.test.mjs` → 13/13 통과(회귀 없음).

## 완료 기준 대비

- [x] 문서 변경(neither) 이슈가 webp 없이 merge guard 를 통과한다(evidence.report 기준).
- [x] issue-start/issue-end 의 neither 처리(evidence-recheck 5절)와 정합된다.
- [x] webp 가 필요한 UI 이슈는 여전히 guard 로 보호된다(UI 부분·증거 없음 케이스 막힘 확인).

## 트레이드오프 · 남은 이슈

- UI 이슈가 캡처를 완전히 누락하고 comment 만 남기면 문서로 오인될 수 있다. issue-start 가 UI 이슈에 캡처를 강제하므로 현실적으로 드물다. 더 엄격한 신호(neither 마커/라벨)는 issue-start/issue-end 까지 손대야 해 범위 밖.
- `end` 액션의 동일 결함(`end.schema.json` + `issue-end.mjs`)은 별도 후속 이슈 후보.
