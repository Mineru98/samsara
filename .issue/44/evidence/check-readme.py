#!/usr/bin/env python3
"""이슈 #44 완료 기준 구조 검사.

문자열 grep -c 로 세지 않는다 — 문구를 조금만 다듬어도 기대값이 깨져 증거가 못 된다.
저장소 루트에서 실행한다. 모든 항목이 참이면 exit 0, 하나라도 거짓이면 exit 1.
"""
import io, re, subprocess, sys, unicodedata

README = 'README.md'
SCRIPT = 'skills/issue-version/scripts/issue-version.mjs'
s = io.open(README, encoding='utf-8').read()
ch3 = s[s.index('輪廻 : SAMSARA CYCLE'):s.index('## 제4장')]

def display_width(text, ambiguous):
    total = 0
    for ch in text:
        w = unicodedata.east_asian_width(ch)
        total += 2 if w in 'WF' else (ambiguous if w == 'A' else 1)
    return total

def diagram_offsets():
    """다이어그램 세로축의 어긋남을 기존 축과 신규 축 각각에 대해 잰다."""
    lines = s.split('\n')
    st = next(i for i, l in enumerate(lines) if '輪廻 : SAMSARA CYCLE' in l)
    en = next(i for i, l in enumerate(lines) if '결계 각인 · vX.Y.Z' in l)
    block = lines[st:en + 1]

    def center(pattern, amb):
        for l in block:
            m = re.search(pattern, l)
            if m:
                return display_width(l[:m.start()], amb) + display_width(m.group(), amb) / 2
        return None

    def vertical_center(idx, ch, amb):
        l = block[idx]
        return display_width(l[:l.index(ch)], amb) + display_width(ch, amb) / 2

    arrow_idx = max(i for i, l in enumerate(block) if l.strip() == '▼')
    out = {}
    for amb in (1, 2):
        out[amb] = (
            abs(center(r'\[ 인의 발동 \]', amb) - vertical_center(3, '│', amb)),
            abs(center(r'\[ 명부 \(Closed\) \]', amb) - vertical_center(arrow_idx, '▼', amb)),
        )
    return out

def version_sources_count():
    out = subprocess.run(
        [ 'node', '--input-type=module', '-e',
          f"import {{VERSION_SOURCES}} from './{SCRIPT}'; console.log(VERSION_SOURCES.length);" ],
        capture_output=True, text=True)
    return int(out.stdout.strip()) if out.returncode == 0 and out.stdout.strip() else -1

script_src = io.open(SCRIPT, encoding='utf-8').read()
offsets = diagram_offsets()
sources = version_sources_count()

checks = [
    ("세 인자 예시가 있다",
     sorted(re.findall(r'^/issue-version (patch|minor|major)\b', s, re.M)) == ['major', 'minor', 'patch']),
    ("1단계가 PR 에서 멈춘다는 사실이 있다",
     'bump PR' in s and 'merge 한 뒤' in s and '한 번 더 불러야' in s),
    ("다이어그램에 결계 각인 축이 있다",
     '[ 결계 각인 · vX.Y.Z ]' in s),
    ("제3장 설명이 커밋을 가리킨다 (이슈가 아니다)",
     '쌓인 커밋을 비문에' in ch3 and '잠든 인들만' not in ch3),
    ("기존 순환 화살표가 그대로다",
     '└─── [ 명부 (Closed) ] ◀───┘' in s),
    ("v0.1.0 조건이 코드와 일치한다",
     '태그도 파일에 적힌 버전도 없는 첫 릴리즈' in s),
    ("예시가 저장소 현재 버전에 묶여 있지 않다",
     'v1.4.2' in s and not re.search(r'v0\.\d+\.\d+ → v0\.\d+\.\d+', s)),
    ("코드블록 펜스가 짝수다",
     len(re.findall(r'^```', s, re.M)) % 2 == 0),
    ("README 의 '8개 파일' 이 VERSION_SOURCES 와 같다",
     sources == 8 and '8개 파일' in s),
    ("스크립트가 이슈 API 를 쓰지 않는다 (노트는 커밋 기반)",
     len(re.findall(r'gh issue|issues/', script_src)) == 0 and "'log', '--no-merges'" in script_src),
    (f"신규 축이 기존 축보다 나쁘지 않다 (amb=1: {offsets[1][1]:.1f} ≤ {offsets[1][0]:.1f})",
     offsets[1][1] <= offsets[1][0]),
    (f"같은 판정이 amb=2 에서도 성립한다 ({offsets[2][1]:.1f} ≤ {offsets[2][0]:.1f})",
     offsets[2][1] <= offsets[2][0]),
]

failed = 0
for label, ok in checks:
    print(f"  {'PASS' if ok else 'FAIL'}  {label}")
    failed += 0 if ok else 1
print(f"  → {len(checks) - failed}/{len(checks)} 통과")
sys.exit(1 if failed else 0)
