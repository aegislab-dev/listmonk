# AXM listmonk fork

이 저장소는 [knadh/listmonk](https://github.com/knadh/listmonk)의 공개 수정판(AGPL-3.0)입니다.
2026-11-10: 프론트 포함 이미지 빌드와 화면 하단 소스 링크를 추가했습니다. 이후 CSV 열 맞춤, 에디터 개인화, 동적 리스트, 추가 정보 열, 감사 기록·휴지통 등을 구현했고 기능 목록과 변경 범위는 [FORK.md](FORK.md)에 있습니다.
기본 브랜치는 **`axm/v6.2`**, 배포 버전별 소스는 **`v6.2.0-axm.N` 태그**, 이미지는 **`ghcr.io/aegislab-dev/listmonk`**(GHCR 게시됨)에서 제공합니다.
이 저장소는 「무엇을 만들었나」를 적습니다. 운영 버전 고정·기록, 인프라, 사이드카, 백업, 경보는 비공개 `aegislab-dev/newsletter`가 맡으며([경계 문서](https://github.com/aegislab-dev/newsletter/blob/main/docs/listmonk-fork-boundary.md), 비공개라 외부인은 열 수 없습니다), 이 저장소의 「레포 경계」 절([FORK.md](FORK.md#레포-경계))도 참고하세요.
빌드·릴리스·upstream 추종·Go/DB 변경 규칙은 [FORK.md](FORK.md)를 참고하세요.

Public AGPL-3.0 fork of knadh/listmonk; see [FORK.md](FORK.md) for modifications, versioned source and build/release rules.

---

<a href="https://zerodha.tech"><img src="https://zerodha.tech/static/images/github-badge.svg" align="right" /></a>

[![listmonk-logo](https://user-images.githubusercontent.com/547147/231084896-835dba66-2dfe-497c-ba0f-787564c0819e.png)](https://listmonk.app)

listmonk is a standalone, self-hosted, newsletter and mailing list manager. It is fast, feature-rich, and packed into a single binary. It uses a PostgreSQL database as its data store.

[![listmonk-dashboard](https://github.com/user-attachments/assets/689b5fbb-dd25-4956-a36f-e3226a65f9c4)](https://listmonk.app)

Visit [listmonk.app](https://listmonk.app) for more info. Check out the [**live demo**](https://demo.listmonk.app).

## Installation

### Docker

The latest image is available on DockerHub at [`listmonk/listmonk:latest`](https://hub.docker.com/r/listmonk/listmonk/tags?page=1&ordering=last_updated&name=latest).
Download and use the sample [docker-compose.yml](https://github.com/knadh/listmonk/blob/master/docker-compose.yml).


```shell
# Download the compose file to the current directory.
curl -LO https://github.com/knadh/listmonk/raw/master/docker-compose.yml

# Run the services in the background.
docker compose up -d
```
Visit `http://localhost:9000`

See [installation docs](https://listmonk.app/docs/installation)

__________________

### Binary
- Download the [latest release](https://github.com/knadh/listmonk/releases) and extract the listmonk binary.
- `./listmonk --new-config` to generate config.toml. Edit it.
- `./listmonk --install` to setup the Postgres DB (or `--upgrade` to upgrade an existing DB. Upgrades are idempotent and running them multiple times have no side effects).
- Run `./listmonk` and visit `http://localhost:9000`

See [installation docs](https://listmonk.app/docs/installation)
__________________


## Developers
listmonk is free and open source software licensed under AGPLv3. If you are interested in contributing, refer to the [developer setup](https://listmonk.app/docs/developer-setup). The backend is written in Go and the frontend is Vue with Buefy for UI. 


## License
listmonk is licensed under the AGPL v3 license.
