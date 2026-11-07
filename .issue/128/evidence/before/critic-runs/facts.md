# 계획 대조 재료 (inventory · preflight 결과)

저장소: Mineru98/toy-calculate (공개, CI 없음, Actions 비활성), base = main (707f8a9)
후보
- #1 계산 엔진 — PR #14, 브랜치 feat/1-calc-engine, head a1b2c3d, 증거 충족, 변경 파일: src/engine.ts, src/engine.test.ts
- #4 글래스 UI — PR #15, 브랜치 feat/4-glass-ui, head d4e5f6a, 증거 충족(화면 4장), 변경 파일: src/glass.css, index.html
파일 overlap: 0
preflight 단독: #14 clean, #15 clean
preflight 누적: #15 → #14 --onto 결과 clean, commit 25fac07 (tree 2e2c494)
충돌 해소: 없음 (resolver 미실행), 보정 push 없음
PR 본문 Closes/Fixes/Resolves: #14 에 "Closes #1" 1건, #15 없음
