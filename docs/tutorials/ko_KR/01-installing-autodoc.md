# AutoDoc 설치하기

AutoDoc은 `autodoc`이라는 프로그램 하나입니다. 사용하는 컴퓨터에 맞는 방법을 고르세요.

| 환경 | 설치 방법 |
| --- | --- |
| Linux, macOS (Go 사용) | 창 버전: `GOBIN=~/.local/bin go install -tags gui github.com/yongjohnlee80/autodoc/cmd/autodoc@latest` |
| Go가 있다면 어디서나 | 터미널 버전: `go install github.com/yongjohnlee80/autodoc/cmd/autodoc@latest` |
| macOS, Linux | mise: `mise use -g github:yongjohnlee80/autodoc` (창 버전) |
| Linux (서버나 컨테이너) | mise로 터미널 버전: `mise use -g 'github:yongjohnlee80/autodoc[asset_pattern=autodoc-*-linux-amd64-tui.tar.gz]'` (arm64에서는 `linux-arm64-tui`) |
| macOS, Linux | Homebrew: `brew install yongjohnlee80/tap/autodoc` (Linux에서는 터미널 버전) |
| Linux, macOS | 프로젝트 Releases 페이지의 릴리스 아카이브 (`amd64` 또는 `arm64`) |
| Windows | WSL2를 설치한 뒤 Linux 방법 중 하나로 |

**실행 방법은 두 가지입니다.** `autodoc --ui`는 터미널 안에서 실행됩니다. `autodoc --gui`는 자체 창을 열고, 다이어그램을 그려 주며 원하는 글꼴을 쓸 수 있습니다. `--gui`는 실행할 때 쓰는 플래그이고, `-tags gui`는 Go가 창 버전을 빌드하는 빌드 태그입니다. Linux에서 mise와 접미사 없는 릴리스 아카이브는 창 버전이며, `--ui`로 실행할 때도 데스크톱의 그래픽 라이브러리(EGL, Wayland, X11)가 필요합니다. 서버나 컨테이너에서는 `-tui` 아카이브를 쓰세요. Linux의 Homebrew는 터미널 버전만 설치합니다.

```mermaid
flowchart LR
  I["autodoc 설치"] --> U["터미널에서 autodoc --ui"]
  I --> G["창에서 autodoc --gui"]
  U --> D[(데몬)]
  G --> D
```

**데몬은 알아서 시작합니다.** 두 방식 모두 파일 색인을 관리하는 백그라운드 프로그램인 데몬과 통신합니다. 처음 실행한 `autodoc --ui`나 `--gui`가 데몬을 시작하고, 이후 실행은 같은 데몬을 함께 씁니다. 직접 시작할 필요는 없습니다.

`autodoc --version`으로 설치된 버전을 확인할 수 있습니다.

더 보기: [레퍼런스 2장: 빠른 시작과 워크스페이스](../../reference/02-quickstart-and-workspaces.md).
