# Emacs Jail MCP Manual

Emacs Jail MCP

Manages an Emacs instance running inside a copy-on-write disposable sandbox. Allows LLMs to control sandbox container, run elisp code, and capture logs and screenshots.

```text
emacs-jail-mcp [command] [global flags] [command flags]
```

### Commands
- [emacs-jail-mcp serve](#emacs-jail-mcp-serve)
- [emacs-jail-mcp info](#emacs-jail-mcp-info)
- [emacs-jail-mcp send](#emacs-jail-mcp-send)
    - [emacs-jail-mcp send control](#emacs-jail-mcp-send-control)
    - [emacs-jail-mcp send eval](#emacs-jail-mcp-send-eval)
    - [emacs-jail-mcp send bytecomp](#emacs-jail-mcp-send-bytecomp)
    - [emacs-jail-mcp send shell](#emacs-jail-mcp-send-shell)
    - [emacs-jail-mcp send screenshot](#emacs-jail-mcp-send-screenshot)
    - [emacs-jail-mcp send logs](#emacs-jail-mcp-send-logs)
- [emacs-jail-mcp man](#emacs-jail-mcp-man)
- [emacs-jail-mcp completion](#emacs-jail-mcp-completion)
    - [emacs-jail-mcp completion bash](#emacs-jail-mcp-completion-bash)
    - [emacs-jail-mcp completion zsh](#emacs-jail-mcp-completion-zsh)
    - [emacs-jail-mcp completion fish](#emacs-jail-mcp-completion-fish)
    - [emacs-jail-mcp completion powershell](#emacs-jail-mcp-completion-powershell)


# Commands

## `emacs-jail-mcp serve`

Run the MCP server.

Without --stdio (default): listens on a TCP port (SSE transport) for MCP client and
CLI client connections.

With --stdio: reads MCP JSON-RPC messages from stdin/stdout (stdio transport).

```text
emacs-jail-mcp serve [flags]
```

### Command Flags

```text
      --stdio                     use stdio transport instead of TCP SSE transport
      --podman-binary string      podman binary name or path (default "podman")
      --image string              container image name or reference (default "emacs-jail:latest")
      --sudo                      run podman commands with sudo
      --no-sudo                   disable sudo for podman commands (deprecated)
      --display-width int         override auto-detected Xvfb display width in pixels
      --display-height int        override auto-detected Xvfb display height in pixels
      --display-depth int         Xvfb display color depth (default 24)
  -e, --emacs-binary string       emacs binary name or path (default "emacs")
      --emacs-launcher string     optional executable that wraps the emacs command
      --emacs-pre-init string     optional elisp file loaded from site-start.el before user init
      --emacs-post-init string    optional elisp file loaded after user init
      --emacs-socket-dir string   directory for the emacs-jail-rpc Unix socket (default "/tmp")
  -H, --mcp-host string           host for the MCP SSE server to bind to (default "127.0.0.1")
  -P, --mcp-port int              TCP port for the MCP SSE server (default 9421)
      --start-timeout duration    timeout waiting for jail to start (default 10m0s)
      --stop-timeout duration     timeout waiting for jail to stop (default 10s)
      --exec-timeout duration     timeout for elisp eval and shell commands (default 10m0s)
```

## `emacs-jail-mcp info`

Check whether the MCP SSE server is listening on the configured TCP address.

```text
emacs-jail-mcp info [flags]
```

### Command Flags

```text
  -H, --mcp-host string   host of the MCP SSE server to connect to (default "127.0.0.1")
  -P, --mcp-port int      TCP port of the MCP SSE server (default 9421)
```

## `emacs-jail-mcp send`

Send MCP requests from the command line.

Each subcommand corresponds to one MCP tool and connects to an already-running server via
its TCP port (SSE transport).

### `emacs-jail-mcp send control`

Control the jail lifecycle.

Specify exactly one action flag: --start, --stop, --restart, or --status.

```text
emacs-jail-mcp send control [flags]
```

**Aliases:** `ctl`

#### Command Flags

```text
      --start              start the jail
      --stop               stop the jail
      --restart            restart the jail
      --status             show jail status
      --timeout duration   optional timeout for --start or --restart, for example 5s
      --swallow-errors     swallow Emacs init errors and continue startup
```

### `emacs-jail-mcp send eval`

Evaluate an Emacs Lisp expression

```text
emacs-jail-mcp send eval EXPRESSION [flags]
```

**Aliases:** `ev`

### `emacs-jail-mcp send bytecomp`

Byte-compile an Elisp file and return diagnostics

```text
emacs-jail-mcp send bytecomp [flags]
```

**Aliases:** `bc`

#### Command Flags

```text
  -f, --file-path string   path to the Elisp file to byte-compile
  -s, --severity string    filter by severity: error or warning
```

### `emacs-jail-mcp send shell`

Run a shell command inside the jail container

```text
emacs-jail-mcp send shell COMMAND [flags]
```

**Aliases:** `sh`

### `emacs-jail-mcp send screenshot`

Capture a screenshot of the Emacs display

```text
emacs-jail-mcp send screenshot [flags]
```

**Aliases:** `sc`

#### Command Flags

```text
  -o, --output string   output file path for the PNG screenshot
```

### `emacs-jail-mcp send logs`

Fetch Emacs log and diagnostic output

```text
emacs-jail-mcp send logs [flags]
```

**Aliases:** `lg`

#### Command Flags

```text
  -s, --sources string   comma-separated log sources (messages, warnings, backtrace, compile_log, async_compile_log, init_log, stderr)
  -o, --offset int       1-based line offset within each source section
  -l, --limit int        max lines to return per source section
```

## `emacs-jail-mcp man`

Generate manual page

```text
emacs-jail-mcp man [flags]
```

### Command Flags

```text
      --format string   output format: troff or markdown (default "troff")
```

## `emacs-jail-mcp completion`

Generate the autocompletion script for emacs-jail-mcp for the specified shell.
See each sub-command's help for details on how to use the generated script.


### `emacs-jail-mcp completion bash`

Generate the autocompletion script for the bash shell.

This script depends on the 'bash-completion' package.
If it is not installed already, you can install it via your OS's package manager.

To load completions in your current shell session:

	source <(emacs-jail-mcp completion bash)

To load completions for every new session, execute once:

#### Linux:

	emacs-jail-mcp completion bash > /etc/bash_completion.d/emacs-jail-mcp

#### macOS:

	emacs-jail-mcp completion bash > $(brew --prefix)/etc/bash_completion.d/emacs-jail-mcp

You will need to start a new shell for this setup to take effect.


```text
emacs-jail-mcp completion bash
```

#### Command Flags

```text
      --no-descriptions   disable completion descriptions
```

### `emacs-jail-mcp completion zsh`

Generate the autocompletion script for the zsh shell.

If shell completion is not already enabled in your environment you will need
to enable it.  You can execute the following once:

	echo "autoload -U compinit; compinit" >> ~/.zshrc

To load completions in your current shell session:

	source <(emacs-jail-mcp completion zsh)

To load completions for every new session, execute once:

#### Linux:

	emacs-jail-mcp completion zsh > "${fpath[1]}/_emacs-jail-mcp"

#### macOS:

	emacs-jail-mcp completion zsh > $(brew --prefix)/share/zsh/site-functions/_emacs-jail-mcp

You will need to start a new shell for this setup to take effect.


```text
emacs-jail-mcp completion zsh [flags]
```

#### Command Flags

```text
      --no-descriptions   disable completion descriptions
```

### `emacs-jail-mcp completion fish`

Generate the autocompletion script for the fish shell.

To load completions in your current shell session:

	emacs-jail-mcp completion fish | source

To load completions for every new session, execute once:

	emacs-jail-mcp completion fish > ~/.config/fish/completions/emacs-jail-mcp.fish

You will need to start a new shell for this setup to take effect.


```text
emacs-jail-mcp completion fish [flags]
```

#### Command Flags

```text
      --no-descriptions   disable completion descriptions
```

### `emacs-jail-mcp completion powershell`

Generate the autocompletion script for powershell.

To load completions in your current shell session:

	emacs-jail-mcp completion powershell | Out-String | Invoke-Expression

To load completions for every new session, add the output of the above command
to your powershell profile.


```text
emacs-jail-mcp completion powershell [flags]
```

#### Command Flags

```text
      --no-descriptions   disable completion descriptions
```

# Reporting Bugs

Please report bugs at https://github.com/gavv/emacs-jail-mcp

# Copyright

Copyright Victor Gaydov and contributors. See AUTHORS.md in Git repo.

# License

emacs-jail-mcp is licensed under the GNU General Public License version 3 or later.

See LICENSE in Git repo.

# History

See CHANGES.md in Git repo.

# See Also

emacs(1), podman(1), Xvfb(1)
