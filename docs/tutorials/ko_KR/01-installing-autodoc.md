# AutoDoc 설치하기

AutoDoc은 `autodoc`이라는 프로그램 하나입니다.

## 설치 스크립트 (권장)

Linux와 macOS에서는 다음 한 줄로 창 버전 AutoDoc을 설치합니다.

```sh
curl -fsSL https://github.com/yongjohnlee80/autodoc/releases/latest/download/install-autodoc.sh | sh
```

이 스크립트는 다음을 합니다.

- 사용하는 시스템에 맞는 최신 릴리스를 내려받고, 릴리스의 체크섬으로 검증합니다.
- `~/.local/bin/autodoc`으로, 현재 사용자에게만 설치합니다. `sudo`는 절대 요구하지 않습니다.
- 런처를 추가해 AutoDoc이 애플리케이션 목록에 나타나게 합니다 (macOS에서는 AutoDoc.app, Linux에서는 데스크톱 항목).
- AutoDoc을 최신 상태로 유지하는 `update.sh`를 `~/.local/state/autodoc`에 설치합니다.

`~/.local/bin`이 `PATH`에 없으면, 셸 시작 파일에 추가할 줄을 알려 줍니다. 그 파일을 직접 고치지는 않습니다. 시스템에 맞는 미리 빌드된 버전이 없으면, 릴리스 소스로 빌드할지 먼저 묻습니다.

언제든 다시 실행해도 됩니다. 설치를 업그레이드하거나 복구하고, 런처를 두 번 추가하지 않습니다. `sh install-autodoc.sh --help`로 옵션 목록을 볼 수 있습니다 (특정 릴리스를 설치하는 `--version`, `--no-launcher` 등).

**업데이트.** 스크립트로 설치한 AutoDoc은 스스로 업데이트합니다. **도움말 › 정보**에서 **업데이트**를 누르세요. 업데이트는 AutoDoc의 터미널 패널에서 실행되고, 새 버전이 준비되면 **지금 다시 시작**을 제안합니다. 셸에서는 `~/.local/state/autodoc/update.sh`가 같은 일을 합니다.

## 다른 방법

- Homebrew: `brew install yongjohnlee80/tap/autodoc` (Linux에서는 터미널 버전만)
- mise: `mise use -g github:yongjohnlee80/autodoc`
- Go: `go install -tags gui github.com/yongjohnlee80/autodoc/cmd/autodoc@latest` (`-tags gui` 없이는 터미널 버전만)
- 프로젝트 Releases 페이지의 릴리스 아카이브
- Windows: WSL2를 설치한 뒤 설치 스크립트로

이 방법들은 설치한 방식 그대로 업데이트합니다 (`brew upgrade`, `mise upgrade`, `go install` 다시 실행). 그래서 **도움말 › 정보**에 업데이트 버튼이 나오지 않습니다. 서버, 컨테이너, 소스에서 빌드하기는 [레퍼런스 2장](../../reference/02-quickstart-and-workspaces.md)을 보세요.

**실행 방법은 두 가지입니다.** `autodoc --ui`는 터미널 안에서 실행됩니다. `autodoc --gui`는 자체 창을 열고, 다이어그램을 그려 주며 원하는 글꼴을 쓸 수 있습니다.

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
