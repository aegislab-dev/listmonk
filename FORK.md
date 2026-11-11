# AXM listmonk 포크 운영 규칙

이 저장소는 [knadh/listmonk](https://github.com/knadh/listmonk)의 공개 수정판이다.
기반: **v6.2.0** (`ef0a75872463f10a4848af6c547d1c057405453a`).
2026-11-11 수정: 프론트 포함 이미지 빌드, AGPL 소스 링크, 계정 발신자·인증된 Reply-To, CSV 열 맞춤, 캠페인 에디터 개인화, 캠페인 사업자정보 자동 푸터, 비주얼 캠페인 자동 추적, 공개 콘텐츠 AI 크롤러 차단, 포크 운영 문서.
원저작자의 저작권 고지와 [AGPL-3.0 라이선스](LICENSE)를 유지한다.
현재 동적 리스트 기능은 아직 구현하지 않았다. CSV 열 맞춤은 아래 범위로 구현했다.

## 브랜치와 upstream 추종

- `master`: upstream 거울. 직접 커밋하거나 배포하지 않는다.
- `axm/v6.2`: v6.2.0 기반 유지 브랜치이자 GitHub 기본 브랜치. 기능 브랜치를 여기서 만들고 PR로 돌려보낸다. 직접 푸시 대신 PR을 사용한다.
- 공유 유지 브랜치는 force-push하지 않는다. 게시한 태그는 수정하거나 삭제하지 않는다. 유지 브랜치에 PR 검사와 브랜치 보호 설정을 권고한다.
- **knadh/listmonk에 푸시·PR·이슈·댓글을 보내지 않는다.** upstream 기여는 별도 결정한다. `gh`가 부모 저장소를 선택할 수 있으므로 PR 생성은 항상 대상을 명시한다.

```sh
git remote -v
git remote get-url --push upstream # 반드시 no_push
# 새 clone에서는 upstream을 등록한 뒤 푸시를 막는다.
git remote add upstream https://github.com/knadh/listmonk.git
git remote set-url --push upstream no_push
gh repo set-default aegislab-dev/listmonk
gh pr create --repo aegislab-dev/listmonk --base axm/v6.2 --head <feature-branch>
```

`upstream`이 이미 있으면 `remote add`는 생략한다. `master`의 동기화는 다음처럼 fast-forward만 허용한다. 강제 푸시하지 않는다.

```sh
git fetch upstream --tags
git push origin upstream/master:master
```

`axm/v6.2`에서는 GitHub의 **Sync fork** 버튼을 누르지 않는다. upstream master의 v7 개발 코드를 섞게 된다.
upstream이 `v6.2.x` 패치를 내면 그 태그를 유지 브랜치에 **merge**하고(rebase하지 않음), 이미지 태그를 `v6.2.x-axm.1`부터 다시 센다.
마이너·메이저 변경은 해당 공식 태그에서 `axm/v6.3`·`axm/v7` 같은 새 브랜치를 만들고 포크 변경을 옮겨 검증한 뒤 기본 브랜치를 바꾼다.
v7은 UI 전면 교체이므로 프론트 포팅이 필요하다.

- upstream 보안 릴리스 확인 담당: **[사람 지정 필요]**
- 확인 주기: **[사람 지정 필요]**

## 버전, 이미지와 릴리스

Git 태그 = 이미지 태그 = `v<upstream 버전>-axm.<N>`.
N은 1부터 시작하고 같은 upstream 버전 안에서 하나씩 증가한다. 예: `v6.2.0-axm.1`.
수정된 이미지는 N+1로 새로 발행한다. 이미 존재하는 릴리스 태그의 재게시는 CI가 거부한다.
`latest`는 만들지 않는다. 보조 태그는 `sha-<전체 커밋 SHA 앞 12자리>`다.
운영 기록에는 버전 태그와 digest를 함께 적고 이미지는 digest로 고정한다.

**첫 릴리스 태그와 GHCR 게시는 초기 설정 PR 머지 뒤에 수행한다.**
Squash 머지 전 태스크 커밋에 릴리스 태그를 달지 않는다.

```sh
git fetch origin --tags
# 최신 유지 브랜치의 검증된 머지 커밋에서, 사용하지 않은 번호로 실행한다.
git tag -a v6.2.0-axm.1 origin/axm/v6.2 -m 'AXM listmonk v6.2.0-axm.1'
git push origin v6.2.0-axm.1
```

`axm-image` 성공 뒤 job summary의 `ghcr.io/aegislab-dev/listmonk@sha256:…`와 태그 커밋을 기록한다.
처음 게시한 뒤 사람이 GitHub 패키지 설정에서 GHCR 패키지를 **public**으로 전환하고, 인증 없는 환경에서 pull이 되는지 확인한다.
공개 저장소라고 패키지가 자동 공개된다고 가정하지 않는다.
포크에서 Actions가 비활성화되어 있으면 사람이 Actions 탭에서 활성화해야 한다.
상속된 release/nightly/Pages/stale/Hodor/build-sanity 워크플로는 포크 브랜치에서 제거한다. GitHub Actions 목록에 옛 워크플로가 남으면 disable한다.
`.goreleaser*.yml`은 upstream 추종을 위해 보존하지만 이 포크에서 실행하지 않는다.

워크플로 동작:

- `axm/**` 대상 PR: linux/amd64 이미지 빌드 + 폐기용 Postgres 설치·기동 검사, 게시 없음.
- 수동 실행: 같은 검사만 수행, 게시 없음.
- `v*-axm.*` 태그: 엄격한 버전 형식·유지 브랜치 포함 여부·미게시 태그 확인 → 같은 검사 → GHCR 게시.
- 게시에는 `GITHUB_TOKEN`만 사용한다. DockerHub로 보내지 않는다.
- OCI 라벨에는 source, url(소스 태그), revision(전체 SHA), version, licenses, created, title, 원본의 수정판이라는 description을 담는다.

## 로컬 빌드와 검증

빌더는 **Node 24.22.2 / Go 1.26.1 / Yarn 1.22.22**, 런타임은 **Alpine 3.23.4**로 고정한다.
두 프론트에서 `yarn install --frozen-lockfile`을 사용한다.
Cypress 실행 파일은 이미지 빌드에 필요 없어 자동 다운로드를 생략한다.

```sh
docker build --platform linux/amd64 -f Dockerfile.axm \
  --build-arg VERSION=v6.2.0-axm.0-local \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  -t listmonk-axm:local .
docker run --rm --entrypoint ./listmonk listmonk-axm:local --version
```

`VERSION`과 `COMMIT`은 필수다. PR·수동 빌드는 `v6.2.0-axm.0-pr…`로 구별하며 릴리스용 정규식에 맞지 않아 게시할 수 없다.
릴리스 작업에서는 Git 태그를 VERSION으로 넘기고 COMMIT은 `git rev-parse --short HEAD` 결과를 쓴다.
이미지 보조 태그의 12자리 SHA와 바이너리의 short SHA는 별개다.

React 이메일 빌더 → Vue 관리 프론트 → Go 바이너리 → **stuffbin 패킹**을 `make dist`로 실행한다.
프론트만 고쳐도 바이너리와 이미지를 다시 빌드해야 한다. `go build`만 실행하거나 `.vue` 파일을 런타임에 복사하면 배포되지 않는다.
upstream `Makefile`·`Dockerfile`은 보존한다. Go 변경이 허용되는 후속 태스크도 `Dockerfile.axm`으로 전체 빌드한다.
런타임의 `/listmonk`, `config.toml`, entrypoint, 포트 9000, 기본 명령 `./listmonk`은 공식 이미지와 같다.

CI는 빈 Postgres 17에 `--install --idempotent --yes` 후 `/health`, `/admin/login`, 임베드된 관리 JS 번들, 버전 출력과 라이선스 파일을 확인한다.
프론트 lint는 `yarn build`의 prebuild에서 실행한다. LST-6부터 이미지 빌드에 `go test ./...`도 포함하며 크롤러 차단·메일 경로 예외·robots.txt와 사업자정보 푸터를 검사한다. 새 설치의 사업자정보 설정 기본값과 공식 v6.2.0 설치 DB에서 포크 이미지로 `--upgrade --yes`(반복 실행 포함) 후 설정 행·마이그레이션 번호·기동을 검사한다.

## 소스 제공과 배포

- 관리 화면 하단에는 `listmonk {version} · 소스 코드 · AGPL-3.0`를 표시한다. 포크 버전의 링크는 해당 버전 Git 태그다. 일반 개발 버전은 저장소 첫 화면으로 연결한다.
  버전은 Settings와 동일한 기존 `/api/config`의 `serverConfig.version`을 사용한다. 기존 `import.meta.env.VUE_APP_VERSION`은 v6.2.0 Vite 설정에서 노출되지 않으므로 사용하지 않는다.
- 공개·로그인 화면의 기존 Powered by listmonk 뒤에도 소스 링크를 표시한다. 공개 템플릿에는 버전 값이 없어 저장소 첫 화면으로 연결한다. **배포 버전별 소스는 `v6.2.0-axm.N` 태그**에서 제공한다.
- 소스 링크·기존 저작권·라이선스 고지를 지우지 않는다. 이미지에는 LICENSE·README·이 문서를 포함한다. 배포한 버전의 태그·소스·빌드 스크립트·lockfile을 보존한다.
- 비밀값, 실제 명단·이메일·속성값을 저장소에 넣지 않는다. 공개 키 카탈로그에도 공개하면 안 되는 내부 용어를 넣지 않는다.
- `--static-dir`로 공개 템플릿을 덮어쓰면 해당 템플릿에도 소스 링크가 필요하다.
- Railway 이미지 교체는 별도 승인 범위다. 이전 deployment와 이미지 digest·환경·시작 명령을 기록하고, DB·미디어 백업과 폐기용 환경 검증 후 진행한다. 기존 Custom JS/CSS의 중복 기능도 확인한다.
- 이미지 롤백은 가져온 구독자 데이터·해지·차단 상태를 되돌리지 않는다. DB에 저장된 Custom JS/CSS도 이미지 교체만으로 복원되지 않는다.

## 공개 콘텐츠 크롤러 정책 (LST-6)

`/robots.txt`는 기존 `static/public/static/robots.txt`를 읽는 Go 핸들러에서 제공한다. 일반 검색엔진에는 `/subscription/form` 허용과 기존 `/subscription/`·`/campaign/`·`/link/` 금지를 유지하고 `/archive`·`/archive.xml` 금지를 추가했다. AI 전체 금지 그룹은 `cmd/crawlers.go`의 단일 상수 목록에서 덧붙인다. 목록의 출처·확인일·레거시 이름은 그 상수 주석에 있다. 정적 파일만 복사해서는 이 그룹이 추가되지 않으므로 전체 이미지를 다시 빌드한다.

같은 목록을 대소문자 구분 없는 UA 부분 문자열로 비교해 홈·구독 폼·공개 캠페인 보기·아카이브 HTML/RSS·공개 아카이브 API에서 **403**을 반환한다. 아카이브 HTML/RSS/API와 캠페인 보기는 `X-Robots-Tag: noindex`를 제공한다. 응답 캐시가 UA별 허용/차단을 섞지 않도록 보호 경로에 `Vary: User-Agent`를 붙인다. 차단 로그에는 프로세스 시작 이후 누적 건수만 기록하며 UA·IP·URL·쿼리·구독자 식별자를 남기지 않는다.

**수신거부 GET/POST(원클릭 List-Unsubscribe POST 포함), 구독 확인 GET/POST, 개인정보 내보내기·삭제, `/link/` 추적 리다이렉트, `/campaign/:campUUID/:subUUID/px.png` 열람 픽셀은 UA 차단을 적용하지 않는다.** Gmail·Outlook·보안 스캐너가 같은 UA로 접근해도 기존 처리를 거친다. 관리 화면·로그인·인증 API와 구독 API도 기존 인증·동작을 유지한다.

robots.txt와 noindex는 권고일 뿐 강제 차단이 아니다. UA 미들웨어는 알려진 이름을 밝히는 요청만 막으며, UA를 위장하거나 생략한 크롤러와 이미 수집된 데이터는 막거나 회수할 수 없다. Google-Extended·Applebot-Extended는 별도 HTTP UA가 아닌 사용 목적 제어 토큰이므로 Googlebot·Applebot의 AI 활용을 UA로 구분할 수도 없다. 완전한 차단을 목표로 하려면 CDN/WAF(Cloudflare 등)의 봇 검증·접근 제어가 필요하며, 그때에도 메일 필수 경로 예외를 유지해야 한다. 이 변경은 DB 마이그레이션이 없고 운영 배포는 LST-5 이후 별도 범위다.

## Go·DB 변경 규칙

기본 범위는 프론트 변경이다. Go·SQL 변경은 태스크가 명시적으로 허용했을 때만(LST-4, LST-6 크롤러 차단, LST-7 사업자정보 푸터 등) 최소 범위로 수행하고, upstream에 낼 수 있는 구조를 유지한다(실제 기여는 별도 결정).

포크 전용 마이그레이션 번호는 **`v<기반 upstream 버전의 패치+1>-axm.<N>`**이다.
v6.2.0 기반이면 `v6.2.1-axm.1`, `v6.2.1-axm.2` …을 사용한다.
이미지 태그 `v6.2.0-axm.N`과 다른 번호다.
`golang.org/x/mod/semver` 순서는 다음과 같다.

```text
v6.2.0 < v6.2.1-axm.1 < v6.2.1-axm.2 < v6.2.1 < v6.3.0 < v7.0.0
```

공식 안정 버전 마이그레이션보다 먼저 정렬되고 번호도 겹치지 않는다. upstream의 미래 prerelease 마이그레이션을 도입할 때에는 그 목록과의 실제 정렬도 확인한다.
`+axm` build metadata는 비교에서 무시되어 실행되지 않으므로 쓰지 않는다.

- 파일: `internal/migrations/v6.2.1-axm.N.go`, 함수: `V6_2_1_axm_N`. `cmd/upgrade.go`의 `migList` 맨 끝에 추가하며 semver 오름차순을 유지한다.
- 이미 게시된 이미지의 마이그레이션 내용·번호를 바꾸지 않는다.
- 모두 멱등(`IF NOT EXISTS` 등)으로 쓴다. 새 기반으로 포팅할 때에는 `v7.0.1-axm.N`처럼 다시 번호를 매겨도 기존 운영 DB에서 재실행이 안전해야 한다.
- **추가만 허용한다**: 새 nullable 컬럼, 테이블, 인덱스. upstream 컬럼의 이름·타입·제약을 바꾸지 않는다.
- 같은 변경을 `schema.sql`에도 반영한다. 새 설치는 schema.sql만 실행하고 마지막 마이그레이션 번호를 기록한다.
- `queries/*.sql`의 기존 쿼리 이름과 인자 순서를 유지하고, 필터가 없는 리스트의 동작을 바이트 단위로 유지한다.
- `settings.migrations`의 마지막 번호보다 큰 것만 실행한다. pending migration이 있으면 부팅을 거부한다.
- 공식 v6.2.0으로 복귀하면 포크 컬럼은 남지만, 더 높은 기록 번호를 보고 대기 마이그레이션이 없다고 판단한다. **공식 이미지가 동적 리스트를 일반 리스트로 취급할 위험**은 LST-4에서 별도로 다뤄야 한다.
- 마이그레이션이 있는 릴리스는 릴리스 설명에 명시한다. 교체 전 DB 백업 → `./listmonk --upgrade --yes` → 기동. 프론트만 바뀐 릴리스에는 마이그레이션이 없다.
- 첫 포크 마이그레이션을 추가한 LST-7에서 CI에 **새 설치와 공식 v6.2.0 스키마에서의 upgrade** 시험을 함께 추가했다. 후속 마이그레이션도 두 경로를 유지한다.

## 캠페인 사업자정보 자동 푸터 (LST-7)

설정 → 사업자정보에 전역 한 세트를 입력하고 켜면, 서버가 이메일 캠페인의 렌더링된 본문에 사업자정보를 자동으로 붙인다. 설정 key는 `axm.business_footer`이며 기본값은 꺼짐·모든 항목 빈 문자열이다. 켜려면 상호·주소·전화·이메일을 모두 입력해야 한다. 대표자·사업자등록번호·추가 문구는 선택이다. 실제 값은 설정에만 입력하고 저장소에 넣지 않는다.

- 실제 캠페인 발송·테스트 발송·관리 미리보기·브라우저에서 보기·공개 아카이브의 메일 본문에 동일하게 적용한다. `email`과 `email-…` 메신저만 대상이다.
- HTML(richtext/html/markdown/visual)은 마지막 `</body>` 앞, 닫는 body 태그가 없으면 끝에 붙인다. plain 및 기존 대체 텍스트는 끝에 붙인다. 없는 대체 텍스트를 만들지 않는다.
- 입력값은 HTML 이스케이프하며 추가 문구의 줄바꿈만 보존한다. 라벨은 인스턴스 언어를 따르고 값은 입력 그대로 표시한다.
- 사업자등록번호가 입력돼 있고 본문에 그 문자열이 이미 있으면 그 본문에는 삽입하지 않는다(HTML/텍스트 각각 판단). 등록번호가 없거나 표현이 다르면 중복을 감지하지 못하므로 기존 템플릿의 사업자정보는 직접 정리한다.
- 수신거부 링크·문구·헤더는 변경하지 않는다. 본문의 수신거부 링크는 기존 템플릿의 `{{ UnsubscribeURL }}`에 달려 있으며 이 기능이 추가하지 않는다. 기본 템플릿에서는 기존 수신거부/브라우저 보기 안내 뒤에 배치된다.
- 트랜잭션·구독 확인·알림 메일, 이메일 외 메신저, 구독/해지/아카이브 목록 등 공개 페이지 자체의 footer에는 적용하지 않는다. 법적 표기 충족 여부는 사람이 확인하며 제목 `(광고)` 표기는 범위 밖이다.
- 설정은 기존 재시작 절차가 끝난 뒤 반영된다. 진행 중인 캠페인이 있으면 재시작이 지연되고 이전 설정이 계속 사용된다.

첫 포크 마이그레이션 **`v6.2.1-axm.1`**은 `settings`에 행 하나를 `ON CONFLICT DO NOTHING`으로 추가한다. 테이블·컬럼 변경은 없다. `schema.sql`에도 같은 기본값을 반영했다. 공식 v6.2.0 이미지는 이 추가 key를 무시한다.

이 변경을 포함한 릴리스 설명에는 마이그레이션 번호와 다음 절차를 적는다: **DB 백업 → 새 이미지에서 `./listmonk --upgrade --yes` → 새 이미지 기동**. 새 설치는 별도 upgrade가 필요 없다. 후속 태스크는 머지 전 유지 브랜치의 마이그레이션 번호 충돌 여부를 확인한다.

## 비주얼 캠페인 자동 추적 (LST-9)

upstream v6.2.0은 비주얼 캠페인에 기본 템플릿을 씌우지 않는다. 기본 비주얼 원본에는 `{{ TrackView }}`가 있지만, 원본을 교체하거나 직접 만든 본문에 태그가 없으면 오픈이 기록되지 않는다. 클릭도 `@TrackLink` 또는 `TrackLink`를 붙인 URL만 추적한다.

이 포크는 `internal/manager/tracking.go`에서 **수신자별 렌더링 이후** 비주얼 이메일에 추적을 기본 적용한다. DB의 `body`·`body_source`는 고치지 않으므로 기존 초안·복제본도 새 이미지에서 발송할 때 적용된다. DB 마이그레이션과 새 설정은 없다.

- 대상은 `content_type=visual`, 메신저 `email` 또는 `email-…`이다. 다른 형식과 대체 텍스트는 기존 동작을 유지한다.
- 오픈 픽셀은 마지막 `</body>` 앞, 없으면 본문 끝에 넣는다. 같은 캠페인·수신자용 픽셀이 이미 있으면 추가하지 않는다.
- `a`·`area`·`v:roundrect`의 절대 `http(s)` 링크를 기존 링크 등록·캐시·리다이렉트로 감싼다. 개인화된 URL은 렌더링된 최종 목적지를 등록한다. 이미지 `src`, 상대 URL, 앵커, `mailto:`·`tel:` 등은 변경하지 않는다.
- 현재 인스턴스의 `/link/`·`/subscription/`·`/campaign/` 경로는 제외한다(설치 경로 접두사 포함). 기존 추적 링크의 중복 래핑과 수신거부·관리·구독 확인·브라우저 보기 링크의 추적을 방지한다. 사업자정보 푸터를 붙이기 전에 처리하므로 자동 푸터의 링크는 직접 연결을 유지한다.
- HTML 전체를 재직렬화하지 않고 해당 속성 값만 바꾼다. 조건부 주석이 렌더링 결과에 남아 있으면 그 안의 Outlook 버튼 링크도 처리한다. 기존 Go 템플릿 렌더러의 주석 처리 동작은 변경하지 않는다.
- `privacy.disable_tracking=true`면 자동 처리 전체를 건너뛰고 기존 수동 추적 함수도 픽셀 없음·원래 URL 반환을 유지한다. `individual_tracking=false`면 기존 함수와 동일하게 수신자 UUID를 더미 UUID로 대체한다. 기존 공개 핸들러가 수신자 없이 캠페인 합계를 기록한다.
- 관리 미리보기의 더미 캠페인 UUID에서는 자동 추적을 건너뛰어 링크 등록·실제 통계 유입을 막는다. 실제 캠페인 UUID를 쓰는 테스트 발송과 브라우저 보기는 기존 수동 태그처럼 통계에 포함될 수 있다. 미리보기만으로 추적 성공을 판단하지 않는다.
- 링크 등록이 실패하면 기존 `trackLink`처럼 원래 목적지를 유지한다. 이미 발송된 메일의 본문이나 과거 통계를 복구하지 않는다. 이미지 로딩 차단·프록시 캐시·보안 스캐너로 오픈/클릭 수치는 실제 사람 행동과 다를 수 있다.

## 확정된 제품 기본값 (NEWS-30)

우선순위: **LST-2(CSV 열 맞춤)·LST-3(개인화) → LST-4(동적 리스트)**.

- 개인화는 네 형식 본문과 비주얼 Text/Heading부터 지원한다.
- 키는 영문 snake_case + 한국어 라벨, 일반 값은 문자열, 태그만 배열이다. 기존 한글·중첩 속성을 자동 마이그레이션하지 않는다.
- CSV 입력 상한은 10 MiB·50,000행이다.
- userinfo/subscription overwrite는 기본 off다.

v6.2.0 importer의 UTF-8 오류·실패 복구 결함은 프론트 변경으로 근본 해결할 수 없다. 이번 초기 설정에서는 수정하지 않는다.

## 계정 발신자와 인증된 답장 주소 (LST-8)

캠페인 편집의 From을 계정 기준으로 정하고, 외부 주소는 메일함 소유 확인 후 Reply-To로 사용한다.
세 설정은 일반 설정 화면에서 편집하며 새 설치·업그레이드 기본값은 모두 비어 있다.

- `axm.sender_domains` (`[]`): 계정 이메일을 From으로 쓰는 도메인. 정확히 일치하는 도메인만 허용하며 서브도메인은 포함하지 않는다. 빈 배열이면 기능이 꺼져 upstream의 From 입력·발송 동작을 유지한다.
- `axm.default_from` (`""`): 그 외 계정과 API 사용자의 From. 비면 `app.from_email`을 쓴다. 입력한 주소는 허용 도메인에 속해야 한다. 시스템 메일의 From은 바뀌지 않는다.
- `axm.default_reply_to` (`""`): 그 외 계정이 새 캠페인을 만들 때 채우는 Reply-To. 외부 주소라면 이 기본값도 인증이 필요하다. 캠페인에서 다른 허용 주소로 바꾸거나 비울 수 있다.

기능을 켜면 생성·저장 및 초안 시작·예약 요청의 로그인 사용자로 From을 계산한다. 표시 이름은 계정 Name이다. API 요청의 `from_email`은 무시한다. 이미 실행·일시정지·완료된 캠페인의 From은 보존하며, 재개해도 바꾸지 않는다. 기존 예약 캠페인은 자동으로 주소를 이전하지 않고 원래 From으로 나가므로 필요하면 초안으로 돌려 다시 예약한다. 복제는 Reply-To를 복사하고 From은 복제한 사용자로 계산한다. 트랜잭션·알림·옵트인 확인 메일은 이 규칙의 대상이 아니다.

`reply_to`는 단일 주소(표시 이름 포함 가능) 또는 빈 값이다. 빈 값이면 헤더를 생략해 답장이 From으로 간다. 허용 도메인은 바로 사용할 수 있고 외부 주소는 `sender_addresses`에서 인증되어야 한다. 미인증 주소도 발송 날짜가 있는 초안 저장은 허용하지만 시작·예약·테스트 발송 및 예약 캠페인 수정의 발송 경로는 차단한다. 날짜 저장만으로 초안이 예약 상태가 되지는 않는다. 기능이 켜져 있으면 캠페인 사용자 지정 headers의 `From`, `Reply-To`, `Sender`, `Return-Path`(대소문자 무관)는 거부한다. 이메일 메신저에만 Reply-To 헤더를 붙인다.

인증은 전역으로 공유한다. 캠페인 편집 권한자(`campaigns:manage` / `campaigns:manage_all`)가 `/api/sender-addresses`에서 목록 조회·추가·재발송을 한다. 요청자 본인 또는 설정 관리자가 삭제할 수 있고, 예약·실행·일시정지 캠페인이 사용 중이면 409로 거부한다. 별도 관리자 승인 단계나 인증 만료는 없다. 링크는 24시간·1회용이며 DB에는 SHA-256 해시만 보관한다. 동일 주소의 재발송은 60초 간격이다. 공개 `/sender/verify/:token` GET은 화면만 열고 POST 확인 버튼을 눌러야 확정한다. 메일 스캐너의 미리 열기로 인증되지 않으며 이 경로에는 크롤러 차단을 적용하지 않는다.

마이그레이션 **`v6.2.1-axm.2`**는 LST-7의 `.1` 뒤에 실행한다. `campaigns.reply_to` 컬럼, `sender_addresses` 테이블·인덱스, 세 설정 행만 추가하며 기존 캠페인 주소는 변경하지 않는다. **DB 백업 → 새 이미지의 `./listmonk --upgrade --yes` → 새 이미지 기동** 순서다. 기능은 빈 기본값으로 꺼져 있으므로 활성화와 실제 운영 주소 입력·기본 Reply-To 인증은 배포 뒤 사람이 수행한다.

공식 `listmonk/listmonk:v6.2.0` 이미지로 업그레이드된 로컬 DB를 기동해 `/api/campaigns`가 200으로 열리는 것을 확인했다(2026-11-10). 추가 컬럼을 무시하므로 이 버전에서는 컬럼 삭제 없이 이미지 롤백이 가능하다. 공식 이미지는 이 발신 규칙과 새 Reply-To를 적용하지 않는다. 다른 빌드에서 추가 컬럼 스캔 오류가 실제로 발생한다면 백업 뒤 `ALTER TABLE campaigns DROP COLUMN reply_to`로 제거할 수 있으나 저장된 답장 주소를 잃는다. 포크로 복귀하기 전에는 백업에서 컬럼·데이터를 복원하거나 마이그레이션의 추가 SQL을 다시 적용해야 한다(기록 번호가 이미 `.2`이면 `--upgrade`만으로는 재생성되지 않는다).

운영에서는 계정 프로필 이메일을 사용자가 스스로 바꿀 수 있다는 기존 권한 범위를 수용한다. 개인 SES identity의 커스텀 MAIL FROM 미설정은 이 코드가 감지·수정하지 않으므로 주소별 수신 원문과 SES 이벤트를 확인한 뒤 사용한다. DNS·SES·Terraform·운영 설정 변경은 이 기능의 마이그레이션에 포함되지 않는다.

## CSV 열 맞춤

구독자 → 가져오기에서 CSV를 선택하면 브라우저 Worker가 UTF-8(BOM 포함) 또는 CP949를 엄격히 읽는다.
원본 시트의 A·B·C… 열 머리글에서 이메일·이름·속성(문자열)·태그(쉼표 목록)·건너뛰기를 지정한다.
이메일은 하나, 이름은 최대 하나이며 속성 키는 중복될 수 없다. 신규 키는 영문 snake_case이고,
선택한 리스트 최대 5개에서 최근 50명씩 찾은 기존 키와 브라우저에 기억된 키도 선택할 수 있다.
키 샘플에는 누락이 있을 수 있으며 조회 권한이 없으면 직접 입력으로 진행할 수 있다.

일반 값은 trim한 문자열로 유지하고 태그만 쉼표로 나눠 빈 값·중복을 제거한 배열로 저장한다.
빈 속성 셀은 키를 생략한다. 이메일을 정리한 뒤 빈 값·형식 오류·CSV 오류를 제외하고,
파일 안 중복은 첫 행만 남긴다. 따옴표 오류는 뒤쪽 행의 경계와 개수를 확신할 수 없어 파일 전체를 거절한다.
예시와 미리보기의 이메일은 가린다.
브라우저 검사가 서버의 최종 이메일·권한·도메인 검증을 대신하지는 않는다.
검사 결과를 확인해 업로드하면 email,name,attributes UTF-8 CSV로 재구성하여 기존 import API로 보낸다.
열 맞춤에는 원본 10 MiB·데이터 50,000행·결과 20 MiB 상한이 있다.

표준 헤더와 UTF-8인 CSV는 원본 업로드가 기본이며 기존 중첩 attributes JSON을 보존한다.
원본 업로드에는 열 맞춤 상한을 적용하지 않는다. 10 MiB를 넘으면 Worker가 512 KiB씩 읽어
전체 UTF-8 유효성을 검사하고 헤더 일부만 보관하며, 파일 전체를 변환용 메모리에 복사하지 않는다.
CP949는 열 맞춤을 거쳐야 한다. ZIP 내부 열 맞춤·XLSX·속성 부분 병합은 지원하지 않는다.
사용자 정보 덮어쓰기는 기존 속성 전체를 대체하며 두 덮어쓰기 옵션은 기본 off다.
같은 화면에서 시작한 열 맞춤 작업은 보낸 행 수와 서버 imported 수가 다르면 완료 상태여도 경고한다.
새로 고친 화면에서는 이전 작업에 보낸 행 수를 알 수 없다. 최종 결과는 서버 로그와 함께 대조한다.

맞춤은 업로드 시작 시 localStorage의 listmonk.axm.importMapping.v1에 열 이름·지정·키만 최대 200개 기억한다.
구독자 값은 저장하지 않으며 저장소 사용이 불가능해도 동작한다.
Go·SQL·schema·기존 API 계약 변경과 DB 마이그레이션은 없다.
영어·한국어 문구를 제공하며 다른 언어는 기존 영어 fallback을 사용한다.

Dockerfile.axm은 Node 순수 모듈 시험을 실행한다. axm-image는 packed 이미지의 실제 Worker를 사용하는
Chromium Cypress 시험도 거친 뒤 태그 조건을 만족할 때만 게시한다.
CP949 확장 한글(똠, 8C 63)은 브라우저에서 검증하며 Node TextDecoder 결과를 대신 사용하지 않는다.
