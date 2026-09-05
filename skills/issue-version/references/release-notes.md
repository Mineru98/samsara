# 릴리즈 노트

직전 태그 이후의 커밋만 읽어 conventional commit type 별로 묶는다.
merge 커밋은 제외한다 — PR 제목이 이미 squash 커밋 제목으로 들어와 있어 두 번 세어진다.

```bash
git log --no-merges v0.3.2..HEAD
```

## type → 섹션

순서가 곧 노트에 나오는 순서다.

| type | 섹션 |
| --- | --- |
| (`!` 또는 본문 `BREAKING CHANGE:`) | 💥 호환성이 깨지는 변경 |
| `feat` | ✨ 기능 |
| `fix` | 🐛 버그 수정 |
| `perf` | ⚡ 성능 |
| `refactor` | ♻️ 리팩터링 |
| `docs` | 📝 문서 |
| `test` | ✅ 테스트 |
| `build` | 📦 빌드 |
| `ci` | 🤖 CI |
| `style` | 💄 스타일 |
| `revert` | ⏪ 되돌림 |
| `chore` | 🧹 유지보수 |
| 형식에 맞지 않는 제목 | 📌 기타 |

호환성이 깨지는 변경은 type 과 무관하게 맨 위로 올린다. 사용자가 가장 먼저 봐야 할 항목이다.

## 한 줄 형식

```text
- **<scope>**: <설명> (#41, #39)
```

`scope` 가 없으면 빼고 설명만 쓴다. 번호는 제목과 본문에서 `#\d+` 를 중복 없이 뽑는다.
`(#39)` 는 squash merge 가 붙인 PR 번호이고 `Closes #38` 은 이슈 번호다. 둘을 구분하지 않고 함께 적어
사용자가 GitHub 에서 눌러 확인하게 둔다.

**제목 끝의 `(#39)` 는 설명에서 떼어낸다.** squash 커밋 제목에 이미 들어 있으므로 그대로 두면
참조 목록에서 한 번 더 찍혀 `카탈로그 추가 (#32) (#32, #29)` 가 된다. 떼어낸 번호는 사라지지 않고
참조 목록으로 합쳐진다. 문장 중간의 `#41` 은 설명의 일부이므로 건드리지 않는다.

## 링크

노트 본문의 `#41` 은 GitHub 릴리즈 페이지에서 자동으로 링크된다. 직접 URL 을 조립하지 않는다.
맨 아래 compare 링크만 `origin` remote 에서 저장소 slug 를 뽑아 만든다.

```text
**전체 변경**: https://github.com/<owner>/<repo>/compare/v0.3.2...v0.3.3
```

최초 릴리즈에는 비교 대상이 없으므로 이 줄을 빼고 커밋 수만 적는다.

## 손보고 싶을 때

자동 생성 결과를 그대로 쓸 필요는 없다. 파일로 뽑아 고친 뒤 넘긴다.

```bash
node <skill>/scripts/issue-version.mjs notes --version v0.3.3 --out /tmp/notes.md
# /tmp/notes.md 를 고친다
node <skill>/scripts/issue-version.mjs release v0.3.3 --notes-file /tmp/notes.md
```

**커밋에 없는 변경을 노트에 적지 않는다.** 릴리즈 노트는 홍보문이 아니라 기록이다.
