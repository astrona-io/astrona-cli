# Lab Resources

Labs and playgrounds ship scripts, manifests and other files in `resources/`
next to `config.yaml`. Astrona snapshots them when the lab starts. Nothing runs
until the student asks.

## Inside the lab terminal

Open a lab tab, `astrona shell <lab>`, or `astrona ssh <vm>`. Astrona provides an
editable **`~/resources`** folder and a resource-only `astrona` helper:

```sh
astrona res
ls ~/resources
astrona res show count-versions.sh
astrona res run count-versions.sh -- "${SCOUT}/0"
bash ~/resources/count-versions.sh "${SCOUT}/0"
```

The full relative filename and the existing short name both work:
`count-versions.sh` and `count-versions`. Nested entries such as `tools/check.sh`
retain their folder structure. In Kind Bash terminals, Tab completes resource
filenames after `astrona res run`, `show`, `path`, or `copy`.

`SCOUT` must be defined in the terminal where you run the command. Shell variables
are expanded there; Astrona passes the resulting arguments literally, including
spaces, quotes and arguments beginning with `-`. Use `--` before script arguments.

All terminal tabs for a Kind run share the same editable files. QEMU tabs for the
same VM share that VM's files. Opening another tab, reconnecting, or running a
resource again preserves edits. A full reset restores the shipped originals.
Different labs, and different VMs, have separate workspaces. A VM folder that
already exists without an Astrona receipt is preserved; move it aside before
initializing resources.

The helper supports `res` / `resource` / `resources` with `list`, `show`, `path`,
`copy`, and `run`. It runs entirely inside the terminal or VM. Lifecycle and
account commands still belong in your host terminal. The helper needs no host
socket or credentials. `copy` refuses an existing destination.

## From your host terminal

```sh
astrona res --lab <lab>
astrona res run count-versions.sh --lab <lab> -- "${SCOUT}/0"
astrona res copy broken-deployment.yaml
astrona res path broken-deployment.yaml
```

By default, `res run` enters the lab environment: an isolated tools container for
Kind, or the student account in the selected QEMU VM. It uses the same editable
resources as interactive terminal tabs, never silently falling back to host
execution. The command prints the lab, execution location and working directory.

For a Kind script that deliberately requires host tools, opt in explicitly:

```sh
astrona res run local-tool.sh --host --lab <lab> -- --example
```

`--host` can access your host files and uses your host tools. It is unavailable
inside the resource-only helper and for QEMU resources. The existing host
execution rules are retained for this explicit compatibility option.

Host `res path`, `show` and `copy` use the editable Kind workspace when it has
been initialized; otherwise they use the original snapshot. For QEMU, use those
commands inside the VM to inspect edits; the host has the shipped snapshot.

## Execution rules

| Resource | Runs as | Working directory in the lab |
| --- | --- | --- |
| Has `run:` | The command split into arguments, without shell evaluation | Resource folder, or containing folder for a file |
| `*.sh` | `bash <file>` | Containing folder |
| Kind `*.yaml` / `*.yml` | `kubectl apply -f <file>` | Containing folder |
| Executable with `#!` | The file itself | Containing folder |
| Other file or folder without `run:` | Use `show`, `copy` or `path` | — |

Sibling files are available without additional copying. `$ASTRONA_RESOURCE_DIR`
points at all the lab's resources. `$ASTRONA_LAB` identifies the selected lab.
Kind commands use the lab's kubeconfigs and linked clusters, without mounting
host homes, SSH agents or container engine sockets. Scripts requiring tools not
included in the terminal image must supply those tools or explicitly use `--host`.

For QEMU, select a VM with `--vm <name>`, the resource's `vm:` field, or the lab's
only VM. A multi-VM lab without a selection reports which VM to choose. YAML is
a plain file in QEMU. Resources are installed through the student SSH identity,
without sudo, on first access. Subsequent access checks the workspace receipt
without re-uploading files. Custom SSH users/password sessions do not provision
another user's resources automatically.

## Selecting a lab

From the host, Astrona picks resources in this order:

1. `--lab <lab>` (running name or catalog name).
2. `$ASTRONA_LAB`.
3. The lab selected by `-c` / `--git` or `astrona use`.
4. The only lab with resource snapshots.

Several possible labs cause an error listing the choices. Inside a lab terminal,
the helper always works on that terminal's workspace.

## Authoring and sharing

Keep resources in the same repository and pinned revision as the task:

```text
playground/
  config.yaml
  resources/
    count-versions.sh
    examples.json
    tools/
      check.sh
```

Everything directly in `resources/` is included automatically, including folder
contents. The optional `resources:` list adds descriptions and custom commands,
or exposes a nested file as its own named resource. No separate student download
or copy step is needed. See the [resource configuration reference](../reference/lab-config.md#resources).

Resource short names must be unique. Remote code approval covers the exact lab
revision and its resources. Copies reject symlinks and escaping paths and retain
the existing file-count and size limits. Nothing executes while copying or opening
a terminal. Host snapshots live under `~/.astrona/resources/<lab>`; development
builds use `~/.astrona-dev`. Kind edits live under `runtime/<lab>/work/resources`.
Destroy removes the workspace; `--keep-resources` retains the original snapshot.
