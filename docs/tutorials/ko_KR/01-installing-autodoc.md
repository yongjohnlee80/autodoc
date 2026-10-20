# AutoDoc 설치하기

AutoDoc은 `autodoc`이라는 프로그램 하나입니다. 사용하는 컴퓨터에 맞는 방법을 고르세요.

| 환경 | 설치 방법 |
| --- | --- |
| macOS, Linux | Homebrew: `brew install yongjohnlee80/tap/autodoc` |
| Linux, macOS | mise: `mise use -g github:yongjohnlee80/autodoc` |
| Linux, macOS | 프로젝트 Releases 페이지의 릴리스 아카이브 (`amd64` 또는 `arm64`) |
| Go가 있다면 어디서나 | `go install github.com/yongjohnlee80/autodoc/cmd/autodoc@latest` |
| Windows | WSL2를 설치한 뒤 Linux 방법 중 하나로 |

**실행 방법은 두 가지입니다.** `autodoc --ui`는 터미널 안에서 실행됩니다. `autodoc --gui`는 자체 창을 열고, 다이어그램을 그려 주며 원하는 글꼴을 쓸 수 있습니다. Linux에서 창으로 쓰려면 `-gui` 릴리스 아카이브나 `go install -tags gui …`가 필요합니다.

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
