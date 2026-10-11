# Facet

Facet is a video studio for the AI agent you already use: Claude Code, Codex,
GitHub Copilot CLI, OpenCode or Compa. It brings a studio's thinking (the
brief, research, concepts, a proposal, the story and a plan for every scene)
and then the making: narration, stock and generated media, music, editing,
composition in Remotion, HyperFrames or FFmpeg, captions, mixing and review.
Your agent stays in charge of the model, the tool calls and the approvals;
Facet never calls a model.

| Part | What it is |
|---|---|
| **The `facet` command** | 35 media tools that probe, edit, narrate, compose, caption, generate and review video; 12 production pipelines with their stage guides; and an MCP server, `facet mcp`. |
| **The bundle** | What `facet wire` puts in your agent's CLI: the `facet` skill (the guide), the `facet` producer agent where the CLI has selectable agents (Copilot CLI, OpenCode), the role agents (researcher, critic, reference analyst), the MCP server, and a rule to ask before each of the 8 tools that may charge. |
| **The App** | Facet in its own window, a page in your browser, for people who don't work in a terminal agent. It runs a private [Compa](https://github.com/kayiik/compa) kernel with Facet wired in, and tools that may charge always ask in the window first. |

## Install

Windows, in PowerShell:

```powershell
irm https://kayiik.github.io/facet/install.ps1 | iex
```

macOS and Linux:

```sh
curl -fsSL https://kayiik.github.io/facet/install.sh | sh
```

This installs the App and the `facet` command for your user account only, with
no administrator rights, asks which components to install, and starts the
App. On Windows they go to `%LOCALAPPDATA%\Programs\Facet`, whose `bin` folder
is added to your PATH, with Facet in the Start menu. On macOS and Linux they go
to `~/.local/lib/facet`, with `facet` and `facet-app` linked into
`~/.local/bin` (which must be on your PATH) and Facet in your Applications
folder or application menu.

To use Facet from your agent, install the `facet` command and wire it into
your CLIs (`claude`, `codex`, `copilot`, `opencode` or `compa`):

```powershell
& ([scriptblock]::Create((irm https://kayiik.github.io/facet/install.ps1))) -Mode bundle -Harness claude,opencode
```

```sh
curl -fsSL https://kayiik.github.io/facet/install.sh | sh -s -- --mode bundle --harness claude,opencode
```

Use `-Mode core` (`--mode core`) for the `facet` command alone. Run the
installer again with `-Upgrade` (`--upgrade`) to move to a newer release,
`-Verify` to check every installed file, or `-Uninstall` to remove Facet with
its wirings and components; your projects, Facet's records in `~/.facet` and
the App's data are kept. `-DryRun` prints the plan and changes nothing.

Every file is checked against the release's SHA-256 checksums. The programs
are not code-signed, so Windows may warn that the publisher is unknown.

### Components

Facet's tools run on programs that `facet` installs beside itself, pinned and
checked against their SHA-256: FFmpeg, Node.js, a browser (the Chrome headless
shell), Remotion and HyperFrames by default, and Piper with an English voice
when you ask for it (it needs Python 3.9 or newer). A supported copy already on
your computer is used instead. The installer ends with `facet setup`, which
asks; later:

```sh
facet components          # what is installed, on this computer, or missing
facet install hyperframes # or ffmpeg, node, browser, remotion, piper, defaults, all
facet remove piper
```

## Use

```sh
facet wire claude    # or codex, copilot, opencode, compa, a comma-separated list, or all
facet doctor
```

Start a new session in your CLI from a project folder (in Copilot CLI and
OpenCode, choose the `facet` agent) and ask:

> Make a 45-second animated explainer about why the sky is blue.

Facet's guide has your agent work like a studio: it picks a production
pipeline, works through the brief, research, concepts and a proposal, writes
and tests the story, plans every scene, and stops for your approval at each of
the pipeline's approval points. It announces every paid call before making it,
with its credit note, for example "uses your OpenAI API credits", and a
complete free path always exists.

`facet wire` installs the skill and agents, registers the `facet` MCP server,
and adds a rule to ask before each tool that may charge (Claude Code, Codex,
OpenCode and Compa; Copilot CLI already asks before every tool that is not
read-only). Facet declares each tool's effects; your CLI enforces consent.

Claude Code, Codex and Copilot CLI can instead add Facet as a plugin from this
repository's marketplace, once the `facet` command is installed:

```sh
claude plugin marketplace add kayiik/facet && claude plugin install facet@facet
codex plugin marketplace add kayiik/facet && codex plugin add facet@facet
copilot plugin marketplace add kayiik/facet && copilot plugin install facet@facet
```

Use the plugin or `facet wire` for a CLI, not both.

People and scripts can run the same tools directly:

```sh
facet capabilities
facet tools describe video_compose
facet tools run video_compose --input request.json
```

## Development

Go 1.26, Node.js and Python 3.12. FFmpeg for the tool tests.

```sh
go build ./...
go test -short ./...
node --test "bin/*.test.js" "scripts/*.test.mjs"
python -m unittest discover -s scripts -p "test_*.py"
```

`scripts/dev.ps1` (Windows) and `scripts/dev.sh` build `facet` into
`build/dev`, laid out like an installed runtime: the composer is linked to
this checkout and HyperFrames is installed with `facet install`. They wire the
build into Claude Code and OpenCode unless you pass `-NoWire` (`NO_WIRE=1`).

The App is `cmd/facet-app`, with its packages in `internal/app`: `kernel`
(the agent engine, implemented with Compa's web channel and a stdio hook),
`toolkit` (runs `facet` for the App's own screens, only tools that cannot
charge and are read-only or local), `journeys` (projects, technical review
and review decisions) and `window` (the page and its API, on `127.0.0.1`
with a per-run token). It reaches Facet only through the `facet` command.
Its integration tests need a `compa-kernel` and a `facet`; without them they
are skipped:

```sh
FACET_APP_KERNEL=/path/to/compa-kernel FACET_APP_FACET=/path/to/facet go test ./internal/app/...
```

A release is both products for Windows, macOS and Linux on amd64 and arm64.
Build and check one from a clean checkout, into an empty folder outside it,
and install from it (here for Linux on amd64; `--targets` takes any of the
six):

```sh
python scripts/build-release.py --out "$HOME/facet-release" --targets linux/amd64
python scripts/verify-release.py --directory "$HOME/facet-release" --smoke
sh install.sh --mode core --distribution-dir "$HOME/facet-release"
```

The Release workflow builds every target from a `vX.Y.Z` tag on `main`,
installs the result on each platform, attests its build provenance and
drafts the GitHub release.

## License

Facet is licensed under
[AGPL-3.0-or-later](https://github.com/kayiik/facet/blob/main/LICENSE). See
[THIRD_PARTY_NOTICES.md](https://github.com/kayiik/facet/blob/main/THIRD_PARTY_NOTICES.md)
for third-party notices.
