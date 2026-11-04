# homie

A small chezmoi-style dotfile manager that also installs your user-level tools. Single Go binary,
macOS / Linux / Windows, never needs sudo.

## Install

    curl -fsSL https://raw.githubusercontent.com/ryanzhangtianran/homie/main/scripts/install.sh | sh     # macOS/Linux
    irm https://raw.githubusercontent.com/ryanzhangtianran/homie/main/scripts/install.ps1 | iex          # Windows
    # or: go install github.com/ryanzhangtianran/homie@latest

Run `homie` for the interactive UI; `homie help` lists commands.

## Bind your GitHub repo

    homie init you/dotfiles      # clones it if it exists; otherwise creates it (private, via `gh`) and binds it
    homie add ~/.zshrc
    homie push "add zsh"
    homie init you/dotfiles@dev  # a specific branch
    homie remote other/repo      # change the binding later

New machine: `homie init you/dotfiles && homie apply`. Later: `homie update` (pull + apply + upgrade).
For HTTPS auth run `gh auth login` once, or use an SSH URL.

## Source repo layout (~/.local/share/homie)

| Source name                         | Becomes                                       |
|-------------------------------------|-----------------------------------------------|
| `dot_bashrc`                        | `~/.bashrc`                                   |
| `private_dot_config/...`            | `~/.config/...`, dir mode 0700                |
| `private_x` / `executable_x`        | file mode 0600 / 0755                         |
| `dot_gitconfig.tmpl`                | rendered Go template                          |
| `dot_bashrc##server`                | used instead of `dot_bashrc` on `server`      |
| `dot_zshrc##mac,darwin`             | only on machines named mac, or any macOS      |
| `run_x.sh` / `run_onchange_x.sh`    | run after every apply / when its content changes (`.ps1` on Windows) |
| `.anything`                         | metadata, never copied (e.g. `.homie.toml`, `.git`) |

A variant tag matches the machine name or the OS (`darwin`, `linux`, `windows`).
The machine name comes from `$HOMIE_MACHINE`, `homie machine <name>`, or `machine_detect` in `.homie.toml`.
Otherwise it is the OS.

Templates see `.os .arch .hostname .machine .home` and can use `include "path"` and `sha256sum`:

    {{ if eq .machine "server" "desktop" }}export PROXY=...{{ end }}

`homie add -t server ~/.bashrc` stores a file as the `##server` variant.
`apply` backs up every file it overwrites to `~/.local/state/homie/backup/<time>/`.

## Packages (.homie.toml)

One line per app, like a dependency list:

    [packages]
    fzf    = "gh:junegunn/fzf"                                 # latest GitHub release
    claude = "curl -fsSL https://claude.ai/install.sh | bash"   # any other string: an install command

    homie add-pkg bat gh:sharkdp/bat     # appends the line and installs it (like `uv add`)

For `gh:` homie picks this OS/arch's asset from the release by name. On Linux it prefers musl builds,
which run on old distros. It installs the archive's executables into `~/.local/bin` and records the
version, so `outdated`, `upgrade` and `uninstall` work. An install command is run with sh (PowerShell
on Windows) only when the program is missing; `homie install <name>` reruns it on purpose.

Use a table when one line is not enough: per-OS/machine differences (`on.windows = "..."`),
`only = ["server"]`, `bins`, an explicit `url` with `{os} {arch} {rust} {version}`, `files`, `remove`.
See [default.toml](internal/config/default.toml), which `homie init` writes for a new repo.

`apply` installs only missing packages, so it stays offline otherwise. `upgrade` fetches the latest
versions, and `uninstall` removes only what homie put under `$HOME`.

## Code layout

    main.go, tui.go        command line and interactive menu
    internal/config        paths, .homie.toml parsing (default.toml), machine name
    internal/dotfiles      source scan, apply/add/status/diff, run_ scripts, git repo binding
    internal/pkgs          package install/upgrade, release asset picking, archives
    internal/ui            boxes, columns, color themes
    scripts/               install.sh / install.ps1

## Release

Push a `v*` tag. The GitHub workflow builds the `homie_<os>_<arch>` binaries that `self-update`
and the install scripts download.
