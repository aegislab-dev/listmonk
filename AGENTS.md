# 로컬 검사 규칙 (에이전트·리뷰어)

여러 태스크가 한 PC 에서 같이 돌기 때문에 검사를 태스크마다 처음부터 하면 CPU 가 몰린다(LST-51: 2026-11-20 load average 71/32코어).
로컬에서는 **바뀐 부분에 닿는 시험만** 돌리고, 전체 시험은 PR 의 CI(`axm-image`)가 한다. 도구는 `scripts/axm/axm` 하나다.

## 평소 쓰는 명령

```sh
scripts/axm/axm affected          # 바뀐 파일 → 돌릴 spec·Go 시험·--full 필요 여부를 보기만 한다
scripts/axm/axm check             # 위에서 고른 것만 빌드·Go 시험·Cypress 로 돌리고 단계별 시간·CPU초 표를 낸다
scripts/axm/axm check --since-last   # 리뷰 재라운드·수정 뒤: 지난 통과 기록과 내용이 다른 파일에 닿는 시험만 + 지난번 실패한 시험
scripts/axm/axm down              # 내 컨테이너 정리(check 는 끝날 때 스스로 내린다)
```

리뷰어는 모든 명령에 `--role reviewer` 를 붙인다(컨테이너 이름·포트·통과 기록이 에이전트와 따로다. 에이전트의 통과 기록은 리뷰어의 검증이 아니다).
태스크 번호는 브랜치 `work/<N>-…` 에서 얻는다. 아니면 `AXM_TASK=<N>`.

## 규칙

1. **로컬 검사는 `axm check` 로 한다.** 직접 `yarn install`, `docker build -f Dockerfile.axm`, Cypress 전체 실행(`--spec` 없이 또는 CI spec 전부)을 하지 않는다. 여러 기능 파일을 고쳐 고른 spec 이 CI spec 전부가 되면 `axm check` 는 smoke 묶음과 바뀐 spec·지난번 실패한 spec 만 돌리고 나머지는 "CI 가 판정"으로 넘긴다(`affected` 출력의 `deferred_specs`). 예외는 `axm affected` 가 `--full` 을 요구할 때(`Dockerfile.axm`·`Makefile`·lockfile·`docker-entrypoint.sh` 변경: `axm build --full` 1회)와 릴리스 연습이다.
2. **전체 e2e 와 운영 방식 이미지 빌드·smoke 는 CI 가 한다.** 마무리 보고와 PR 설명에 "로컬에서 돌린 spec·Go 시험"과 "CI 에 맡긴 것"을 나눠 적는다. CI(`.github/workflows/axm-image.yml`)의 시험 범위는 줄이지 않는다.
3. **리뷰 재라운드와 수정 뒤 재검사는 `axm check --since-last`.** 비교는 파일 내용(blob 해시)이라 rebase·squash 뒤에도 맞는다.
4. **캐시를 따로 만들지 않는다.** Go 모듈·빌드 캐시, 프론트 dist, 시험용 베이스 이미지는 `~/.cache/listmonk-axm`(`AXM_CACHE` 로 바꿈)에서 태스크끼리 같이 쓴다. `$TMPDIR` 에 Go 캐시나 `node_modules` 를 새로 만들지 않는다. 검사가 끝나면 컨테이너를 내린다(`axm down`).
5. **새 화면·API·spec 을 만들면 `scripts/axm/test-map.json` 에 줄을 추가한다.** `python3 -m unittest scripts/axm/test_axm.py` 가 CI 의 spec 목록과 어긋나거나 규칙이 존재하지 않는 파일을 가리키면 실패한다.
6. **CPU 한도.** 무거운 단계(이미지 빌드의 RUN 단계 포함 `--cpu-quota`, Go 시험, Cypress)는 컨테이너당 `AXM_CPUS`(기본 4)코어, 동시에 `AXM_SLOTS`(기본 2)개만 돈다. 칸이 다 차 있으면 기다리라는 메시지가 나온다. 화면 캡처용 헤드리스 브라우저를 따로 띄울 때도 같은 한도(`docker run --cpus 4`, 호스트라면 `taskset`/`nice`)를 지킨다.
7. Cypress 는 로컬에서 Electron, CI 에서는 Chrome 이다. 둘 다 CPU 렌더링(SwiftShader)이다. 통과 판정은 CPU 렌더링 기준이다. GPU 렌더링은 도입하지 않았다: 렌더링 몫이 검사 CPU 의 약 2%(측정은 LST-51 보고서)라 얻을 것이 적고, CI 러너에는 GPU 가 없다. Before/After 화면 캡처 한 쌍은 같은 방식(같은 브라우저·같은 인자)으로 찍는다.

## 선택 규칙 요약

- 바뀐 spec 파일은 그 spec. 기능 파일(예: `Audit.vue`, `cmd/axm_audit.go`)은 그 기능의 spec.
- 공용 파일(`main.js`, `router`, `store`, `api`, `cmd/handlers.go`, `models`, `queries`, `schema.sql`, `i18n` …)은 smoke 묶음(짧은 spec 4개) + 그 파일이 닿는 spec.
- Cypress 픽스처(`frontend/cypress/fixtures/*`)는 그 파일 이름을 읽는 CI spec. 검사 도구가 DB 에 넣는 `reaction-lists.sql` 은 규칙에 적힌 spec + smoke.
- 어느 규칙에도, 문서·도구 목록(`none`)에도 안 맞는 파일은 무엇이든 smoke 묶음 + "매핑 없음" 경고다(예: 설치가 읽는 `config.toml.sample` 은 공용 파일).
- 문서만 바뀌면 시험 없음.
- Go: 바뀐 파일의 패키지와 그것을 import 하는 패키지. `cmd`·`internal/core` 에서 `axm_<기능>*.go` 만 바뀌었으면 그 기능과 같은 접두어의 시험(과 그 파일의 식별자를 쓰는 시험)만 `-run` 으로 좁힌다.
